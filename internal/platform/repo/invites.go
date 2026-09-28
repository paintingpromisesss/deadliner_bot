package repo

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sauron/deadliner/internal/domain"
)

const inviteColumns = `id, group_id, code, role, max_uses, used_count, created_by, expires_at, revoked_at`

type invitesRepo struct {
	pool *pgxpool.Pool
}

func NewInvites(pool *pgxpool.Pool) domain.InviteRepo {
	return &invitesRepo{pool: pool}
}

var _ domain.InviteRepo = (*invitesRepo)(nil)

func scanInvite(row pgx.Row) (*domain.Invite, error) {
	var (
		inv     domain.Invite
		role    string
		revoked *time.Time
	)
	err := row.Scan(
		&inv.ID, &inv.GroupID, &inv.Code, &role, &inv.MaxUses,
		&inv.UsedCount, &inv.CreatedBy, &inv.ExpiresAt, &revoked,
	)
	if err != nil {
		return nil, err
	}
	inv.Role = domain.Role(role)
	inv.RevokedAt = revoked
	return &inv, nil
}

func (r *invitesRepo) Create(ctx context.Context, inv *domain.Invite) error {
	row := r.pool.QueryRow(ctx,
		`INSERT INTO invites (group_id, code, role, max_uses, used_count, created_by, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 RETURNING id`,
		inv.GroupID, inv.Code, string(inv.Role), inv.MaxUses, inv.UsedCount,
		inv.CreatedBy, inv.ExpiresAt)
	if err := row.Scan(&inv.ID); err != nil {
		return mapErr(err)
	}
	return nil
}

func (r *invitesRepo) GetByCode(ctx context.Context, code string) (*domain.Invite, error) {
	inv, err := scanInvite(r.pool.QueryRow(ctx,
		`SELECT `+inviteColumns+` FROM invites WHERE code = $1`, code))
	if err != nil {
		return nil, mapNotFoundErr(err, "invite")
	}
	return inv, nil
}

// IncrementUsed атомарно расходует одно использование: UPDATE с условием
// (не отозван, не истёк, лимит не исчерпан; max_uses < 0 = без лимита).
// Нулевое число строк → domain.ErrConflict — гонка параллельных redeem
// исключена на уровне БД (check-then-act в app-слое был небезопасен).
func (r *invitesRepo) IncrementUsed(ctx context.Context, id int64) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE invites SET used_count = used_count + 1
		 WHERE id = $1 AND revoked_at IS NULL AND expires_at > now()
		   AND (max_uses < 0 OR used_count < max_uses)`, id)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: invite id=%d unusable (revoked, expired or exhausted)",
			domain.ErrConflict, id)
	}
	return nil
}

func (r *invitesRepo) Revoke(ctx context.Context, groupID int64, code string) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE invites SET revoked_at = now()
		 WHERE group_id = $1 AND code = $2 AND revoked_at IS NULL`, groupID, code)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: invite group=%d", domain.ErrNotFound, groupID)
	}
	return nil
}

func (r *invitesRepo) ListByGroup(ctx context.Context, groupID int64) ([]domain.Invite, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+inviteColumns+` FROM invites
		 WHERE group_id = $1 ORDER BY id`, groupID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := []domain.Invite{}
	for rows.Next() {
		inv, err := scanInvite(rows)
		if err != nil {
			return nil, mapErr(err)
		}
		out = append(out, *inv)
	}
	return out, rows.Err()
}
