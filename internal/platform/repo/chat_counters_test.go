package repo

import (
	"context"
	"testing"
	"time"

	"github.com/sauron/deadliner/internal/domain"
)

// IncAndCheck инкрементирует счётчик чата и возвращает значение ПОСЛЕ
// инкремента; окна и actions независимы (спека §3.3: 3 claim-кода в час на чат).
func TestChatCountersIncAndCheck(t *testing.T) {
	pool := newTestDB(t)
	ctx := context.Background()
	repo := NewChatCounters(pool)

	window := time.Now().UTC().Truncate(time.Hour)
	chatID := int64(-100500)

	// Три инкремента — 1, 2, 3 (политика count > limit остаётся в app-слое).
	for want := 1; want <= 3; want++ {
		got, err := repo.IncAndCheck(ctx, chatID, "claim_chat_hour", window, 3)
		if err != nil {
			t.Fatalf("IncAndCheck #%d: %v", want, err)
		}
		if got != want {
			t.Errorf("count #%d = %d, want %d", want, got, want)
		}
	}
	// Четвёртый — 4 > limit: вызывающий обязан отклонить.
	got, err := repo.IncAndCheck(ctx, chatID, "claim_chat_hour", window, 3)
	if err != nil {
		t.Fatalf("IncAndCheck #4: %v", err)
	}
	if got != 4 {
		t.Errorf("count #4 = %d, want 4 (limit check is the caller's job)", got)
	}

	// Другой чат — свой счётчик (это и есть per-chat семантика).
	other, err := repo.IncAndCheck(ctx, -100600, "claim_chat_hour", window, 3)
	if err != nil {
		t.Fatalf("IncAndCheck(other chat): %v", err)
	}
	if other != 1 {
		t.Errorf("other chat count = %d, want 1", other)
	}

	// Другое окно — счётчик начинается заново.
	next, err := repo.IncAndCheck(ctx, chatID, "claim_chat_hour", window.Add(time.Hour), 3)
	if err != nil {
		t.Fatalf("IncAndCheck(next window): %v", err)
	}
	if next != 1 {
		t.Errorf("next window count = %d, want 1", next)
	}

	// Другое действие того же чата — независимый счётчик.
	otherAction, err := repo.IncAndCheck(ctx, chatID, "other_action", window, 3)
	if err != nil {
		t.Fatalf("IncAndCheck(other action): %v", err)
	}
	if otherAction != 1 {
		t.Errorf("other action count = %d, want 1", otherAction)
	}

	// Строка живёт ровно в (chat_id, action, window_start) — проверяем составной
	// ключ явным SELECT с column list (никакого SELECT *).
	var stored int
	if err := pool.QueryRow(ctx,
		`SELECT count FROM chat_action_counters
		 WHERE chat_id = $1 AND action = $2 AND window_start = $3`,
		chatID, "claim_chat_hour", window).Scan(&stored); err != nil {
		t.Fatalf("select counter: %v", err)
	}
	if stored != 4 {
		t.Errorf("stored count = %d, want 4", stored)
	}
}

var _ domain.ChatCounterRepo = (*chatCountersRepo)(nil)
