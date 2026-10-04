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

// Живое НЕДЕЛЬНОЕ окно (group_create_week, 168ч) обязано выжить. Окно
// floor-ится методом Truncate(168h), поэтому его window_start бывает почти
// 168ч от роду — retention 48ч удалял живую строку и лимит переставал
// срабатывать. Ретенция 192ч: живое окно остаётся, строка старше 192ч
// вычищается. Обе таблицы счётчиков (user и chat) чистятся одинаково.
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
	// Вырожденный случай (совпадение до микросекунды) схлопнул бы две строки в
	// одну: разводим их на микросекунду — возраст 150ч остаётся «живым
	// недельным».
	if oldWeekWindow.Equal(weekWindow) {
		oldWeekWindow = oldWeekWindow.Add(-time.Microsecond)
	}
	stale := now.Add(-200 * time.Hour).Truncate(time.Hour)
	for name, w := range map[string]time.Time{"week": weekWindow, "150h": oldWeekWindow} {
		if age := now.Sub(w); age <= 0 || age > 168*time.Hour {
			t.Fatalf("%s window %v has age %v, want within (0, 168h] — a live week bucket", name, w, age)
		}
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
		// chat_action_counters в этом тесте заводится на chat_id -100950.
		owner := uid
		if table == "chat_action_counters" {
			owner = -100950
		}
		got := selectWindowStarts(t, ctx, pool, table, owner)
		assertWindowSet(t, table, got, weekWindow, oldWeekWindow)
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

	// Граница ретенции — вне календарной зависимости: возраст 167ч (максимум
	// для окна Truncate(168h)) выживает при 192ч; 193ч вычищается.
	t.Run("retention boundary is calendar-independent", func(t *testing.T) {
		boundaryUID := insertUser(t, pool, 7301)
		liveMaxAge := now.Add(-167 * time.Hour).Truncate(time.Microsecond)
		tooOld := now.Add(-193 * time.Hour).Truncate(time.Microsecond)
		for _, w := range []time.Time{liveMaxAge, tooOld} {
			if _, err := counters.IncAndCheck(ctx, boundaryUID, "group_create_week", w, 5); err != nil {
				t.Fatalf("counter %v: %v", w, err)
			}
		}

		if n, err := repo.PurgeCounters(ctx, now.Add(-192*time.Hour)); err != nil {
			t.Fatalf("PurgeCounters(192h): %v", err)
		} else if n < 1 {
			t.Errorf("purged with the 192h cutoff = %d, want >= 1 (the 193h-old row)", n)
		}
		assertWindowSet(t, "192h cutoff", selectWindowStarts(t, ctx, pool, "user_action_counters", boundaryUID), liveMaxAge)

		// Живая недельная строка на максимальном возрасте. Счётчик строк общий
		// по таблице (строки внешнего теста тоже подпадают), поэтому проверяем
		// выборку ИМЕННО этого пользователя.
		if _, err := repo.PurgeCounters(ctx, now.Add(-48*time.Hour)); err != nil {
			t.Fatalf("PurgeCounters(48h): %v", err)
		}
		assertWindowSet(t, "48h cutoff", selectWindowStarts(t, ctx, pool, "user_action_counters", boundaryUID))
	})

	// Проверка выживания нечувствительна к порядку строк: weekWindow и
	// 150-часовая строка меняются местами относительно календаря.
	t.Run("survival assertion is order-independent", func(t *testing.T) {
		// Оба порядка — обе стороны календарной фазы, ни один не должен падать.
		assertWindowSet(t, "week-first", []time.Time{weekWindow, oldWeekWindow}, weekWindow, oldWeekWindow)
		assertWindowSet(t, "old-first", []time.Time{oldWeekWindow, weekWindow}, weekWindow, oldWeekWindow)

		// Свип фаз календаря (синтетические моменты, без wall-clock): возраст
		// окна Truncate(168h) пробегает 0..168ч, и при 192ч-ретенции оба живых
		// окна обязаны пережить уборку в КАЖДОЙ фазе — иначе тест был бы
		// чувствителен к моменту запуска.
		for _, phase := range []time.Duration{
			0, time.Hour, 12 * time.Hour, 47 * time.Hour, 48 * time.Hour,
			72 * time.Hour, 150 * time.Hour, 151 * time.Hour, 167 * time.Hour, 168*time.Hour - time.Microsecond,
		} {
			base := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
			at := base.Add(phase)
			week := at.Truncate(168 * time.Hour).Truncate(time.Microsecond)
			old := at.Add(-150 * time.Hour).Truncate(time.Microsecond)
			if old.Equal(week) {
				old = old.Add(-time.Microsecond)
			}
			cut := at.Add(-192 * time.Hour)
			// При 192ч обе строки живут, в любом порядке и в любой фазе.
			for _, w := range []time.Time{week, old} {
				if !w.After(cut) {
					t.Fatalf("phase %v: %v would be purged by the 192h retention (age %v)", phase, w, at.Sub(w))
				}
			}
			got := []time.Time{week, old}
			if !week.After(old) {
				got = []time.Time{old, week} // вторая сторона фазы
			}
			assertWindowSet(t, "phase", got, week, old)
			// Ретенция 48ч удаляет 150-часовую строку в любой фазе — именно это
			// и ловится основным тестом.
			cut48 := at.Add(-48 * time.Hour)
			if old.After(cut48) {
				t.Fatalf("phase %v: the 150h window %v would survive a 48h purge", phase, old)
			}
		}
	})
}

// selectWindowStarts читает window_start строк счётчика владельца userID
// (для user_action_counters — по user_id; для chat_action_counters — по
// chat_id, который в этих тестах играет ту же роль). Явный column list, без
// SELECT *.
func selectWindowStarts(t *testing.T, ctx context.Context, pool *pgxpool.Pool, table string, ownerID int64) []time.Time {
	t.Helper()
	var q string
	switch table {
	case "user_action_counters":
		q = "SELECT window_start FROM user_action_counters WHERE user_id = $1"
	case "chat_action_counters":
		q = "SELECT window_start FROM chat_action_counters WHERE chat_id = $1"
	default:
		t.Fatalf("selectWindowStarts: unexpected table %q", table)
	}
	rows, err := pool.Query(ctx, q, ownerID)
	if err != nil {
		t.Fatalf("%s: select windows: %v", table, err)
	}
	defer rows.Close()
	var out []time.Time
	for rows.Next() {
		var w time.Time
		if err := rows.Scan(&w); err != nil {
			t.Fatalf("%s: scan window: %v", table, err)
		}
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("%s: rows: %v", table, err)
	}
	return out
}

// assertWindowSet проверяет выборку как МНОЖЕСТВО: ровно want строк и каждая
// ожидаемая встречается. Порядок входа не важен — взаимное расположение
// окон зависит от календаря.
func assertWindowSet(t *testing.T, label string, got []time.Time, want ...time.Time) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: surviving windows = %v, want exactly %v", label, got, want)
	}
	seen := make(map[int64]bool, len(got))
	for _, w := range got {
		seen[w.UnixMicro()] = true
	}
	for _, w := range want {
		if !seen[w.UnixMicro()] {
			t.Errorf("%s: surviving windows = %v, missing %v (a 48h retention would have purged it)",
				label, got, w)
		}
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
