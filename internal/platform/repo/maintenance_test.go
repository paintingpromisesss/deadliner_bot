package repo

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

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

// Регрессия C-1: живое НЕДЕЛЬНОЕ окно (group_create_week, 168ч) обязано
// выжить. Окно floor-ится методом Truncate(168h), поэтому его window_start
// бывает почти 168ч от роду — retention 48ч удалял живую строку и
// LIMIT_GROUP_CREATE_WEEK молча переставал срабатывать. Ретенция 192ч:
// строка живого недельного окна остаётся, строка старше 192ч вычищается.
// Обе таблицы счётчиков (user и chat) чистятся одинаково.
func TestMaintenancePurgeCountersKeepsLiveWeekWindow(t *testing.T) {
	pool := newTestDB(t)
	ctx := context.Background()
	repo := NewMaintenance(pool)
	uid := insertUser(t, pool, 7300)
	now := time.Now().UTC()

	// Живое недельное окно: Truncate(168h) от «сейчас» — возраст до 168ч.
	weekWindow := now.Truncate(168 * time.Hour).Truncate(time.Microsecond)
	// Второе живое недельное окно с ГАРАНТИРОВАННЫМ возрастом 150ч: возраст
	// окна Truncate(168h) зависит от календаря (0..168ч), поэтому для
	// детерминированной проверки границы нужна явная дата. Микросекунды — как
	// хранит timestamptz (иначе сравнение instants разойдётся на наносекундах).
	oldWeekWindow := now.Add(-150 * time.Hour).Truncate(time.Microsecond)
	stale := now.Add(-200 * time.Hour).Truncate(time.Hour)
	if !weekWindow.Before(now) {
		t.Fatalf("week window %v must be in the past", weekWindow)
	}

	counters := NewCounters(pool)
	chats := NewChatCounters(pool)
	for _, w := range []time.Time{weekWindow, oldWeekWindow, stale} {
		if _, err := counters.IncAndCheck(ctx, uid, "group_create_week", w, 5); err != nil {
			t.Fatalf("user counter: %v", err)
		}
		if _, err := chats.IncAndCheck(ctx, -100950, "claim_chat_hour", w, 3); err != nil {
			t.Fatalf("chat counter: %v", err)
		}
	}

	n, err := repo.PurgeCounters(ctx, now.Add(-192*time.Hour))
	if err != nil {
		t.Fatalf("PurgeCounters: %v", err)
	}
	if n != 2 {
		t.Errorf("purged = %d, want 2 (one 200h-old row per counter)", n)
	}

	for _, table := range []string{"user_action_counters", "chat_action_counters"} {
		var windows []time.Time
		rows, err := pool.Query(ctx, "SELECT window_start FROM "+table+" ORDER BY window_start")
		if err != nil {
			t.Fatalf("%s: select surviving windows: %v", table, err)
		}
		for rows.Next() {
			var w time.Time
			if err := rows.Scan(&w); err != nil {
				rows.Close()
				t.Fatalf("%s: scan window: %v", table, err)
			}
			windows = append(windows, w)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			t.Fatalf("%s: rows: %v", table, err)
		}
		if len(windows) != 2 {
			t.Fatalf("%s: surviving rows = %v, want the live week window %v and the 150h-old one %v",
				table, windows, weekWindow, oldWeekWindow)
		}
		for i, want := range []time.Time{oldWeekWindow, weekWindow} {
			if !windows[i].Equal(want) {
				t.Errorf("%s: surviving[%d] = %v, want %v (48h retention would have purged it)",
					table, i, windows[i], want)
			}
		}
	}

	// Живая строка по-прежнему расходует недельный лимит: счётчик продолжает
	// расти от прежнего значения, а не начинается с 1 — то есть строка живого
	// недельного окна НЕ удалена.
	got, err := counters.IncAndCheck(ctx, uid, "group_create_week", weekWindow, 5)
	if err != nil {
		t.Fatalf("IncAndCheck after purge: %v", err)
	}
	if got != 2 {
		t.Errorf("week counter after purge = %d, want 2 (the live bucket survived; 1 would mean the row was purged)", got)
	}

	// Демонстрация самой регрессии C-1 на 150-часовой живой строке: с прежним
	// retention (48ч) она действительно удаляется — значит проверки выше ловят
	// именно баг, а не «случайно зелёный» тест.
	purged48, err := repo.PurgeCounters(ctx, now.Add(-48*time.Hour))
	if err != nil {
		t.Fatalf("PurgeCounters(48h): %v", err)
	}
	if purged48 == 0 {
		t.Fatal("PurgeCounters(48h) purged nothing: the C-1 assertion above proves nothing")
	}
	var left int64
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM user_action_counters WHERE window_start = $1`, oldWeekWindow).Scan(&left); err != nil {
		t.Fatalf("count 150h-old week row after 48h purge: %v", err)
	}
	if left != 0 {
		t.Errorf("150h-old week row survived a 48h purge (count=%d), want it purged — this is the C-1 regression", left)
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

// tableCount — тестовый помощник: число строк таблицы. Имя таблицы приходит из
// самого теста (константа из закрытого списка) — параметров для имён таблиц в
// SQL не существует, поэтому подстановка допустима; запрос явный, без SELECT *.
func tableCount(ctx context.Context, pool *pgxpool.Pool, table string) (int64, error) {
	switch table {
	case "user_action_counters", "chat_action_counters", "sessions", "audit_log":
	default:
		return 0, fmt.Errorf("tableCount: unexpected table %q", table)
	}
	var n int64
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}
