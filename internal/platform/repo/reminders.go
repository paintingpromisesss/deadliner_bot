package repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sauron/deadliner/internal/domain"
)

const reminderColumns = `id, deadline_id, kind, target_user_id, offset_minutes,
	fire_at, status, attempts, locked_by, locked_at, last_error, sent_at`

type remindersRepo struct {
	pool *pgxpool.Pool
}

func NewReminders(pool *pgxpool.Pool) domain.ReminderRepo {
	return &remindersRepo{pool: pool}
}

var _ domain.ReminderRepo = (*remindersRepo)(nil)

func scanReminder(row pgx.Row) (*domain.Reminder, error) {
	var (
		r       domain.Reminder
		kind    string
		status  string
		lastErr *string
	)
	err := row.Scan(
		&r.ID, &r.DeadlineID, &kind, &r.TargetUserID, &r.OffsetMinutes, &r.FireAt, &status,
		&r.Attempts, &r.LockedBy, &r.LockedAt, &lastErr, &r.SentAt,
	)
	if err != nil {
		return nil, err
	}
	r.Kind = domain.ReminderKind(kind)
	r.Status = domain.ReminderStatus(status)
	if lastErr != nil {
		r.LastError = *lastErr
	}
	return &r, nil
}

// insertReminder строго вставляет одну строку внутри tx, заполняя ID.
// Дубликат на unique-индексе → ErrConflict: транзакция откатывается,
// строка не теряется молча (спека §7.1).
func insertReminder(ctx context.Context, q pgx.Tx, r *domain.Reminder) error {
	status := r.Status
	if status == "" {
		status = domain.ReminderStatusPending
	}
	row := q.QueryRow(ctx,
		`INSERT INTO reminders (deadline_id, kind, target_user_id, offset_minutes, fire_at, status)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING id`,
		r.DeadlineID, string(r.Kind), r.TargetUserID, r.OffsetMinutes, r.FireAt, string(status))
	if err := row.Scan(&r.ID); err != nil {
		return mapErr(err)
	}
	return nil
}

// upsertReminder — конфликт-толерантная запись для Regenerate.
// Unique-индексы не учитывают status, поэтому cancelled-строка с тем же
// (kind, offset) «воскрешается» в pending вместо вставки: для preset/
// custom_offset — старейшая по id cancelled-строка, если её новый fire_at не
// занят (иначе остаётся cancelled); для custom_at — cancelled-строка с точно
// тем же fire_at. Если кандидата нет — вставка с ON CONFLICT DO NOTHING.
// sent/failed не трогаются; true — строка стала pending.
func upsertReminder(ctx context.Context, q pgx.Tx, r *domain.Reminder) (bool, error) {
	var id int64
	var err error
	if r.OffsetMinutes == nil {
		err = q.QueryRow(ctx,
			`UPDATE reminders
			 SET status = 'pending', attempts = 0,
			     locked_by = NULL, locked_at = NULL, last_error = NULL, sent_at = NULL
			 WHERE id = (
			     SELECT c.id FROM reminders c
			      WHERE c.deadline_id = $1 AND c.kind = $2
			        AND c.fire_at = $3 AND c.status = 'cancelled'
			      ORDER BY c.id LIMIT 1)
			 RETURNING id`,
			r.DeadlineID, string(r.Kind), r.FireAt).Scan(&id)
	} else {
		err = q.QueryRow(ctx,
			`UPDATE reminders
			 SET fire_at = $4, status = 'pending', attempts = 0,
			     locked_by = NULL, locked_at = NULL, last_error = NULL, sent_at = NULL
			 WHERE id = (
			     SELECT c.id FROM reminders c
			      WHERE c.deadline_id = $1 AND c.kind = $2
			        AND c.offset_minutes = $3 AND c.status = 'cancelled'
			        AND NOT EXISTS (
			            SELECT 1 FROM reminders o
			             WHERE o.deadline_id = $1 AND o.kind = $2
			               AND o.fire_at = $4 AND o.id <> c.id)
			      ORDER BY c.id LIMIT 1)
			 RETURNING id`,
			r.DeadlineID, string(r.Kind), r.OffsetMinutes, r.FireAt).Scan(&id)
	}
	if err == nil {
		r.ID = id
		return true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, mapErr(err)
	}

	status := r.Status
	if status == "" {
		status = domain.ReminderStatusPending
	}
	err = q.QueryRow(ctx,
		`INSERT INTO reminders (deadline_id, kind, target_user_id, offset_minutes, fire_at, status)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 ON CONFLICT DO NOTHING
		 RETURNING id`,
		r.DeadlineID, string(r.Kind), r.TargetUserID, r.OffsetMinutes, r.FireAt, string(status)).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil // дубликат (гонка/повтор) — терпимо
	}
	if err != nil {
		return false, mapErr(err)
	}
	r.ID = id
	return true, nil
}

func (r *remindersRepo) CreateBatch(ctx context.Context, reminders []domain.Reminder) error {
	if len(reminders) == 0 {
		return nil
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return mapErr(err)
	}
	defer tx.Rollback(ctx)
	for i := range reminders {
		if err := insertReminder(ctx, tx, &reminders[i]); err != nil {
			return err
		}
	}
	return mapErr(tx.Commit(ctx))
}

