package repo

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sauron/deadliner/internal/domain"
)

type countersRepo struct {
	pool *pgxpool.Pool
}

func NewCounters(pool *pgxpool.Pool) domain.CounterRepo {
	return &countersRepo{pool: pool}
}

var _ domain.CounterRepo = (*countersRepo)(nil)

// IncAndCheck upserts the (user, action, window) counter row and returns the
// count after incrementing. The caller compares it against limit: count > limit
// means the rate limit is exhausted (per Task 3 ruling — policy stays in the
// app layer).
func (r *countersRepo) IncAndCheck(ctx context.Context, userID int64, action string, windowStart time.Time, limit int) (int, error) {
	var count int
	err := r.pool.QueryRow(ctx,
		`INSERT INTO user_action_counters (user_id, action, window_start, count)
		 VALUES ($1, $2, $3, 1)
		 ON CONFLICT (user_id, action, window_start)
		 DO UPDATE SET count = user_action_counters.count + 1
		 RETURNING count`,
		userID, action, windowStart).Scan(&count)
	if err != nil {
		return 0, mapErr(err)
	}
	return count, nil
}
