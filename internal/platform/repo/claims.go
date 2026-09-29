package repo

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sauron/deadliner/internal/domain"
)

const claimColumns = `id, group_id, code_hash, chat_id, message_id, created_by, expires_at, used_at`

type claimsRepo struct {
	pool *pgxpool.Pool
}

func NewClaims(pool *pgxpool.Pool) domain.ClaimRepo {
	return &claimsRepo{pool: pool}
}

var _ domain.ClaimRepo = (*claimsRepo)(nil)

func scanClaim(row pgx.Row) (*domain.ClaimCode, error) {
	var c domain.ClaimCode
	err := row.Scan(&c.ID, &c.GroupID, &c.CodeHash, &c.ChatID, &c.MessageID,
		&c.CreatedBy, &c.ExpiresAt, &c.UsedAt)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// Create inserts the claim code; message_id is the ID of the message the code
// was posted with, so the row is written only after a successful send.
func (r *claimsRepo) Create(ctx context.Context, c *domain.ClaimCode) error {
	row := r.pool.QueryRow(ctx,
		`INSERT INTO claim_codes (group_id, code_hash, chat_id, message_id, created_by, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING id`,
		c.GroupID, c.CodeHash, c.ChatID, c.MessageID, c.CreatedBy, c.ExpiresAt)
	if err := row.Scan(&c.ID); err != nil {
		return mapErr(err)
	}
	return nil
}

// GetActiveByGroup returns the newest live code — used_at IS NULL AND
// expires_at > now — of the group. An expired code is indistinguishable from
// a missing one (ErrNotFound).
func (r *claimsRepo) GetActiveByGroup(ctx context.Context, groupID int64, now time.Time) (*domain.ClaimCode, error) {
	c, err := scanClaim(r.pool.QueryRow(ctx,
		`SELECT `+claimColumns+` FROM claim_codes
		 WHERE group_id = $1 AND used_at IS NULL AND expires_at > $2
		 ORDER BY id DESC
		 LIMIT 1`, groupID, now))
	if err != nil {
		return nil, mapNotFoundErr(err, fmt.Sprintf("active claim code group_id=%d", groupID))
	}
	return c, nil
}

// ListActiveByGroup returns every live code of the group (usually zero or one;
// StartClaim revokes the previous one, but the revoke is not atomic with the
// insert, so a race may leave two for a moment).
func (r *claimsRepo) ListActiveByGroup(ctx context.Context, groupID int64, now time.Time) ([]domain.ClaimCode, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+claimColumns+` FROM claim_codes
		 WHERE group_id = $1 AND used_at IS NULL AND expires_at > $2
		 ORDER BY id`, groupID, now)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := []domain.ClaimCode{}
	for rows.Next() {
		c, err := scanClaim(rows)
		if err != nil {
			return nil, mapErr(err)
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// MarkUsed burns the code with a conditional UPDATE: a second confirm (or a
// concurrent one) finds used_at already set and gets ErrNotFound, so exactly
// one call wins the race.
func (r *claimsRepo) MarkUsed(ctx context.Context, id int64, now time.Time) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE claim_codes SET used_at = $2 WHERE id = $1 AND used_at IS NULL`, id, now)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: claim code id=%d already used or revoked", domain.ErrNotFound, id)
	}
	return nil
}

// RevokeActiveByGroup burns every live code of the group. Rows affected is
// deliberately ignored by the caller: revoking with nothing to revoke is a
// no-op success (the API answers 204 either way).
func (r *claimsRepo) RevokeActiveByGroup(ctx context.Context, groupID int64, now time.Time) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE claim_codes SET used_at = $2
		 WHERE group_id = $1 AND used_at IS NULL`,
		groupID, now)
	return mapErr(err)
}
