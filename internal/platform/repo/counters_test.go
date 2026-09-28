package repo

import (
	"context"
	"testing"
	"time"

	"github.com/sauron/deadliner/internal/domain"
)

func TestCountersIncAndCheck(t *testing.T) {
	pool := newTestDB(t)
	ctx := context.Background()
	repo := NewCounters(pool)
	userID := insertUser(t, pool, 10001)

	windowStart := time.Now().UTC().Truncate(time.Hour)

	for i := 1; i <= 3; i++ {
		n, err := repo.IncAndCheck(ctx, userID, "create_deadline", windowStart, 3)
		if err != nil {
			t.Fatalf("IncAndCheck %d: %v", i, err)
		}
		if n != i {
			t.Errorf("IncAndCheck #%d = %d, want %d", i, n, i)
		}
	}

	// 4th call exceeds the limit: count keeps incrementing, over-limit visible.
	n, err := repo.IncAndCheck(ctx, userID, "create_deadline", windowStart, 3)
	if err != nil {
		t.Fatalf("IncAndCheck 4th: %v", err)
	}
	if n != 4 {
		t.Errorf("4th call count = %d, want 4", n)
	}
	if n <= 3 {
		t.Errorf("4th call must exceed limit (count=%d limit=3)", n)
	}

	// New window starts fresh.
	nextWindow := windowStart.Add(time.Hour)
	n, err = repo.IncAndCheck(ctx, userID, "create_deadline", nextWindow, 3)
	if err != nil {
		t.Fatalf("IncAndCheck new window: %v", err)
	}
	if n != 1 {
		t.Errorf("new window count = %d, want 1", n)
	}

	// Different action is independent.
	n, err = repo.IncAndCheck(ctx, userID, "create_group", windowStart, 3)
	if err != nil {
		t.Fatalf("IncAndCheck other action: %v", err)
	}
	if n != 1 {
		t.Errorf("other action count = %d, want 1", n)
	}
}

var _ domain.CounterRepo = (*countersRepo)(nil)
