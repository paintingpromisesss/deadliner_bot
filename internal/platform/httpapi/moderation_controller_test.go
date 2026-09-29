package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sauron/deadliner/internal/app/moderation"
	"github.com/sauron/deadliner/internal/domain"
	"github.com/sauron/deadliner/internal/platform/repo"
)

// newTestModerationService — moderation.Service на реальных репозиториях
// (общий postgres-контейнер пакета).
func newTestModerationService(pool *pgxpool.Pool) *moderation.Service {
	return moderation.NewService(moderation.Deps{
		Groups:      repo.NewGroups(pool),
		Deadlines:   repo.NewDeadlines(pool),
		Reminders:   repo.NewReminders(pool),
		Memberships: repo.NewMemberships(pool),
		Bindings:    repo.NewBindings(pool),
		Users:       repo.NewUsers(pool),
		Sessions:    repo.NewSessions(pool),
		Maintenance: repo.NewMaintenance(pool),
		Audit:       repo.NewAudit(pool),
		Clock:       domain.SystemClock{},
		Config: moderation.Config{
			PendingTTL:       14 * 24 * time.Hour,
			CounterRetention: 8 * 24 * time.Hour,
		},
	})
}

// meID — users.id вызывающего по его токену.
func meID(t *testing.T, r http.Handler, token string) int64 {
	t.Helper()
	resp := doJSON(r, http.MethodGet, "/api/v1/me", token, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("GET /me = %d; body: %s", resp.Code, resp.Body)
	}
	var me struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &me); err != nil {
		t.Fatalf("decode /me: %v", err)
	}
	return me.ID
}

// markBanned — флаг бана напрямую в БД (сессии сохраняются): так проверяется
// именно middleware-ветка отказа, которую BanUser закрывает ещё и отзывом
// токенов.
func markBanned(t *testing.T, telegramID int64, banned bool) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(),
		`UPDATE users SET is_banned = $2 WHERE telegram_id = $1`, telegramID, banned); err != nil {
		t.Fatalf("set is_banned: %v", err)
	}
}

// Забаненный отклоняется middleware.Auth на ЛЮБОМ аутентифицированном запросе
// (403) — это и делает бан точкой принуждения спеки §3.3 (создание групп,
// claim, привязка чата идут через API).
func TestBanForbiddenByMiddleware(t *testing.T) {
	r := newTestGroupsRouterDefault(t)
	tok, _ := login(t, r, 801)

	if resp := doJSON(r, http.MethodGet, "/api/v1/groups", tok, nil); resp.Code != http.StatusOK {
		t.Fatalf("GET /groups before ban = %d, want 200", resp.Code)
	}
	markBanned(t, 801, true)

	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/groups"},
		{http.MethodPost, "/api/v1/groups"},
		{http.MethodGet, "/api/v1/me"},
	} {
		resp := doJSON(r, c.method, c.path, tok, nil)
		if resp.Code != http.StatusForbidden {
			t.Errorf("%s %s after ban = %d, want 403; body: %s", c.method, c.path, resp.Code, resp.Body)
		}
	}

	// Повторный вход через initData запрещён (auth.Login читает флаг).
	if resp := doJSON(r, http.MethodPost, "/api/v1/auth/telegram", "",
		map[string]string{"initData": validInitData(t, 801)}); resp.Code != http.StatusForbidden {
		t.Errorf("POST /auth/telegram for a banned user = %d, want 403; body: %s",
			resp.Code, resp.Body)
	}

	// Снятие бана возвращает доступ (сессии не отзывались этой веткой).
	markBanned(t, 801, false)
	if resp := doJSON(r, http.MethodGet, "/api/v1/groups", tok, nil); resp.Code != http.StatusOK {
		t.Errorf("GET /groups after unban = %d, want 200; body: %s", resp.Code, resp.Body)
	}
}

// BanUser отзывает все сессии немедленно: старый токен не проходит даже после
// снятия флага (строки сессии больше нет → 401). Именно ради отзыва выбран
// opaque-токен вместо JWT (спека §5.1).
func TestBanRevokesSessions(t *testing.T) {
	r := newTestGroupsRouterDefault(t)
	tok, _ := login(t, r, 802)

	svc := newTestModerationService(testPool)
	if err := svc.BanUser(t.Context(), moderation.SystemActor, 802); err != nil {
		t.Fatalf("BanUser: %v", err)
	}
	// Сессия уже отозвана, поэтому запрос падает на 401 (нет токена в БД) —
	// 403 от middleware достаётся забаненному с ЖИВОЙ сессией (отдельный тест).
	if resp := doJSON(r, http.MethodGet, "/api/v1/groups", tok, nil); resp.Code != http.StatusUnauthorized {
		t.Errorf("GET /groups with a revoked session = %d, want 401", resp.Code)
	}
	var sessions int
	if err := testPool.QueryRow(t.Context(), `SELECT count(*) FROM sessions`).Scan(&sessions); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if sessions != 0 {
		t.Errorf("sessions = %d, want 0 after ban", sessions)
	}

	if err := svc.UnbanUser(t.Context(), moderation.SystemActor, 802); err != nil {
		t.Fatalf("UnbanUser: %v", err)
	}
	if resp := doJSON(r, http.MethodGet, "/api/v1/me", tok, nil); resp.Code != http.StatusUnauthorized {
		t.Errorf("GET /me with a revoked token = %d, want 401", resp.Code)
	}
}

