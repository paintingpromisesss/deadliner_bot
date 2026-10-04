package httpapi

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/sauron/deadliner/internal/app/auth"
	"github.com/sauron/deadliner/internal/app/groups"
	"github.com/sauron/deadliner/internal/domain"
	"github.com/sauron/deadliner/internal/i18n"
	"github.com/sauron/deadliner/internal/platform/repo"
	"github.com/sauron/deadliner/internal/platform/slugprovider"
)

const sessionTTL = 30 * 24 * time.Hour

// newTestGroupsRouter собирает роутер на реальных репозиториях (общий
// postgres-контейнер из TestMain) с настраиваемыми лимитами групп.
func newTestGroupsRouter(t *testing.T, cfg groups.Config) http.Handler {
	t.Helper()
	pool := newTestDB(t)
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	i18n.MustLoad(i18n.Locales)
	return New(Deps{
		Auth: auth.NewService(repo.NewUsers(pool), repo.NewSessions(pool), auth.Config{
			BotToken:       testBotToken,
			AuthDateMaxAge: 24 * time.Hour,
			SessionTTL:     sessionTTL,
		}, domain.SystemClock{}),
		Groups: groups.NewService(
			repo.NewGroups(pool), repo.NewMemberships(pool), repo.NewInvites(pool),
			repo.NewCounters(pool), repo.NewAudit(pool), repo.NewBindings(pool),
			cfg, domain.SystemClock{}, log),
		Users:      repo.NewUsers(pool),
		Sessions:   repo.NewSessions(pool),
		Log:        log,
		SessionTTL: sessionTTL,
	})
}

func newTestGroupsRouterDefault(t *testing.T) http.Handler {
	t.Helper()
	return newTestGroupsRouter(t, groups.Config{
		PendingTTL:       14 * 24 * time.Hour,
		CreateDayLimit:   3,
		CreateWeekLimit:  5,
		InviteDefaultTTL: 7 * 24 * time.Hour,
	})
}

// newTestGroupsRouterWithSlugRegex — тот же роутер, но groups.Service собран
// с local-провайдером слага на заданном SLUG_REGEX: проверяем сквозной путь
// env → провайдер → 400/201, а не только unit-тесты провайдера.
func newTestGroupsRouterWithSlugRegex(t *testing.T, expr string) http.Handler {
	t.Helper()
	pool := newTestDB(t)
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	i18n.MustLoad(i18n.Locales)

	groupsSvc := groups.NewService(
		repo.NewGroups(pool), repo.NewMemberships(pool), repo.NewInvites(pool),
		repo.NewCounters(pool), repo.NewAudit(pool), repo.NewBindings(pool),
		groups.Config{
			PendingTTL:       14 * 24 * time.Hour,
			CreateDayLimit:   3,
			CreateWeekLimit:  5,
			InviteDefaultTTL: 7 * 24 * time.Hour,
		}, domain.SystemClock{}, log)
	provider, err := slugprovider.NewLocal(expr, repo.NewGroups(pool))
	if err != nil {
		t.Fatalf("slugprovider.NewLocal(%q): %v", expr, err)
	}
	groupsSvc.WithOptions(groups.Options{Slugs: provider, Users: repo.NewUsers(pool)})

	return New(Deps{
		Auth: auth.NewService(repo.NewUsers(pool), repo.NewSessions(pool), auth.Config{
			BotToken:       testBotToken,
			AuthDateMaxAge: 24 * time.Hour,
			SessionTTL:     sessionTTL,
		}, domain.SystemClock{}),
		Groups:     groupsSvc,
		Users:      repo.NewUsers(pool),
		Sessions:   repo.NewSessions(pool),
		Log:        log,
		SessionTTL: sessionTTL,
	})
}

