package repo

import (
	"context"
	"testing"
	"time"

	"github.com/sauron/deadliner/internal/domain"
)

// Stats собирает счётчики инстанса одним запросом: soft-deleted группы и
// дедлайны не считаются, напоминания — по статусу, сессии — активные на now.
func TestMaintenanceStats(t *testing.T) {
	pool := newTestDB(t)
	ctx := context.Background()
	repo := NewMaintenance(pool)
	now := time.Now().UTC()

	owner := insertUser(t, pool, 7001)
	groups := NewGroups(pool)
	deadlines := NewDeadlines(pool)

	pending := newGroup("ИКБО-91-21", "pending", owner, nil)
	if err := groups.Create(ctx, pending); err != nil {
		t.Fatalf("create pending group: %v", err)
	}
	active := newGroup("ИКБО-92-21", "active", owner, nil)
	if err := groups.Create(ctx, active); err != nil {
		t.Fatalf("create active group: %v", err)
	}
	if err := groups.SetStatus(ctx, active.ID, domain.GroupStatusActive); err != nil {
		t.Fatalf("activate: %v", err)
	}
	deleted := newGroup("ИКБО-93-21", "deleted", owner, nil)
	if err := groups.Create(ctx, deleted); err != nil {
		t.Fatalf("create deleted group: %v", err)
	}
	if err := groups.SoftDelete(ctx, deleted.ID); err != nil {
		t.Fatalf("soft delete: %v", err)
	}

	d := &domain.Deadline{
		GroupID: &active.ID, Title: "Дедлайн", DueAt: now.Add(48 * time.Hour),
		TZ: "Europe/Moscow", CreatedBy: owner, Status: domain.DeadlineStatusActive,
	}
	mins := 1440
	if err := deadlines.Create(ctx, d, []domain.Reminder{{
		Kind: domain.KindPreset, OffsetMinutes: &mins,
		FireAt: now.Add(24 * time.Hour), Status: domain.ReminderStatusPending,
	}}); err != nil {
		t.Fatalf("create deadline: %v", err)
	}
	// Второй дедлайн — со сгоревшим напоминанием и soft-deleted дедлайном.
	gone := &domain.Deadline{
		GroupID: &active.ID, Title: "Просроченный", DueAt: now.Add(-time.Hour),
		TZ: "Europe/Moscow", CreatedBy: owner, Status: domain.DeadlineStatusActive,
	}
	if err := deadlines.Create(ctx, gone, []domain.Reminder{{
		Kind: domain.KindPreset, OffsetMinutes: &mins,
		FireAt: now.Add(-2 * time.Hour), Status: domain.ReminderStatusFailed,
	}}); err != nil {
		t.Fatalf("create failed deadline: %v", err)
	}
	if err := deadlines.SoftDelete(ctx, gone.ID); err != nil {
		t.Fatalf("soft delete deadline: %v", err)
	}

	sessions := NewSessions(pool)
	if err := sessions.Create(ctx, &domain.Session{
		TokenHash: "live", UserID: owner, ExpiresAt: now.Add(time.Hour),
		CreatedAt: now, LastSeen: now,
	}); err != nil {
		t.Fatalf("create live session: %v", err)
	}
	if err := sessions.Create(ctx, &domain.Session{
		TokenHash: "dead", UserID: owner, ExpiresAt: now.Add(-time.Hour),
		CreatedAt: now, LastSeen: now,
	}); err != nil {
		t.Fatalf("create expired session: %v", err)
	}

	got, err := repo.Stats(ctx, now)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}

	if got.Users != 1 {
		t.Errorf("Users = %d, want 1", got.Users)
	}
	if got.GroupsTotal != 2 || got.GroupsActive != 1 || got.GroupsPending != 1 {
		t.Errorf("groups = total %d active %d pending %d, want 2/1/1 (soft-deleted excluded)",
			got.GroupsTotal, got.GroupsActive, got.GroupsPending)
	}
	if got.DeadlinesActive != 1 {
		t.Errorf("DeadlinesActive = %d, want 1 (soft-deleted excluded)", got.DeadlinesActive)
	}
	if got.RemindersPending != 1 || got.RemindersFailed != 1 {
		t.Errorf("reminders = pending %d failed %d, want 1/1", got.RemindersPending, got.RemindersFailed)
	}
	if got.SessionsActive != 1 {
		t.Errorf("SessionsActive = %d, want 1 (expired row excluded)", got.SessionsActive)
	}
}