func (r *remindersRepo) ListByDeadline(ctx context.Context, deadlineID int64) ([]domain.Reminder, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+reminderColumns+` FROM reminders
		 WHERE deadline_id = $1 ORDER BY fire_at, id`, deadlineID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	return collectReminders(rows)
}

// FetchDue (спека §7.2): в той же транзакции tx выбирает due-pending строки
// с FOR UPDATE SKIP LOCKED и помечает их locked_by/locked_at. SKIP LOCKED —
// гарантия отсутствия пересечений между конкурентными воркерами.
func (r *remindersRepo) FetchDue(ctx context.Context, tx domain.Tx, now time.Time, limit int, workerID string) ([]domain.Reminder, error) {
	rows, err := txQuery(ctx, tx,
		`SELECT `+reminderColumns+` FROM reminders
		 WHERE status = 'pending' AND fire_at <= $1 AND locked_by IS NULL
		 ORDER BY fire_at
		 LIMIT $2
		 FOR UPDATE SKIP LOCKED`, now, limit)
	if err != nil {
		return nil, mapErr(err)
	}
	batch, err := collectReminders(rows)
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(batch) == 0 {
		return batch, nil
	}

	ids := make([]int64, 0, len(batch))
	for _, rem := range batch {
		ids = append(ids, rem.ID)
	}
	if err := tx.Exec(ctx,
		`UPDATE reminders SET locked_by = $2, locked_at = $3
		 WHERE id = ANY($1)`, ids, workerID, now); err != nil {
		return nil, mapErr(err)
	}
	for i := range batch {
		w := workerID
		t := now
		batch[i].LockedBy = &w
		batch[i].LockedAt = &t
	}
	return batch, nil
}

// PgxTx — экспортируемый адаптер pgx.Tx под domain.Tx (Exec/QueryRow/Query).
// Query не входит в порт domain.Tx (domain не знает pgx.Rows): FetchDue
// обращается к нему через type-assertion в txQuery.
type PgxTx struct{ Tx pgx.Tx }

func (a *PgxTx) Exec(ctx context.Context, sql string, args ...any) error {
	_, err := a.Tx.Exec(ctx, sql, args...)
	return err
}

func (a *PgxTx) QueryRow(ctx context.Context, sql string, args ...any) domain.Row {
	return a.Tx.QueryRow(ctx, sql, args...)
}

func (a *PgxTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return a.Tx.Query(ctx, sql, args...)
}

// TxBeginner открывает транзакции для воркера. PgxPool реализует интерфейс.
type TxBeginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// BeginDomainTx открывает транзакцию из пула и заворачивает её в PgxTx
// (domain.Tx). Откат — на вызывающем; идиома: tx, err := BeginDomainTx(...);
// defer tx.(*PgxTx).Tx.Rollback(ctx).
func BeginDomainTx(ctx context.Context, pool TxBeginner) (domain.Tx, func(), error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, nil, mapErr(err)
	}
	adapter := &PgxTx{Tx: tx}
	rollback := func() { _ = tx.Rollback(ctx) }
	return adapter, rollback, nil
}

// CommitDomainTx коммитит транзакцию, открытую BeginDomainTx. Откат при
// ошибке коммита — на вызывающем (повторный Rollback после commit безопасен).
func CommitDomainTx(ctx context.Context, tx domain.Tx) error {
	adapter, ok := tx.(*PgxTx)
	if !ok {
		return fmt.Errorf("repo: tx is not *PgxTx")
	}
	if err := adapter.Tx.Commit(ctx); err != nil {
		return mapErr(err)
	}
	return nil
}

// txQuery выполняет SELECT через domain.Tx. Сам порт Tx не объявляет Query —
// адаптер пула (pgx.Tx) его имеет; если реализации нет, возвращаем ошибку.
func txQuery(ctx context.Context, tx domain.Tx, sql string, args ...any) (pgx.Rows, error) {
	type rowQuerier interface {
		Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	}
	if q, ok := tx.(rowQuerier); ok {
		return q.Query(ctx, sql, args...)
	}
	return nil, fmt.Errorf("repo: domain.Tx implementation does not support Query")
}

// MarkSent (идемпотентность доставки, спека §7.2): UPDATE проходит только
// если строка ещё pending и держится этим воркером. ok=false — отправку
// подавляем (уже отправлена или лок потерян).
func (r *remindersRepo) MarkSent(ctx context.Context, id int64, workerID string, now time.Time) (bool, error) {
	tag, err := r.pool.Exec(ctx,
		`UPDATE reminders
		 SET status = 'sent', sent_at = $3, locked_by = NULL, locked_at = NULL
		 WHERE id = $1 AND status = 'pending' AND locked_by = $2`,
		id, workerID, now)
	if err != nil {
		return false, mapErr(err)
	}
	return tag.RowsAffected() == 1, nil
}

