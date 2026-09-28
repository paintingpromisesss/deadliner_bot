package repo

import (
	"context"
	"errors"
	"testing"

	"github.com/sauron/deadliner/internal/domain"
)

func TestUsersUpsertIdempotent(t *testing.T) {
	pool := newTestDB(t)
	ctx := context.Background()
	repo := NewUsers(pool)

	u := &domain.User{TelegramID: 777, Username: "alice", FirstName: "Alice"}
	if err := repo.UpsertByTelegram(ctx, u); err != nil {
		t.Fatalf("UpsertByTelegram: %v", err)
	}
	if u.ID == 0 {
		t.Fatal("UpsertByTelegram did not set ID")
	}
	firstID := u.ID

	// Second upsert with updated profile data: same row, same ID.
	u2 := &domain.User{TelegramID: 777, Username: "alice2", FirstName: "Alice B"}
	if err := repo.UpsertByTelegram(ctx, u2); err != nil {
		t.Fatalf("UpsertByTelegram second: %v", err)
	}
	if u2.ID != firstID {
		t.Fatalf("upsert created second row: id %d != %d", u2.ID, firstID)
	}

	got, err := repo.GetByTelegramID(ctx, 777)
	if err != nil {
		t.Fatalf("GetByTelegramID: %v", err)
	}
	if got.Username != "alice2" || got.FirstName != "Alice B" {
		t.Errorf("username=%q first_name=%q, want alice2 / Alice B", got.Username, got.FirstName)
	}
	// Defaults from schema must survive the upsert.
	if got.TZ != "Europe/Moscow" || got.DMNotifyDefault || got.IsSuperadmin || got.IsBanned || got.BotBlocked {
		t.Errorf("unexpected defaults: %+v", got)
	}

	gotByID, err := repo.GetByID(ctx, firstID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if gotByID.TelegramID != 777 {
		t.Errorf("telegram_id=%d, want 777", gotByID.TelegramID)
	}

	if _, err := repo.GetByTelegramID(ctx, 12345); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("GetByTelegramID missing = %v, want ErrNotFound", err)
	}
	if _, err := repo.GetByID(ctx, 12345); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("GetByID missing = %v, want ErrNotFound", err)
	}
}

func TestUsersUpsertKeepsSettings(t *testing.T) {
	pool := newTestDB(t)
	ctx := context.Background()
	repo := NewUsers(pool)

	u := &domain.User{TelegramID: 888, Username: "bob", FirstName: "Bob"}
	if err := repo.UpsertByTelegram(ctx, u); err != nil {
		t.Fatalf("UpsertByTelegram: %v", err)
	}
	if err := repo.UpdateSettings(ctx, u.ID, "Asia/Tokyo", true); err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}
	if err := repo.SetSuperadmin(ctx, u.ID, true); err != nil {
		t.Fatalf("SetSuperadmin: %v", err)
	}

	// Re-upsert (user renames) must not clobber tz / dm_notify_default / flags.
	u2 := &domain.User{TelegramID: 888, Username: "bob_new", FirstName: "Bobby"}
	if err := repo.UpsertByTelegram(ctx, u2); err != nil {
		t.Fatalf("UpsertByTelegram rename: %v", err)
	}
	got, err := repo.GetByTelegramID(ctx, 888)
	if err != nil {
		t.Fatalf("GetByTelegramID: %v", err)
	}
	if got.Username != "bob_new" {
		t.Errorf("username=%q, want bob_new", got.Username)
	}
	if got.TZ != "Asia/Tokyo" || !got.DMNotifyDefault || !got.IsSuperadmin {
		t.Errorf("upsert clobbered settings: %+v", got)
	}
}

func TestUsersFlags(t *testing.T) {
	pool := newTestDB(t)
	ctx := context.Background()
	repo := NewUsers(pool)

	u := &domain.User{TelegramID: 999, Username: "carol", FirstName: "Carol"}
	if err := repo.UpsertByTelegram(ctx, u); err != nil {
		t.Fatalf("UpsertByTelegram: %v", err)
	}

	if err := repo.SetBanned(ctx, u.ID, true); err != nil {
		t.Fatalf("SetBanned: %v", err)
	}
	if err := repo.MarkBotBlocked(ctx, 999, true); err != nil {
		t.Fatalf("MarkBotBlocked: %v", err)
	}
	got, err := repo.GetByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if !got.IsBanned || !got.BotBlocked {
		t.Errorf("flags not set: banned=%v bot_blocked=%v", got.IsBanned, got.BotBlocked)
	}

	if err := repo.SetBanned(ctx, u.ID, false); err != nil {
		t.Fatalf("SetBanned false: %v", err)
	}
	if err := repo.MarkBotBlocked(ctx, 999, false); err != nil {
		t.Fatalf("MarkBotBlocked false: %v", err)
	}
	got, err = repo.GetByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.IsBanned || got.BotBlocked {
		t.Errorf("flags not cleared: %+v", got)
	}

	if err := repo.SetBanned(ctx, 12345, true); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("SetBanned missing = %v, want ErrNotFound", err)
	}
	if err := repo.MarkBotBlocked(ctx, 12345, true); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("MarkBotBlocked missing = %v, want ErrNotFound", err)
	}
}

var _ domain.UserRepo = (*usersRepo)(nil)