// promoteGroupAdmin выдаёт роль admin напрямую в БД и активирует группу:
// redeem в pending-группу чужим запрещён, а активная группа нужна инвайтам.
func promoteGroupAdmin(t *testing.T, r http.Handler, token string, groupID int64) {
	t.Helper()
	resp := doJSON(r, http.MethodGet, "/api/v1/me", token, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("GET /me = %d", resp.Code)
	}
	var me map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &me); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(t.Context(),
		`UPDATE group_memberships SET role='admin' WHERE group_id=$1 AND user_id=$2`,
		groupID, int64(me["id"].(float64))); err != nil {
		t.Fatalf("promote admin: %v", err)
	}
	if _, err := testPool.Exec(t.Context(),
		`UPDATE groups SET status='active' WHERE id=$1`, groupID); err != nil {
		t.Fatalf("activate group: %v", err)
	}
}

func activateGroup(t *testing.T, groupID int64) {
	t.Helper()
	if _, err := testPool.Exec(t.Context(),
		`UPDATE groups SET status='active' WHERE id=$1`, groupID); err != nil {
		t.Fatalf("activate group: %v", err)
	}
}

// TestGroupsHappyPath — create → invite → redeem → set role → leave.
func TestGroupsHappyPath(t *testing.T) {
	r := newTestGroupsRouterDefault(t)
	adminTok, _ := login(t, r, 501)
	memberTok, _ := login(t, r, 502)

	// POST /groups → 201
	resp := doJSON(r, http.MethodPost, "/api/v1/groups", adminTok,
		map[string]any{"slug": "икбо-33-21", "title": "Моя группа"})
	if resp.Code != http.StatusCreated {
		t.Fatalf("POST /groups = %d, want 201; body: %s", resp.Code, resp.Body)
	}
	var created struct {
		Group struct {
			ID    int64  `json:"id"`
			Slug  string `json:"slug"`
			Title string `json:"title"`
		} `json:"group"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	gid := created.Group.ID
	if created.Group.Slug != "ИКБО-33-21" {
		t.Errorf("slug = %q, want normalized ИКБО-33-21", created.Group.Slug)
	}

	// GET /groups/{id} → 200 с ролью member у создателя
	resp = doJSON(r, http.MethodGet, fmt.Sprintf("/api/v1/groups/%d", gid), adminTok, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("GET /groups/%d = %d; body: %s", gid, resp.Code, resp.Body)
	}
	var details map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &details); err != nil {
		t.Fatal(err)
	}
	if details["role"] != "admin" {
		t.Errorf("creator role = %v, want admin", details["role"])
	}
	if details["members_count"] != float64(1) {
		t.Errorf("members_count = %v, want 1", details["members_count"])
	}

	// POST /groups/{id}/invites — member-инвайт → 201 {code}
	activateGroup(t, gid)
	resp = doJSON(r, http.MethodPost, fmt.Sprintf("/api/v1/groups/%d/invites", gid), adminTok,
		map[string]any{"role": "member", "max_uses": 2, "ttl_hours": 48})
	if resp.Code != http.StatusCreated {
		t.Fatalf("POST invites = %d, want 201; body: %s", resp.Code, resp.Body)
	}
	var inv struct {
		Code      string    `json:"code"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &inv); err != nil {
		t.Fatal(err)
	}
	if len(inv.Code) != 8 || inv.ExpiresAt.IsZero() {
		t.Fatalf("invite = %+v, want 8-char code and expiry", inv)
	}

	// POST /invites/redeem → 200 {group}
	resp = doJSON(r, http.MethodPost, "/api/v1/invites/redeem", memberTok,
		map[string]any{"code": inv.Code})
	if resp.Code != http.StatusOK {
		t.Fatalf("redeem = %d, want 200; body: %s", resp.Code, resp.Body)
	}

	// GET /groups/{id}/members — оба участника с именами
	resp = doJSON(r, http.MethodGet, fmt.Sprintf("/api/v1/groups/%d/members", gid), adminTok, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("GET members = %d; body: %s", resp.Code, resp.Body)
	}
	var members []map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &members); err != nil {
		t.Fatal(err)
	}
	if len(members) != 2 {
		t.Fatalf("members = %d, want 2", len(members))
	}
	var memberID float64
	for _, m := range members {
		if m["username"] == "" {
			t.Errorf("member %v has no username (JOIN не отработал)", m)
		}
		if m["role"] == "member" {
			memberID = m["user_id"].(float64)
		}
	}

	// PATCH /groups/{id}/members/{user_id} — promote в admin
	resp = doJSON(r, http.MethodPatch,
		fmt.Sprintf("/api/v1/groups/%d/members/%d", gid, int64(memberID)), adminTok,
		map[string]any{"role": "admin"})
	if resp.Code != http.StatusOK {
		t.Fatalf("PATCH member = %d, want 200; body: %s", resp.Code, resp.Body)
	}

	// DELETE /groups/{id}/me — участник выходит (админов двое)
	resp = doJSON(r, http.MethodDelete, fmt.Sprintf("/api/v1/groups/%d/me", gid), memberTok, nil)
	if resp.Code != http.StatusNoContent {
		t.Fatalf("DELETE /me = %d, want 204; body: %s", resp.Code, resp.Body)
	}
}

