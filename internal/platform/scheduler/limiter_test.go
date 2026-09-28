package scheduler

import (
	"context"
	"testing"
	"time"
)

// Пенальти-бухгалтерия детерминированно: фейковые часы/сон без ожидания.
func TestLimiterPenaltyBookkeeping(t *testing.T) {
	base := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	now := base
	var slept []time.Duration
	sleep := func(ctx context.Context, d time.Duration) error {
		slept = append(slept, d)
		now = now.Add(d) // сон продвигает фейковые часы
		return nil
	}
	l := NewLimiter(25, 18).WithClockAndSleep(func() time.Time { return now }, sleep)

	if got := l.PenaltyUntil(1); !got.IsZero() {
		t.Errorf("empty PenaltyUntil = %v, want zero", got)
	}
	// Пенальти до 12:00:07.
	l.Penalize(1, now.Add(7*time.Second))
	if got := l.PenaltyUntil(1); !got.Equal(now.Add(7 * time.Second)) {
		t.Errorf("PenaltyUntil = %v, want %v", got, now.Add(7*time.Second))
	}
	// WaitChat спит 7с (пенальти активно).
	if err := l.WaitChat(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if len(slept) != 1 || slept[0] != 7*time.Second {
		t.Errorf("slept = %v, want [7s]", slept)
	}
	// Пенальти истёк (тот же now) → второй WaitChat не спит, пенальти снят.
	if err := l.WaitChat(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if len(slept) != 1 {
		t.Errorf("slept twice: %v", slept)
	}
	if got := l.PenaltyUntil(1); !got.IsZero() {
		t.Errorf("penalty not cleared: %v", got)
	}
}

// Penalize не ослабляет более длинное действующее пенальти.
func TestLimiterPenaltyKeepsMax(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	l := NewLimiter(25, 18).WithClockAndSleep(func() time.Time { return now }, func(context.Context, time.Duration) error { return nil })
	l.Penalize(2, now.Add(time.Minute))
	l.Penalize(2, now.Add(10*time.Second))
	if got := l.PenaltyUntil(2); !got.Equal(now.Add(time.Minute)) {
		t.Errorf("PenaltyUntil = %v, want the later one", got)
	}
}

// Другой чат не затронут пенальти соседа.
func TestLimiterPenaltyIsPerChat(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	var slept int
	l := NewLimiter(25, 18).WithClockAndSleep(func() time.Time { return now }, func(context.Context, time.Duration) error {
		slept++
		return nil
	})
	l.Penalize(3, now.Add(time.Hour))
	if err := l.WaitChat(context.Background(), 4); err != nil {
		t.Fatal(err)
	}
	if slept != 0 {
		t.Errorf("chat 4 waited for chat 3 penalty: slept=%d", slept)
	}
}
