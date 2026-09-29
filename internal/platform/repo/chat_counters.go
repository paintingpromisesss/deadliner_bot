package repo

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sauron/deadliner/internal/domain"
)

type chatCountersRepo struct {
	pool *pgxpool.Pool
}

func NewChatCounters(pool *pgxpool.Pool) domain.ChatCounterRepo {
	return &chatCountersRepo{pool: pool}
}

var _ domain.ChatCounterRepo = (*chatCountersRepo)(nil)

// IncAndCheck upserts the (chat, action, window) counter and returns the count
// after incrementing, mirroring CounterRepo: the caller compares it against the
// limit (count > limit = exhausted). Per-chat limits (spec §3.3: 3 claim codes
// per hour per chat) live here because user_action_counters is keyed by users.id.
func (r *chatCountersRepo) IncAndCheck(ctx context.Context, chatID int64, action string, windowStart time.Time, limit int) (int, error) {
	var count int
	err := r.pool.QueryRow(ctx,
		`INSERT INTO chat_action_counters (chat_id, action, window_start, count)
		 VALUES ($1, $2, $3, 1)
		 ON CONFLICT (chat_id, action, window_start)
		 DO UPDATE SET count = chat_action_counters.count + 1
		 RETURNING count`,
		chatID, action, windowStart).Scan(&count)
	if err != nil {
		return 0, mapErr(err)
	}
	return count, nil
}