func TestGroupsDuplicateSlug409(t *testing.T) {
	r := newTestGroupsRouterDefault(t)
	tok1, _ := login(t, r, 601)
	tok2, _ := login(t, r, 602)

	resp := doJSON(r, http.MethodPost, "/api/v1/groups", tok1,
		map[string]any{"slug": "М8О-401Б-23", "title": "A"})
	if resp.Code != http.StatusCreated {
		t.Fatalf("first create = %d", resp.Code)
	}
	resp = doJSON(r, http.MethodPost, "/api/v1/groups", tok2,
		map[string]any{"slug": "м8о-401б-23", "title": "B"})
	if resp.Code != http.StatusConflict {
		t.Fatalf("duplicate slug = %d, want 409; body: %s", resp.Code, resp.Body)
	}
}

func TestGroupsInvalidSlug400(t *testing.T) {
	r := newTestGroupsRouterDefault(t)
	tok, _ := login(t, r, 701)

	resp := doJSON(r, http.MethodPost, "/api/v1/groups", tok,
		map[string]any{"slug": "без цифр", "title": "A"})
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("invalid slug = %d, want 400; body: %s", resp.Code, resp.Body)
	}
	var env map[string]map[string]string
	if err := json.Unmarshal(resp.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env["error"]["code"] != "slug_invalid" {
		t.Errorf("error code = %q, want slug_invalid", env["error"]["code"])
	}
}

// Сквозная проверка: charset приходит из конфига в провайдер, а не из
// зашитого в домене алфавита. «ГРУППА-1» структурно корректна, её судьбу
// решает именно регулярка.
func TestGroupsCreateHonorsSlugRegexFromConfig(t *testing.T) {
	t.Run("дефолтная регулярка отвергает латиницу вне A-Z и кириллицу вне А-Я", func(t *testing.T) {
		r := newTestGroupsRouterWithSlugRegex(t, `^[А-Я0-9]+(-[А-Я0-9]+)*$`)
		tok, _ := login(t, r, 901)

		// «GROUP-1» не проходит настроенную регулярку (латиница выключена).
		resp := doJSON(r, http.MethodPost, "/api/v1/groups", tok,
			map[string]any{"slug": "GROUP-1", "title": "T"})
		if resp.Code != http.StatusBadRequest {
			t.Fatalf("GROUP-1 under a Cyrillic-only regex = %d, want 400; body: %s", resp.Code, resp.Body)
		}
		// «ГРУППА-1» — принимается.
		resp = doJSON(r, http.MethodPost, "/api/v1/groups", tok,
			map[string]any{"slug": "ГРУППА-1", "title": "T"})
		if resp.Code != http.StatusCreated {
			t.Fatalf("ГРУППА-1 under a Cyrillic-only regex = %d, want 201; body: %s", resp.Code, resp.Body)
		}
	})

	t.Run("правило цифры сохраняется при ослабленной регулярке", func(t *testing.T) {
		// Регулярка допускает слаг без цифр (и дефисы в любом порядке) —
		// цифру всё равно требует домен: это анти-спам-правило Deadliner
		// поверх charset'а, а не часть SLUG_REGEX.
		r := newTestGroupsRouterWithSlugRegex(t, `^[А-ЯA-Z0-9-]+$`)
		tok, _ := login(t, r, 902)

		resp := doJSON(r, http.MethodPost, "/api/v1/groups", tok,
			map[string]any{"slug": "ГРУППА", "title": "T"})
		if resp.Code != http.StatusBadRequest {
			t.Fatalf("ГРУППА (no digit) = %d, want 400; body: %s", resp.Code, resp.Body)
		}
		resp = doJSON(r, http.MethodPost, "/api/v1/groups", tok,
			map[string]any{"slug": "ГРУППА-1", "title": "T"})
		if resp.Code != http.StatusCreated {
			t.Fatalf("ГРУППА-1 = %d, want 201; body: %s", resp.Code, resp.Body)
		}
	})
}

