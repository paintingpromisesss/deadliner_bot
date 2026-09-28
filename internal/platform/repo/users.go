package repo

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sauron/deadliner/internal/domain"
)

const userColumns = `id, telegram_id, username, first_name, tz, dm_notify_default,
	is_superadmin, is_banned, bot_blocked, created_at`

type usersRepo struct {
	pool *pgxpool.Pool
}

func NewUsers(pool *pgxpool.Pool) domain.UserRepo {
	return &usersRepo{pool: pool}
}

var _ domain.UserRepo = (*usersRepo)(nil)

func scanUser(row pgx.Row) (*domain.User, error) {
	var (
		u        domain.User
		username *string
		first    *string
	)
	err := row.Scan(
		&u.ID, &u.TelegramID, &username, &first, &u.TZ,
		&u.DMNotifyDefault, &u.IsSuperadmin, &u.IsBanned, &u.BotBlocked, &u.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	if username != nil {
		u.Username = *username
	}
	if first != nil {
		u.FirstName = *first
	}
	return &u, nil
}

func (r *usersRepo) GetByTelegramID(ctx context.Context, telegramID int64) (*domain.User, error) {
	u, err := scanUser(r.pool.QueryRow(ctx,
		`SELECT `+userColumns+` FROM users WHERE telegram_id = $1`, telegramID))
	if err != nil {
		return nil, mapNotFoundErr(err, fmt.Sprintf("user telegram_id=%d", telegramID))
	}
	return u, nil
}

func (r *usersRepo) GetByID(ctx context.Context, id int64) (*domain.User, error) {
	u, err := scanUser(r.pool.QueryRow(ctx,
		`SELECT `+userColumns+` FROM users WHERE id = $1`, id))
	if err != nil {
		return nil, mapNotFoundErr(err, fmt.Sprintf("user id=%d", id))
	}
	return u, nil
}

// UpsertByTelegram inserts the user or refreshes username/first_name on an
// existing telegram_id; settings and flags are preserved. u.ID and u.CreatedAt
// are filled from the returned row.
func (r *usersRepo) UpsertByTelegram(ctx context.Context, u *domain.User) error {
	row := r.pool.QueryRow(ctx,
		`INSERT INTO users (telegram_id, username, first_name)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (telegram_id) DO UPDATE
		 SET username = EXCLUDED.username, first_name = EXCLUDED.first_name
		 RETURNING id, created_at`,
		u.TelegramID, u.Username, u.FirstName)
	if err := row.Scan(&u.ID, &u.CreatedAt); err != nil {
		return mapErr(err)
	}
	return nil
}

func (r *usersRepo) UpdateSettings(ctx context.Context, id int64, tz string, dmNotifyDefault bool) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE users SET tz = $2, dm_notify_default = $3 WHERE id = $1`,
		id, tz, dmNotifyDefault)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: user id=%d", domain.ErrNotFound, id)
	}
	return nil
}

func (r *usersRepo) SetBanned(ctx context.Context, id int64, banned bool) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE users SET is_banned = $2 WHERE id = $1`, id, banned)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: user id=%d", domain.ErrNotFound, id)
	}
	return nil
}

func (r *usersRepo) SetSuperadmin(ctx context.Context, id int64, superadmin bool) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE users SET is_superadmin = $2 WHERE id = $1`, id, superadmin)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: user id=%d", domain.ErrNotFound, id)
	}
	return nil
}

func (r *usersRepo) MarkBotBlocked(ctx context.Context, telegramID int64, blocked bool) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE users SET bot_blocked = $2 WHERE telegram_id = $1`, telegramID, blocked)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: user telegram_id=%d", domain.ErrNotFound, telegramID)
	}
	return nil
}
