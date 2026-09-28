package repo

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sauron/deadliner/internal/domain"
)

const membershipColumns = `group_id, user_id, role, dm_notify, joined_at`

type membershipsRepo struct {
	pool *pgxpool.Pool
}

func NewMemberships(pool *pgxpool.Pool) domain.MembershipRepo {
	return &membershipsRepo{pool: pool}
}

var _ domain.MembershipRepo = (*membershipsRepo)(nil)

func scanMembership(row pgx.Row) (*domain.Membership, error) {
	var (
		m    domain.Membership
		role string
	)
	err := row.Scan(&m.GroupID, &m.UserID, &role, &m.DMNotify, &m.JoinedAt)
	if err != nil {
		return nil, err
	}
	m.Role = domain.Role(role)
	return &m, nil
}

// Upsert adds the membership; on conflict the existing role and dm_notify are
// kept (role changes go through SetRole explicitly).
func (r *membershipsRepo) Upsert(ctx context.Context, m *domain.Membership) error {
	role := m.Role
	if role == "" {
		role = domain.RoleMember
	}
	_, err := r.pool.Exec(ctx,
		`INSERT INTO group_memberships (group_id, user_id, role, dm_notify)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (group_id, user_id) DO NOTHING`,
		m.GroupID, m.UserID, string(role), m.DMNotify)
	if err != nil {
		return mapErr(err)
	}
	return nil
}

func (r *membershipsRepo) Get(ctx context.Context, groupID, userID int64) (*domain.Membership, error) {
	m, err := scanMembership(r.pool.QueryRow(ctx,
		`SELECT `+membershipColumns+` FROM group_memberships
		 WHERE group_id = $1 AND user_id = $2`, groupID, userID))
	if err != nil {
		return nil, mapNotFoundErr(err, fmt.Sprintf("membership group=%d user=%d", groupID, userID))
	}
	return m, nil
}

func (r *membershipsRepo) ListByGroup(ctx context.Context, groupID int64) ([]domain.Membership, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+membershipColumns+` FROM group_memberships
		 WHERE group_id = $1 ORDER BY joined_at, user_id`, groupID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	return collectMemberships(rows)
}

func (r *membershipsRepo) ListByUser(ctx context.Context, userID int64) ([]domain.Membership, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+membershipColumns+` FROM group_memberships
		 WHERE user_id = $1 ORDER BY joined_at, group_id`, userID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	return collectMemberships(rows)
}

func (r *membershipsRepo) SetRole(ctx context.Context, groupID, userID int64, role domain.Role) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE group_memberships SET role = $3 WHERE group_id = $1 AND user_id = $2`,
		groupID, userID, string(role))
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: membership group=%d user=%d", domain.ErrNotFound, groupID, userID)
	}
	return nil
}

func (r *membershipsRepo) SetDMNotify(ctx context.Context, groupID, userID int64, dmNotify *bool) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE group_memberships SET dm_notify = $3 WHERE group_id = $1 AND user_id = $2`,
		groupID, userID, dmNotify)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: membership group=%d user=%d", domain.ErrNotFound, groupID, userID)
	}
	return nil
}

func (r *membershipsRepo) Delete(ctx context.Context, groupID, userID int64) error {
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM group_memberships WHERE group_id = $1 AND user_id = $2`,
		groupID, userID)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: membership group=%d user=%d", domain.ErrNotFound, groupID, userID)
	}
	return nil
}

func (r *membershipsRepo) CountAdmins(ctx context.Context, groupID int64) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM group_memberships WHERE group_id = $1 AND role = 'admin'`,
		groupID).Scan(&n)
	if err != nil {
		return 0, mapErr(err)
	}
	return n, nil
}

func collectMemberships(rows pgx.Rows) ([]domain.Membership, error) {
	out := []domain.Membership{}
	for rows.Next() {
		m, err := scanMembership(rows)
		if err != nil {
			return nil, mapErr(err)
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}