func TestGroupsRateLimit429WithRetryAfter(t *testing.T) {
	r := newTestGroupsRouterDefault(t)
	tok, _ := login(t, r, 801)

	for i, slug := range []string{"А-111", "А-222", "А-333"} {
		resp := doJSON(r, http.MethodPost, "/api/v1/groups", tok,
			map[string]any{"slug": slug, "title": "T"})
		if resp.Code != http.StatusCreated {
			t.Fatalf("create #%d = %d; body: %s", i+1, resp.Code, resp.Body)
		}
	}
	resp := doJSON(r, http.MethodPost, "/api/v1/groups", tok,
		map[string]any{"slug": "А-444", "title": "T"})
	if resp.Code != http.StatusTooManyRequests {
		t.Fatalf("4th create = %d, want 429; body: %s", resp.Code, resp.Body)
	}
	if ra := resp.Header().Get("Retry-After"); ra == "" {
		t.Errorf("429 without Retry-After header")
	}
}

func TestGroupsAdminOpsForbiddenForMember403(t *testing.T) {
	r := newTestGroupsRouterDefault(t)
	adminTok, _ := login(t, r, 901)
	memberTok, _ := login(t, r, 902)

	resp := doJSON(r, http.MethodPost, "/api/v1/groups", adminTok,
		map[string]any{"slug": "В-111", "title": "T"})
	if resp.Code != http.StatusCreated {
		t.Fatalf("create = %d", resp.Code)
	}
	var created struct {
		Group struct {
			ID int64 `json:"id"`
		} `json:"group"`
	}
	_ = json.Unmarshal(resp.Body.Bytes(), &created)
	gid := created.Group.ID
	promoteGroupAdmin(t, r, adminTok, gid)

	// Приглашаем member'а, чтобы он был в группе (иначе тоже 403).
	resp = doJSON(r, http.MethodPost, fmt.Sprintf("/api/v1/groups/%d/invites", gid), adminTok,
		map[string]any{"role": "member", "max_uses": 1})
	if resp.Code != http.StatusCreated {
		t.Fatalf("invite = %d; body: %s", resp.Code, resp.Body)
	}
	var inv struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(resp.Body.Bytes(), &inv)
	if resp = doJSON(r, http.MethodPost, "/api/v1/invites/redeem", memberTok,
		map[string]any{"code": inv.Code}); resp.Code != http.StatusOK {
		t.Fatalf("redeem = %d", resp.Code)
	}

	// member не может: обновлять группу, создавать инвайты, удалять группу.
	if resp = doJSON(r, http.MethodPatch, fmt.Sprintf("/api/v1/groups/%d", gid), memberTok,
		map[string]any{"title": "взлом"}); resp.Code != http.StatusForbidden {
		t.Errorf("member PATCH group = %d, want 403", resp.Code)
	}
	if resp = doJSON(r, http.MethodPost, fmt.Sprintf("/api/v1/groups/%d/invites", gid), memberTok,
		map[string]any{"role": "member"}); resp.Code != http.StatusForbidden {
		t.Errorf("member POST invites = %d, want 403", resp.Code)
	}
	if resp = doJSON(r, http.MethodDelete, fmt.Sprintf("/api/v1/groups/%d", gid), memberTok,
		nil); resp.Code != http.StatusForbidden {
		t.Errorf("member DELETE group = %d, want 403", resp.Code)
	}

	// Неаутентифицированный запрос → 401.
	if resp = doJSON(r, http.MethodGet, fmt.Sprintf("/api/v1/groups/%d", gid), "", nil); resp.Code != http.StatusUnauthorized {
		t.Errorf("anonymous GET group = %d, want 401", resp.Code)
	}
}

