package repo

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sauron/deadliner/internal/domain"
)

type auditRepo struct {
	pool *pgxpool.Pool
}

func NewAudit(pool *pgxpool.Pool) domain.AuditRepo {
	return &auditRepo{pool: pool}
}

var _ domain.AuditRepo = (*auditRepo)(nil)

func (r *auditRepo) Write(ctx context.Context, e *domain.AuditEntry) error {
	var meta []byte
	if e.Meta != nil {
		b, err := json.Marshal(e.Meta)
		if err != nil {
			return err
		}
		meta = b
	}
	row := r.pool.QueryRow(ctx,
		`INSERT INTO audit_log (actor_user_id, action, target_type, target_id, meta)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING id, created_at`,
		e.ActorUserID, e.Action, e.TargetType, e.TargetID, meta)
	if err := row.Scan(&e.ID, &e.CreatedAt); err != nil {
		return mapErr(err)
	}
	return nil
}
