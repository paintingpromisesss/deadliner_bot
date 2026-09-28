package repo

import (
	"context"
	"errors"
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

// ListByGroupDetailed — участники с username/first_name одним JOIN (без N+1).
func (r *membershipsRepo) ListByGroupDetailed(ctx context.Context, groupID int64) ([]domain.MembershipDetail, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT m.group_id, m.user_id, m.role, m.dm_notify, m.joined_at,
		        coalesce(u.username, ''), coalesce(u.first_name, '')
		 FROM group_memberships m
		 JOIN users u ON u.id = m.user_id
		 WHERE m.group_id = $1 ORDER BY m.joined_at, m.user_id`, groupID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := []domain.MembershipDetail{}
	for rows.Next() {
		var (
			d    domain.MembershipDetail
			role string
		)
		err := rows.Scan(&d.GroupID, &d.UserID, &role, &d.DMNotify, &d.JoinedAt,
			&d.Username, &d.FirstName)
		if err != nil {
			return nil, mapErr(err)
		}
		d.Role = domain.Role(role)
		out = append(out, d)
	}
	return out, rows.Err()
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

// ListDMTargets — users.id участников с эффективным dm_notify
// (COALESCE(memberships.dm_notify, users.dm_notify_default)), без bot_blocked:
// цели dm_dup fan-out (спека §7.3).
func (r *membershipsRepo) ListDMTargets(ctx context.Context, groupID int64) ([]int64, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT m.user_id
		 FROM group_memberships m
		 JOIN users u ON u.id = m.user_id
		 WHERE m.group_id = $1
		   AND COALESCE(m.dm_notify, u.dm_notify_default)
		   AND NOT u.bot_blocked
		   AND NOT u.is_banned
		 ORDER BY m.user_id`, groupID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, mapErr(err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
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

// DemoteIfNotLastAdmin — понижение админа с защитой «последнего админа» в БД:
// транзакция «чтение роли → UPDATE → count админов», и если оставшийся
// счётчик равен нулю — откат. App-слой check-then-act был небезопасен при гонках.
func (r *membershipsRepo) DemoteIfNotLastAdmin(ctx context.Context, groupID, userID int64) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return mapErr(err)
	}
	defer tx.Rollback(ctx)

	// Блокировка строки группы сериализует конкурентные demote/kick/leave
	// одной группы — иначе две транзакции могут одновременно понизить
	// двух разных админов и оставить группу без админа.
	if _, err := tx.Exec(ctx, `SELECT id FROM groups WHERE id = $1 FOR UPDATE`, groupID); err != nil {
		return mapErr(err)
	}

	var role string
	err = tx.QueryRow(ctx,
		`SELECT role FROM group_memberships WHERE group_id = $1 AND user_id = $2`,
		groupID, userID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: membership group=%d user=%d", domain.ErrNotFound, groupID, userID)
	}
	if err != nil {
		return mapErr(err)
	}
	if role != string(domain.RoleAdmin) {
		return fmt.Errorf("%w: cannot demote user_id=%d: not an admin",
			domain.ErrValidation, userID)
	}

	if _, err := tx.Exec(ctx,
		`UPDATE group_memberships SET role = 'member'
		 WHERE group_id = $1 AND user_id = $2`, groupID, userID); err != nil {
		return mapErr(err)
	}

	var admins int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM group_memberships WHERE group_id = $1 AND role = 'admin'`,
		groupID).Scan(&admins); err != nil {
		return mapErr(err)
	}
	if admins == 0 {
		return fmt.Errorf("%w: group=%d would lose its last admin", domain.ErrConflict, groupID)
	}
	return mapErr(tx.Commit(ctx))
}

// RemoveIfNotLastAdmin — удаление участника с той же защитой: кик последнего
// админа (даже superadmin'ом) откатывается — группу нельзя «осиротить».
func (r *membershipsRepo) RemoveIfNotLastAdmin(ctx context.Context, groupID, userID int64) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return mapErr(err)
	}
	defer tx.Rollback(ctx)

	// См. DemoteIfNotLastAdmin: блокировка строки группы против гонки.
	if _, err := tx.Exec(ctx, `SELECT id FROM groups WHERE id = $1 FOR UPDATE`, groupID); err != nil {
		return mapErr(err)
	}

	var role string
	err = tx.QueryRow(ctx,
		`SELECT role FROM group_memberships WHERE group_id = $1 AND user_id = $2`,
		groupID, userID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: membership group=%d user=%d", domain.ErrNotFound, groupID, userID)
	}
	if err != nil {
		return mapErr(err)
	}

	if _, err := tx.Exec(ctx,
		`DELETE FROM group_memberships WHERE group_id = $1 AND user_id = $2`,
		groupID, userID); err != nil {
		return mapErr(err)
	}

	if role == string(domain.RoleAdmin) {
		var admins int
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM group_memberships WHERE group_id = $1 AND role = 'admin'`,
			groupID).Scan(&admins); err != nil {
			return mapErr(err)
		}
		if admins == 0 {
			return fmt.Errorf("%w: group=%d would lose its last admin", domain.ErrConflict, groupID)
		}
	}
	return mapErr(tx.Commit(ctx))
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