func TestGroupsBadID400AndNotFound404(t *testing.T) {
	r := newTestGroupsRouterDefault(t)
	tok, _ := login(t, r, 950)

	if resp := doJSON(r, http.MethodGet, "/api/v1/groups/abc", tok, nil); resp.Code != http.StatusBadRequest {
		t.Errorf("GET /groups/abc = %d, want 400", resp.Code)
	}
	if resp := doJSON(r, http.MethodGet, "/api/v1/groups/999999", tok, nil); resp.Code != http.StatusNotFound {
		t.Errorf("GET /groups/999999 = %d, want 404", resp.Code)
	}
}

func TestGroupsSearchAndListMine(t *testing.T) {
	r := newTestGroupsRouterDefault(t)
	tok, _ := login(t, r, 960)

	resp := doJSON(r, http.MethodPost, "/api/v1/groups", tok,
		map[string]any{"slug": "ПОИСК-11", "title": "T"})
	if resp.Code != http.StatusCreated {
		t.Fatalf("create = %d", resp.Code)
	}
	// Pending-группа видна создателю в поиске; единая форма [{group, role}].
	resp = doJSON(r, http.MethodGet, "/api/v1/groups?q=поиск", tok, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("search = %d", resp.Code)
	}
	var found []map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &found); err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 {
		t.Fatalf("search results = %v", found)
	}
	grp, _ := found[0]["group"].(map[string]any)
	if grp == nil || grp["slug"] != "ПОИСК-11" {
		t.Errorf("search result shape = %v, want {group:{slug:ПОИСК-11},role}", found[0])
	}
	if found[0]["role"] != "admin" {
		t.Errorf("search role = %v, want admin (создатель — админ)", found[0]["role"])
	}
	// Без q — мои группы с ролью.
	resp = doJSON(r, http.MethodGet, "/api/v1/groups", tok, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("list mine = %d", resp.Code)
	}
	var mine []map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &mine); err != nil {
		t.Fatal(err)
	}
	if len(mine) != 1 || mine[0]["role"] != "admin" {
		t.Errorf("list mine = %v", mine)
	}
}

func TestGroupsInviteBounds400(t *testing.T) {
	r := newTestGroupsRouterDefault(t)
	tok, _ := login(t, r, 980)

	resp := doJSON(r, http.MethodPost, "/api/v1/groups", tok,
		map[string]any{"slug": "ЛИМ-11", "title": "T"})
	if resp.Code != http.StatusCreated {
		t.Fatalf("create = %d", resp.Code)
	}
	var created struct {
		Group struct {
			ID int64 `json:"id"`
		} `json:"group"`
	}
	_ = json.Unmarshal(resp.Body.Bytes(), &created)
	gid := created.Group.ID
	promoteGroupAdmin(t, r, tok, gid)

	invitesURL := fmt.Sprintf("/api/v1/groups/%d/invites", gid)
	// max_uses = 0 — бессмысленно → 400.
	if resp = doJSON(r, http.MethodPost, invitesURL, tok,
		map[string]any{"role": "member", "max_uses": 0}); resp.Code != http.StatusBadRequest {
		t.Errorf("max_uses=0 = %d, want 400; body: %s", resp.Code, resp.Body)
	}
	// ttl_hours > 90 дней → 400.
	if resp = doJSON(r, http.MethodPost, invitesURL, tok,
		map[string]any{"role": "member", "ttl_hours": 24 * 91}); resp.Code != http.StatusBadRequest {
		t.Errorf("ttl_hours=2184 = %d, want 400; body: %s", resp.Code, resp.Body)
	}
	// ttl_hours отрицательный (кроме -1) → 400.
	if resp = doJSON(r, http.MethodPost, invitesURL, tok,
		map[string]any{"role": "member", "ttl_hours": -2}); resp.Code != http.StatusBadRequest {
		t.Errorf("ttl_hours=-2 = %d, want 400; body: %s", resp.Code, resp.Body)
	}
	// max_uses = -1 (без лимита) — валидно → 201.
	if resp = doJSON(r, http.MethodPost, invitesURL, tok,
		map[string]any{"role": "member", "max_uses": -1}); resp.Code != http.StatusCreated {
		t.Errorf("max_uses=-1 = %d, want 201; body: %s", resp.Code, resp.Body)
	}
}