// MarkSentWithFanout (спека §7.3): в ОДНОЙ транзакции tx — MarkSent родителя
// и вставка dm_dup-детей. Атомарность гарантирует: дети существуют только у
// sent-родителя; гонка проиграна (ok=false) — fan-out подавлен. Дети
// вставляются с ON CONFLICT DO NOTHING: дубликаты при повторе/гонке терпимы
// (unique (deadline_id, target_user_id) WHERE kind='dm_dup').
func (r *remindersRepo) MarkSentWithFanout(ctx context.Context, tx domain.Tx, reminderID int64, workerID string, now time.Time, children []domain.Reminder) (bool, error) {
	var sentID int64
	err := tx.QueryRow(ctx,
		`UPDATE reminders
		 SET status = 'sent', sent_at = $3, locked_by = NULL, locked_at = NULL
		 WHERE id = $1 AND status = 'pending' AND locked_by = $2
		 RETURNING id`,
		reminderID, workerID, now).Scan(&sentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil // не наша строка / уже sent — fan-out подавлен
	}
	if err != nil {
		return false, mapErr(err)
	}
	for i := range children {
		child := &children[i]
		if err := tx.Exec(ctx,
			`INSERT INTO reminders (deadline_id, kind, target_user_id, offset_minutes, fire_at, status)
			 VALUES ($1, 'dm_dup', $2, NULL, $3, 'pending')
			 ON CONFLICT DO NOTHING`,
			child.DeadlineID, child.TargetUserID, child.FireAt); err != nil {
			return false, mapErr(err)
		}
	}
	return true, nil
}

// MarkFailed: attempts+=1, last_error=errText. Если попыток осталось —
// строка возвращается в очередь: status='pending', локи сняты, fire_at
// переиспользуется как время ретрая (отдельной колонки retry_at в схеме
// нет). Иначе status='failed'. Чужой лок не трогаем (locked_by=workerID).
func (r *remindersRepo) MarkFailed(ctx context.Context, id int64, workerID, errText string, retryAt time.Time, maxAttempts int) (bool, error) {
	var (
		status   string
		attempts int
	)
	err := r.pool.QueryRow(ctx,
		`UPDATE reminders
		 SET attempts = attempts + 1,
		     last_error = $3,
		     status = CASE WHEN attempts + 1 >= $4 THEN 'failed' ELSE 'pending' END,
		     fire_at = CASE WHEN attempts + 1 >= $4 THEN fire_at ELSE $5 END,
		     locked_by = NULL,
		     locked_at = NULL
		 WHERE id = $1 AND status = 'pending' AND locked_by = $2
		 RETURNING status, attempts`,
		id, workerID, errText, maxAttempts, retryAt).
		Scan(&status, &attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil // не наша строка / уже не pending — подавляем
	}
	if err != nil {
		return false, mapErr(err)
	}
	return status == string(domain.ReminderStatusFailed), nil
}

// ReleaseStale снимает протухшие локи (спека §7.2, п.1): pending-строки с
// locked_at < olderThan возвращаются в очередь, attempts+=1.
func (r *remindersRepo) ReleaseStale(ctx context.Context, olderThan time.Time) (int64, error) {
	tag, err := r.pool.Exec(ctx,
		`UPDATE reminders
		 SET locked_by = NULL, locked_at = NULL, attempts = attempts + 1
		 WHERE status = 'pending' AND locked_by IS NOT NULL AND locked_at < $1`,
		olderThan)
	if err != nil {
		return 0, mapErr(err)
	}
	return tag.RowsAffected(), nil
}

func (r *remindersRepo) CancelByDeadline(ctx context.Context, deadlineID int64) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE reminders SET status = 'cancelled', locked_by = NULL, locked_at = NULL
		 WHERE deadline_id = $1 AND status = 'pending'`, deadlineID)
	return mapErr(err)
}

// Regenerate одной транзакцией (спека §7.1): pending → cancelled, затем
// новые reminders через upsertReminder (воскрешение cancelled-строк с тем же
// offset + вставка новых; дубликаты терпимы). Возвращает число строк,
// фактически созданных или переведённых обратно в pending.
func (r *remindersRepo) Regenerate(ctx context.Context, deadlineID int64, newReminders []domain.Reminder) (int, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, mapErr(err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`UPDATE reminders SET status = 'cancelled', locked_by = NULL, locked_at = NULL
		 WHERE deadline_id = $1 AND status = 'pending'`, deadlineID); err != nil {
		return 0, mapErr(err)
	}

	inserted := 0
	for i := range newReminders {
		newReminders[i].DeadlineID = deadlineID
		ok, err := upsertReminder(ctx, tx, &newReminders[i])
		if err != nil {
			return 0, err
		}
		if ok {
			inserted++
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, mapErr(err)
	}
	return inserted, nil
}

func collectReminders(rows pgx.Rows) ([]domain.Reminder, error) {
	out := []domain.Reminder{}
	for rows.Next() {
		rem, err := scanReminder(rows)
		if err != nil {
			return nil, mapErr(err)
		}
		out = append(out, *rem)
	}
	return out, rows.Err()
}