// PurgeCounters чистит окна обоих счётчиков старше cut-off и не трогает свежие.
func TestMaintenancePurgeCounters(t *testing.T) {
	pool := newTestDB(t)
	ctx := context.Background()
	repo := NewMaintenance(pool)
	uid := insertUser(t, pool, 7100)

	old := time.Now().UTC().Add(-72 * time.Hour).Truncate(time.Hour)
	fresh := time.Now().UTC().Truncate(time.Hour)
	counters := NewCounters(pool)
	chats := NewChatCounters(pool)
	for _, w := range []time.Time{old, fresh} {
		if _, err := counters.IncAndCheck(ctx, uid, "group_create_day", w, 3); err != nil {
			t.Fatalf("user counter: %v", err)
		}
		if _, err := chats.IncAndCheck(ctx, -100900, "claim_chat_hour", w, 3); err != nil {
			t.Fatalf("chat counter: %v", err)
		}
	}

	n, err := repo.PurgeCounters(ctx, time.Now().UTC().Add(-48*time.Hour))
	if err != nil {
		t.Fatalf("PurgeCounters: %v", err)
	}
	if n != 2 {
		t.Errorf("purged = %d, want 2 (one old row per counter)", n)
	}
	if got, err := tableCount(ctx, pool, "user_action_counters"); err != nil || got != 1 {
		t.Errorf("user_action_counters = %d (err %v), want 1", got, err)
	}
	if got, err := tableCount(ctx, pool, "chat_action_counters"); err != nil || got != 1 {
		t.Errorf("chat_action_counters = %d (err %v), want 1", got, err)
	}

	// Свежая строка осталась именно та, что была в текущем окне.
	var window time.Time
	if err := pool.QueryRow(ctx,
		`SELECT window_start FROM user_action_counters WHERE user_id = $1`, uid).Scan(&window); err != nil {
		t.Fatalf("select fresh counter: %v", err)
	}
	if !window.Equal(fresh) {
		t.Errorf("window_start = %v, want %v", window, fresh)
	}
}

// PurgeExpiredSessions удаляет только сессии, истёкшие до cut-off (грейс).
func TestMaintenancePurgeExpiredSessions(t *testing.T) {
	pool := newTestDB(t)
	ctx := context.Background()
	repo := NewMaintenance(pool)
	uid := insertUser(t, pool, 7200)
	now := time.Now().UTC()

	sessions := NewSessions(pool)
	rows := []domain.Session{
		{TokenHash: "active", UserID: uid, ExpiresAt: now.Add(time.Hour), CreatedAt: now, LastSeen: now},
		{TokenHash: "just-expired", UserID: uid, ExpiresAt: now.Add(-time.Hour), CreatedAt: now, LastSeen: now},
		{TokenHash: "ancient", UserID: uid, ExpiresAt: now.Add(-30 * 24 * time.Hour), CreatedAt: now, LastSeen: now},
	}
	for _, s := range rows {
		s := s
		if err := sessions.Create(ctx, &s); err != nil {
			t.Fatalf("create session %s: %v", s.TokenHash, err)
		}
	}

	n, err := repo.PurgeExpiredSessions(ctx, now.Add(-7*24*time.Hour))
	if err != nil {
		t.Fatalf("PurgeExpiredSessions: %v", err)
	}
	if n != 1 {
		t.Errorf("purged = %d, want 1 (only the ancient row)", n)
	}
	var left []string
	got, err := tableCount(ctx, pool, "sessions")
	if err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if got != 2 {
		t.Fatalf("sessions = %d, want 2", got)
	}
	if err := pool.QueryRow(ctx, `SELECT coalesce(array_agg(token_hash ORDER BY token_hash), '{}') FROM sessions`).Scan(&left); err != nil {
		t.Fatalf("select tokens: %v", err)
	}
	if len(left) != 2 || left[0] != "active" || left[1] != "just-expired" {
		t.Errorf("remaining sessions = %v, want [active just-expired]", left)
	}
}

var _ domain.MaintenanceRepo = (*maintenanceRepo)(nil)
