package repo

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sauron/deadliner/internal/domain"
)

const deadlineColumns = `id, group_id, owner_user_id, title, description, due_at,
	tz, created_by, status, created_at, updated_at, deleted_at`

type deadlinesRepo struct {
	pool *pgxpool.Pool
}

func NewDeadlines(pool *pgxpool.Pool) domain.DeadlineRepo {
	return &deadlinesRepo{pool: pool}
}

var _ domain.DeadlineRepo = (*deadlinesRepo)(nil)

func scanDeadline(row pgx.Row) (*domain.Deadline, error) {
	var (
		d      domain.Deadline
		status string
		descr  *string
		delAt  *time.Time
	)
	err := row.Scan(
		&d.ID, &d.GroupID, &d.OwnerUserID, &d.Title, &descr, &d.DueAt,
		&d.TZ, &d.CreatedBy, &status, &d.CreatedAt, &d.UpdatedAt, &delAt,
	)
	if err != nil {
		return nil, err
	}
	d.Status = domain.DeadlineStatus(status)
	if descr != nil {
		d.Description = *descr
	}
	d.DeletedAt = delAt
	return &d, nil
}

// Create пишет deadline и reminders в одной транзакции (спека §7.1):
// сбой вставки любого reminder откатывает и сам дедлайн. Заполняет d.ID и
// ID/DeadlineID reminder-строк.
func (r *deadlinesRepo) Create(ctx context.Context, d *domain.Deadline, reminders []domain.Reminder) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return mapErr(err)
	}
	defer tx.Rollback(ctx)

	err = tx.QueryRow(ctx,
		`INSERT INTO deadlines (group_id, owner_user_id, title, description, due_at,
			tz, created_by, status)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		 RETURNING id, created_at, updated_at`,
		d.GroupID, d.OwnerUserID, d.Title, nullIfEmpty(d.Description), d.DueAt,
		d.TZ, d.CreatedBy, string(d.Status)).
		Scan(&d.ID, &d.CreatedAt, &d.UpdatedAt)
	if err != nil {
		return mapErr(err)
	}

	for i := range reminders {
		reminders[i].DeadlineID = d.ID
		if err := insertReminder(ctx, tx, &reminders[i]); err != nil {
			return err
		}
	}
	return mapErr(tx.Commit(ctx))
}

func (r *deadlinesRepo) GetByID(ctx context.Context, id int64) (*domain.Deadline, error) {
	d, err := scanDeadline(r.pool.QueryRow(ctx,
		`SELECT `+deadlineColumns+` FROM deadlines
		 WHERE id = $1 AND deleted_at IS NULL`, id))
	if err != nil {
		return nil, mapNotFoundErr(err, fmt.Sprintf("deadline id=%d", id))
	}
	return d, nil
}

// Update меняет только явно заданные поля patch; COALESCE сохраняет текущее
// значение при nil. Пустая description нормализуется в NULL (как в Create) —
// «менять ли» решает отдельный флаг $3, а не NULL-значение.
// Обновление soft-deleted ряда → ErrNotFound.
func (r *deadlinesRepo) Update(ctx context.Context, id int64, patch domain.DeadlinePatch) error {
	var descr *string
	if patch.Description != nil {
		descr = nullIfEmpty(*patch.Description)
	}
	tag, err := r.pool.Exec(ctx,
		`UPDATE deadlines SET
			title = COALESCE($2, title),
			description = CASE WHEN $3 THEN $4 ELSE description END,
			due_at = COALESCE($5, due_at),
			tz = COALESCE($6, tz),
			updated_at = now()
		 WHERE id = $1 AND deleted_at IS NULL`,
		id, patch.Title, patch.Description != nil, descr, patch.DueAt, patch.TZ)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: deadline id=%d", domain.ErrNotFound, id)
	}
	return nil
}

func (r *deadlinesRepo) SetStatus(ctx context.Context, id int64, status domain.DeadlineStatus) error {
	switch status {
	case domain.DeadlineStatusActive, domain.DeadlineStatusDone, domain.DeadlineStatusArchived,
		domain.DeadlineStatusPendingApproval, domain.DeadlineStatusRejected:
	default:
		return fmt.Errorf("%w: deadline status %q", domain.ErrValidation, status)
	}
	tag, err := r.pool.Exec(ctx,
		`UPDATE deadlines SET status = $2, updated_at = now()
		 WHERE id = $1 AND deleted_at IS NULL`, id, string(status))
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: deadline id=%d", domain.ErrNotFound, id)
	}
	return nil
}

func (r *deadlinesRepo) SoftDelete(ctx context.Context, id int64) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE deadlines SET deleted_at = now(), updated_at = now()
		 WHERE id = $1 AND deleted_at IS NULL`, id)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: deadline id=%d", domain.ErrNotFound, id)
	}
	return nil
}

func (r *deadlinesRepo) ListByGroup(ctx context.Context, groupID int64, from, to *time.Time, status *domain.DeadlineStatus) ([]domain.Deadline, error) {
	return r.list(ctx, "group_id = $1", groupID, from, to, status)
}

func (r *deadlinesRepo) ListByOwner(ctx context.Context, ownerID int64, from, to *time.Time, status *domain.DeadlineStatus) ([]domain.Deadline, error) {
	return r.list(ctx, "owner_user_id = $1", ownerID, from, to, status)
}

// list — общий запрос списков: необязательные from/to/status, всегда
// deleted_at IS NULL и порядок due_at ASC.
func (r *deadlinesRepo) list(ctx context.Context, scopeCond string, scopeID int64, from, to *time.Time, status *domain.DeadlineStatus) ([]domain.Deadline, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+deadlineColumns+` FROM deadlines
		 WHERE deleted_at IS NULL AND `+scopeCond+`
		   AND ($2::timestamptz IS NULL OR due_at >= $2)
		   AND ($3::timestamptz IS NULL OR due_at <= $3)
		   AND ($4::text IS NULL OR status = $4)
		 ORDER BY due_at ASC`,
		scopeID, from, to, statusString(status))
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := []domain.Deadline{}
	for rows.Next() {
		d, err := scanDeadline(rows)
		if err != nil {
			return nil, mapErr(err)
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

func statusString(s *domain.DeadlineStatus) *string {
	if s == nil {
		return nil
	}
	v := string(*s)
	return &v
}

// nullIfEmpty сохраняет пустую строку как NULL (колонка description nullable).
func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