func TestGroupsPresetsOverflow400(t *testing.T) {
	r := newTestGroupsRouterDefault(t)
	tok, _ := login(t, r, 990)

	resp := doJSON(r, http.MethodPost, "/api/v1/groups", tok,
		map[string]any{"slug": "ОВФ-11", "title": "T"})
	if resp.Code != http.StatusCreated {
		t.Fatalf("create = %d", resp.Code)
	}
	var created struct {
		Group struct {
			ID int64 `json:"id"`
		} `json:"group"`
	}
	_ = json.Unmarshal(resp.Body.Bytes(), &created)
	promoteGroupAdmin(t, r, tok, created.Group.ID)

	resp = doJSON(r, http.MethodPatch, fmt.Sprintf("/api/v1/groups/%d", created.Group.ID), tok,
		map[string]any{"default_presets": []int64{10*365*24*60 + 1}})
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("presets overflow = %d, want 400; body: %s", resp.Code, resp.Body)
	}
}

func TestGroupsLastAdminLeave409(t *testing.T) {
	r := newTestGroupsRouterDefault(t)
	adminTok, _ := login(t, r, 970)

	resp := doJSON(r, http.MethodPost, "/api/v1/groups", adminTok,
		map[string]any{"slug": "Г-777", "title": "T"})
	if resp.Code != http.StatusCreated {
		t.Fatalf("create = %d", resp.Code)
	}
	var created struct {
		Group struct {
			ID int64 `json:"id"`
		} `json:"group"`
	}
	_ = json.Unmarshal(resp.Body.Bytes(), &created)
	gid := created.Group.ID

	// Привязываем чат, чтобы группа считалась связанной; иначе выход удаляет группу (1.2).
	userID := meID(t, r, adminTok)
	if _, err := testPool.Exec(t.Context(),
		`INSERT INTO chat_bindings (group_id, chat_id, chat_title, bound_by) VALUES ($1, $2, 'Chat', $3)`,
		gid, int64(-100970), userID); err != nil {
		t.Fatalf("bind chat: %v", err)
	}

	resp = doJSON(r, http.MethodDelete, fmt.Sprintf("/api/v1/groups/%d/me", gid), adminTok, nil)
	if resp.Code != http.StatusConflict {
		t.Fatalf("last admin leave = %d, want 409; body: %s", resp.Code, resp.Body)
	}
	var env map[string]map[string]string
	_ = json.Unmarshal(resp.Body.Bytes(), &env)
	if env["error"]["code"] != "last_admin" {
		t.Errorf("error code = %q, want last_admin", env["error"]["code"])
	}
}

func TestGroupsUnboundCreatorLeaveDeletesGroup(t *testing.T) {
	r := newTestGroupsRouterDefault(t)
	adminTok, _ := login(t, r, 971)

	resp := doJSON(r, http.MethodPost, "/api/v1/groups", adminTok,
		map[string]any{"slug": "Г-778", "title": "T"})
	if resp.Code != http.StatusCreated {
		t.Fatalf("create = %d", resp.Code)
	}
	var created struct {
		Group struct {
			ID int64 `json:"id"`
		} `json:"group"`
	}
	_ = json.Unmarshal(resp.Body.Bytes(), &created)
	gid := created.Group.ID

	// Выход создателя из непривязанной группы каскадно удаляет её → 204.
	resp = doJSON(r, http.MethodDelete, fmt.Sprintf("/api/v1/groups/%d/me", gid), adminTok, nil)
	if resp.Code != http.StatusNoContent {
		t.Fatalf("unbound leave = %d, want 204; body: %s", resp.Code, resp.Body)
	}

	// Группа удалена из БД.
	resp = doJSON(r, http.MethodGet, fmt.Sprintf("/api/v1/groups/%d", gid), adminTok, nil)
	if resp.Code != http.StatusNotFound {
		t.Fatalf("GET deleted group = %d, want 404", resp.Code)
	}
}
