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
		I18nLoaded: true,
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

// promoteGroupAdmin выдаёт роль admin напрямую в БД и активирует группу:
// в рамках Task 7 это единственный путь к админству — создатель группы
// намеренно member (спека §3.1), а claim-флоу появится в Task 10. Активация
// нужна, потому что redeem в pending-группу чужим запрещён (finding #3).
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
	if details["role"] != "member" {
		t.Errorf("creator role = %v, want member", details["role"])
	}
	if details["members_count"] != float64(1) {
		t.Errorf("members_count = %v, want 1", details["members_count"])
	}

	// Создатель — member (claim в Task 10); для инвайтов нужен admin.
	promoteGroupAdmin(t, r, adminTok, gid)

	// POST /groups/{id}/invites — member-инвайт → 201 {code}
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

	// DELETE /groups/{id}/me — участник выходит (админов теперь двое)
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
	if found[0]["role"] != "member" {
		t.Errorf("search role = %v, want member (создатель — участник)", found[0]["role"])
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
	if len(mine) != 1 || mine[0]["role"] != "member" {
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
	// ttl_hours отрицательный → 400.
	if resp = doJSON(r, http.MethodPost, invitesURL, tok,
		map[string]any{"role": "member", "ttl_hours": -1}); resp.Code != http.StatusBadRequest {
		t.Errorf("ttl_hours=-1 = %d, want 400; body: %s", resp.Code, resp.Body)
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
	promoteGroupAdmin(t, r, adminTok, created.Group.ID)

	resp = doJSON(r, http.MethodDelete, fmt.Sprintf("/api/v1/groups/%d/me", created.Group.ID), adminTok, nil)
	if resp.Code != http.StatusConflict {
		t.Fatalf("last admin leave = %d, want 409; body: %s", resp.Code, resp.Body)
	}
	var env map[string]map[string]string
	_ = json.Unmarshal(resp.Body.Bytes(), &env)
	if env["error"]["code"] != "last_admin" {
		t.Errorf("error code = %q, want last_admin", env["error"]["code"])
	}
}