// Забаненный не создаёт группу и не начинает claim: запрос падает на
// middleware (403) до контроллера, побочных эффектов в БД нет.
func TestBanBlocksGroupCreationAndClaim(t *testing.T) {
	nfy := &captureNotifier{}
	r := newTestClaimsRouter(t, nfy, claimsDefaultConfig())
	tok, _ := login(t, r, 803)

	resp := doJSON(r, http.MethodPost, "/api/v1/groups", tok,
		map[string]any{"slug": "ИКБО-77-21", "title": "До бана"})
	if resp.Code != http.StatusCreated {
		t.Fatalf("create before ban = %d; body: %s", resp.Code, resp.Body)
	}
	var created struct {
		Group struct {
			ID int64 `json:"id"`
		} `json:"group"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}

	markBanned(t, 803, true)

	resp = doJSON(r, http.MethodPost, "/api/v1/groups", tok,
		map[string]any{"slug": "ОВФ-77", "title": "После бана"})
	if resp.Code != http.StatusForbidden {
		t.Fatalf("create after ban = %d, want 403; body: %s", resp.Code, resp.Body)
	}
	var groups int
	if err := testPool.QueryRow(t.Context(),
		`SELECT count(*) FROM groups WHERE slug_norm = 'ОВФ-77'`).Scan(&groups); err != nil {
		t.Fatalf("count groups: %v", err)
	}
	if groups != 0 {
		t.Error("banned user's group was created")
	}

	resp = doJSON(r, http.MethodPost,
		"/api/v1/groups/"+strconv.FormatInt(created.Group.ID, 10)+"/claim/start", tok, nil)
	if resp.Code != http.StatusForbidden {
		t.Errorf("claim after ban = %d, want 403; body: %s", resp.Code, resp.Body)
	}
	if len(nfy.chatSends) != 0 {
		t.Errorf("claim code posted for a banned user: %+v", nfy.chatSends)
	}
}

// cleanup pending-групп через use case (спека §3.3) — сквозная проверка
// репозиторного отбора на реальной схеме: протухшая pending-группа без
// привязки и админа удаляется, привязанная — остаётся.
func TestCleanupExpiredPendingEndToEnd(t *testing.T) {
	r := newTestGroupsRouterDefault(t)
	tok, _ := login(t, r, 804)

	mk := func(slug string) int64 {
		resp := doJSON(r, http.MethodPost, "/api/v1/groups", tok,
			map[string]any{"slug": slug, "title": "Группа " + slug})
		if resp.Code != http.StatusCreated {
			t.Fatalf("create %s = %d; body: %s", slug, resp.Code, resp.Body)
		}
		var created struct {
			Group struct {
				ID int64 `json:"id"`
			} `json:"group"`
		}
		if err := json.Unmarshal(resp.Body.Bytes(), &created); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return created.Group.ID
	}
	doomed := mk("ИКБО-81-21")
	bound := mk("ИКБО-82-21")

	// Обе группы протухают; у одной появляется привязка чата.
	if _, err := testPool.Exec(t.Context(),
		`UPDATE groups SET claim_expires_at = now() - interval '1 hour' WHERE id = ANY($1)`,
		[]int64{doomed, bound}); err != nil {
		t.Fatalf("expire groups: %v", err)
	}
	userID := meID(t, r, tok)
	if _, err := testPool.Exec(t.Context(),
		`INSERT INTO chat_bindings (group_id, chat_id, chat_title, bound_by)
		 VALUES ($1, -100804, 'Чат', $2)`, bound, userID); err != nil {
		t.Fatalf("bind chat: %v", err)
	}

	svc := newTestModerationService(testPool)
	report, err := svc.CleanupExpiredPending(t.Context())
	if err != nil {
		t.Fatalf("CleanupExpiredPending: %v", err)
	}
	if report.Groups != 1 {
		t.Errorf("report.Groups = %d, want 1 (only the unbound group)", report.Groups)
	}

	var deletedDoomed, deletedBound *time.Time
	if err := testPool.QueryRow(t.Context(),
		`SELECT deleted_at FROM groups WHERE id = $1`, doomed).Scan(&deletedDoomed); err != nil {
		t.Fatalf("select doomed: %v", err)
	}
	if err := testPool.QueryRow(t.Context(),
		`SELECT deleted_at FROM groups WHERE id = $1`, bound).Scan(&deletedBound); err != nil {
		t.Fatalf("select bound: %v", err)
	}
	if deletedDoomed == nil {
		t.Error("expired unbound pending group was not deleted")
	}
	if deletedBound != nil {
		t.Error("expired pending group with a chat binding was deleted")
	}

	// Аудит прогона — системный актор (actor_user_id NULL).
	var actor *int64
	if err := testPool.QueryRow(t.Context(),
		`SELECT actor_user_id FROM audit_log WHERE action = 'cleanup.run' ORDER BY id DESC LIMIT 1`).
		Scan(&actor); err != nil {
		t.Fatalf("select audit: %v", err)
	}
	if actor != nil {
		t.Errorf("cleanup audit actor = %v, want NULL", *actor)
	}
}
