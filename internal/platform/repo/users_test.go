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

// UpdateProfile пишет только first_name: настройки и флаги не трогаются
// (спека §5.2 — PATCH /me с именем).
func TestUsersUpdateProfile(t *testing.T) {
	pool := newTestDB(t)
	ctx := context.Background()
	repo := NewUsers(pool)

	u := &domain.User{TelegramID: 777, Username: "dave", FirstName: "Dave"}
	if err := repo.UpsertByTelegram(ctx, u); err != nil {
		t.Fatalf("UpsertByTelegram: %v", err)
	}
	if err := repo.UpdateSettings(ctx, u.ID, "Asia/Tokyo", true); err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}

	if err := repo.UpdateProfile(ctx, u.ID, "Дэйв"); err != nil {
		t.Fatalf("UpdateProfile: %v", err)
	}
	got, err := repo.GetByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.FirstName != "Дэйв" {
		t.Errorf("first_name = %q, want Дэйв", got.FirstName)
	}
	if got.TZ != "Asia/Tokyo" || !got.DMNotifyDefault {
		t.Errorf("UpdateProfile clobbered settings: %+v", got)
	}
	if got.Username != "dave" {
		t.Errorf("username = %q, want dave (untouched)", got.Username)
	}

	if err := repo.UpdateProfile(ctx, 12345, "Нет"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("UpdateProfile missing = %v, want ErrNotFound", err)
	}
}

// ListSuperadmins — адресаты /report_slug (спека §3.3): только супер-админы,
// забаненные исключены, порядок стабильный (по id).
func TestUsersListSuperadmins(t *testing.T) {
	pool := newTestDB(t)
	ctx := context.Background()
	repo := NewUsers(pool)

	plain := &domain.User{TelegramID: 1001, Username: "plain", FirstName: "P"}
	if err := repo.UpsertByTelegram(ctx, plain); err != nil {
		t.Fatalf("upsert plain: %v", err)
	}
	first := &domain.User{TelegramID: 1002, Username: "root1", FirstName: "R1"}
	if err := repo.UpsertByTelegram(ctx, first); err != nil {
		t.Fatalf("upsert root1: %v", err)
	}
	second := &domain.User{TelegramID: 1003, Username: "root2", FirstName: "R2"}
	if err := repo.UpsertByTelegram(ctx, second); err != nil {
		t.Fatalf("upsert root2: %v", err)
	}
	// Забаненный супер-админ в рассылку не попадает: бан закрывает доступ к
	// инстансу, и тратить на него отправку незачем.
	banned := &domain.User{TelegramID: 1004, Username: "root3", FirstName: "R3"}
	if err := repo.UpsertByTelegram(ctx, banned); err != nil {
		t.Fatalf("upsert root3: %v", err)
	}

	for _, id := range []int64{first.ID, second.ID, banned.ID} {
		if err := repo.SetSuperadmin(ctx, id, true); err != nil {
			t.Fatalf("SetSuperadmin %d: %v", id, err)
		}
	}
	if err := repo.SetBanned(ctx, banned.ID, true); err != nil {
		t.Fatalf("SetBanned: %v", err)
	}

	got, err := repo.ListSuperadmins(ctx)
	if err != nil {
		t.Fatalf("ListSuperadmins: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListSuperadmins = %d rows, want 2 (plain and banned excluded): %+v", len(got), got)
	}
	if got[0].ID != first.ID || got[1].ID != second.ID {
		t.Errorf("order = [%d %d], want ascending user ids [%d %d]",
			got[0].ID, got[1].ID, first.ID, second.ID)
	}
	for _, u := range got {
		if u.TelegramID == 1001 {
			t.Error("a non-superadmin leaked into ListSuperadmins")
		}
		if u.TelegramID == 1004 {
			t.Error("a banned superadmin leaked into ListSuperadmins")
		}
	}
}

var _ domain.UserRepo = (*usersRepo)(nil)
