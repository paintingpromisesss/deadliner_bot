package repo

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sauron/deadliner/internal/domain"
)

type sessionsRepo struct {
	pool *pgxpool.Pool
}

func NewSessions(pool *pgxpool.Pool) domain.SessionRepo {
	return &sessionsRepo{pool: pool}
}

var _ domain.SessionRepo = (*sessionsRepo)(nil)

func (r *sessionsRepo) Create(ctx context.Context, s *domain.Session) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO sessions (token_hash, user_id, expires_at, created_at, last_seen)
		 VALUES ($1, $2, $3, $4, $5)`,
		s.TokenHash, s.UserID, s.ExpiresAt, s.CreatedAt, s.LastSeen)
	if err != nil {
		return mapErr(err)
	}
	return nil
}

// GetActive возвращает сессию, если она существует и не истекла на момент now.
func (r *sessionsRepo) GetActive(ctx context.Context, tokenHash string, now time.Time) (*domain.Session, error) {
	var s domain.Session
	err := r.pool.QueryRow(ctx,
		`SELECT token_hash, user_id, expires_at, created_at, last_seen
		 FROM sessions WHERE token_hash = $1`, tokenHash).
		Scan(&s.TokenHash, &s.UserID, &s.ExpiresAt, &s.CreatedAt, &s.LastSeen)
	if err != nil {
		return nil, mapNotFoundErr(err, fmt.Sprintf("session token_hash=%.8s…", tokenHash))
	}
	if !s.ExpiresAt.After(now) {
		return nil, fmt.Errorf("%w: session expired", domain.ErrNotFound)
	}
	return &s, nil
}

func (r *sessionsRepo) Touch(ctx context.Context, tokenHash string, lastSeen, expiresAt time.Time) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE sessions SET last_seen = $2, expires_at = $3 WHERE token_hash = $1`,
		tokenHash, lastSeen, expiresAt)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: session token_hash=%.8s…", domain.ErrNotFound, tokenHash)
	}
	return nil
}

func (r *sessionsRepo) Revoke(ctx context.Context, tokenHash string) error {
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM sessions WHERE token_hash = $1`, tokenHash)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: session token_hash=%.8s…", domain.ErrNotFound, tokenHash)
	}
	return nil
}

func (r *sessionsRepo) RevokeAllForUser(ctx context.Context, userID int64) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, userID)
	if err != nil {
		return mapErr(err)
	}
	return nil
}
