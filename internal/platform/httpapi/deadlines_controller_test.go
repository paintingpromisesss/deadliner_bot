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
	"github.com/sauron/deadliner/internal/app/deadlines"
	"github.com/sauron/deadliner/internal/app/groups"
	"github.com/sauron/deadliner/internal/domain"
	"github.com/sauron/deadliner/internal/i18n"
	"github.com/sauron/deadliner/internal/platform/repo"
)

// newTestDeadlinesRouter — роутер на реальных репозиториях (общий postgres-
// контейнер из TestMain пакета) с сервисами групп и дедлайнов.
func newTestDeadlinesRouter(t *testing.T) http.Handler {
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
			groups.Config{
				PendingTTL:       14 * 24 * time.Hour,
				CreateDayLimit:   3,
				CreateWeekLimit:  5,
				InviteDefaultTTL: 7 * 24 * time.Hour,
			}, domain.SystemClock{}, log),
		Deadlines: deadlines.NewService(
			repo.NewDeadlines(pool), repo.NewReminders(pool), repo.NewGroups(pool),
			repo.NewMemberships(pool), repo.NewAudit(pool), domain.SystemClock{}, log),
		Users:      repo.NewUsers(pool),
		Sessions:   repo.NewSessions(pool),
		Log:        log,
		I18nLoaded: true,
		SessionTTL: sessionTTL,
	})
}

// createDeadlineResp — форма ответа single-deadline эндпоинтов.
type createDeadlineResp struct {
	Deadline struct {
		ID          int64     `json:"id"`
		Title       string    `json:"title"`
		DueAt       time.Time `json:"due_at"`
		TZ          string    `json:"tz"`
		Status      string    `json:"status"`
		GroupID     *int64    `json:"group_id"`
		OwnerUserID *int64    `json:"owner_user_id"`
	} `json:"deadline"`
	Reminders []struct {
		ID            int64     `json:"id"`
		Kind          string    `json:"kind"`
		OffsetMinutes *int      `json:"offset_minutes"`
		FireAt        time.Time `json:"fire_at"`
		Status        string    `json:"status"`
	} `json:"reminders"`
}

// setupGroupWithAdmin создаёт группу и делает её создателя админом (claim —
// Task 10; напрямую в БД, как promoteGroupAdmin в groups-тестах).
func setupGroupWithAdmin(t *testing.T, r http.Handler, token string, slug string) int64 {
	t.Helper()
	resp := doJSON(r, http.MethodPost, "/api/v1/groups", token,
		map[string]any{"slug": slug, "title": "T"})
	if resp.Code != http.StatusCreated {
		t.Fatalf("create group = %d; body: %s", resp.Code, resp.Body)
	}
	var created struct {
		Group struct {
			ID int64 `json:"id"`
		} `json:"group"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	promoteGroupAdmin(t, r, token, created.Group.ID)
	return created.Group.ID
}

func TestDeadlinesPersonalCRUDHappyPath(t *testing.T) {
	r := newTestDeadlinesRouter(t)
	tok, _ := login(t, r, 3001)
	due := time.Now().UTC().Add(72 * time.Hour).Truncate(time.Second)

	// POST /deadlines → 201 с reminders.
	resp := doJSON(r, http.MethodPost, "/api/v1/deadlines", tok, map[string]any{
		"title":       "Курсовая",
		"description": "по БД",
		"due_at":      due.Format(time.RFC3339),
		"tz":          "Europe/Moscow",
		"reminders": []map[string]any{
			{"kind": "preset", "offset_minutes": 1440},
			{"kind": "custom_at", "fire_at": due.Add(-time.Hour).Format(time.RFC3339)},
		},
	})
	if resp.Code != http.StatusCreated {
		t.Fatalf("POST /deadlines = %d, want 201; body: %s", resp.Code, resp.Body)
	}
	var created createDeadlineResp
	if err := json.Unmarshal(resp.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	id := created.Deadline.ID
	if id == 0 || created.Deadline.Status != "active" || created.Deadline.OwnerUserID == nil {
		t.Errorf("deadline shape: %+v", created.Deadline)
	}
	if !created.Deadline.DueAt.Equal(due) {
		t.Errorf("due_at = %v, want %v", created.Deadline.DueAt, due)
	}
	if len(created.Reminders) != 2 {
		t.Fatalf("reminders = %+v, want 2", created.Reminders)
	}

	// GET /deadlines/{id} → 200 {deadline, reminders}.
	resp = doJSON(r, http.MethodGet, fmt.Sprintf("/api/v1/deadlines/%d", id), tok, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("GET = %d; body: %s", resp.Code, resp.Body)
	}
	var got createDeadlineResp
	if err := json.Unmarshal(resp.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Deadline.ID != id || len(got.Reminders) != 2 {
		t.Errorf("GET shape: %+v", got)
	}

	// PATCH due_at → 200, reminders перегенерированы.
	newDue := due.Add(24 * time.Hour)
	resp = doJSON(r, http.MethodPatch, fmt.Sprintf("/api/v1/deadlines/%d", id), tok,
		map[string]any{"due_at": newDue.Format(time.RFC3339)})
	if resp.Code != http.StatusOK {
		t.Fatalf("PATCH = %d; body: %s", resp.Code, resp.Body)
	}
	var patched createDeadlineResp
	_ = json.Unmarshal(resp.Body.Bytes(), &patched)
	if !patched.Deadline.DueAt.Equal(newDue) {
		t.Errorf("due after patch = %v, want %v", patched.Deadline.DueAt, newDue)
	}
	var pending int
	for _, rem := range patched.Reminders {
		if rem.Status == "pending" {
			pending++
		}
	}
	if pending != 2 {
		t.Errorf("pending reminders after due change = %d, want 2 (regenerated): %+v", pending, patched.Reminders)
	}

	// POST /deadlines/{id}/complete → 200, status done, reminders отменены.
	resp = doJSON(r, http.MethodPost, fmt.Sprintf("/api/v1/deadlines/%d/complete", id), tok, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("complete = %d; body: %s", resp.Code, resp.Body)
	}
	var completed createDeadlineResp
	_ = json.Unmarshal(resp.Body.Bytes(), &completed)
	if completed.Deadline.Status != "done" {
		t.Errorf("status = %q, want done", completed.Deadline.Status)
	}
	for _, rem := range completed.Reminders {
		if rem.Status == "pending" {
			t.Errorf("pending reminder survived complete: %+v", rem)
		}
	}

	// DELETE → 204, затем GET → 404.
	resp = doJSON(r, http.MethodDelete, fmt.Sprintf("/api/v1/deadlines/%d", id), tok, nil)
	if resp.Code != http.StatusNoContent {
		t.Fatalf("DELETE = %d, want 204; body: %s", resp.Code, resp.Body)
	}
	if resp = doJSON(r, http.MethodGet, fmt.Sprintf("/api/v1/deadlines/%d", id), tok, nil); resp.Code != http.StatusNotFound {
		t.Errorf("GET after delete = %d, want 404", resp.Code)
	}
}

func TestDeadlinesGroupFlowFull(t *testing.T) {
	r := newTestDeadlinesRouter(t)
	adminTok, _ := login(t, r, 3002)
	memberTok, _ := login(t, r, 3003)
	gid := setupGroupWithAdmin(t, r, adminTok, "ДЛ-301")
	due := time.Now().UTC().Add(30 * 24 * time.Hour).Truncate(time.Second)

	// member (по инвайту) не может создать групповой дедлайн → 403.
	resp := doJSON(r, http.MethodPost, fmt.Sprintf("/api/v1/groups/%d/invites", gid), adminTok,
		map[string]any{"role": "member", "max_uses": 1})
	if resp.Code != http.StatusCreated {
		t.Fatalf("invite = %d", resp.Code)
	}
	var inv struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(resp.Body.Bytes(), &inv)
	if resp = doJSON(r, http.MethodPost, "/api/v1/invites/redeem", memberTok,
		map[string]any{"code": inv.Code}); resp.Code != http.StatusOK {
		t.Fatalf("redeem = %d", resp.Code)
	}

	resp = doJSON(r, http.MethodPost, "/api/v1/deadlines", memberTok, map[string]any{
		"group_id": gid, "title": "Экзамен", "due_at": due.Format(time.RFC3339),
	})
	if resp.Code != http.StatusForbidden {
		t.Errorf("member create group deadline = %d, want 403; body: %s", resp.Code, resp.Body)
	}

	// admin создаёт без reminders → подставляются пресеты группы (7д/3д/24ч).
	resp = doJSON(r, http.MethodPost, "/api/v1/deadlines", adminTok, map[string]any{
		"group_id": gid, "title": "Экзамен", "due_at": due.Format(time.RFC3339),
	})
	if resp.Code != http.StatusCreated {
		t.Fatalf("admin create = %d; body: %s", resp.Code, resp.Body)
	}
	var created createDeadlineResp
	_ = json.Unmarshal(resp.Body.Bytes(), &created)
	if created.Deadline.GroupID == nil || *created.Deadline.GroupID != gid {
		t.Errorf("group_id = %v, want %d", created.Deadline.GroupID, gid)
	}
	if len(created.Reminders) != 3 {
		t.Errorf("reminders = %+v, want 3 group presets", created.Reminders)
	}

	// member читает список группы и сам дедлайн.
	resp = doJSON(r, http.MethodGet, fmt.Sprintf("/api/v1/groups/%d/deadlines", gid), memberTok, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("member list group = %d; body: %s", resp.Code, resp.Body)
	}
	var list []map[string]any
	_ = json.Unmarshal(resp.Body.Bytes(), &list)
	if len(list) != 1 || list[0]["title"] != "Экзамен" {
		t.Errorf("group list = %v", list)
	}
	if resp = doJSON(r, http.MethodGet, fmt.Sprintf("/api/v1/deadlines/%d", created.Deadline.ID), memberTok, nil); resp.Code != http.StatusOK {
		t.Errorf("member get = %d, want 200", resp.Code)
	}

	// Не-участник не видит ни список, ни дедлайн.
	outsiderTok, _ := login(t, r, 3004)
	if resp = doJSON(r, http.MethodGet, fmt.Sprintf("/api/v1/groups/%d/deadlines", gid), outsiderTok, nil); resp.Code != http.StatusForbidden {
		t.Errorf("outsider list = %d, want 403", resp.Code)
	}
	if resp = doJSON(r, http.MethodGet, fmt.Sprintf("/api/v1/deadlines/%d", created.Deadline.ID), outsiderTok, nil); resp.Code != http.StatusForbidden {
		t.Errorf("outsider get = %d, want 403", resp.Code)
	}
}

func TestDeadlinesMeListScopeAndFilters(t *testing.T) {
	r := newTestDeadlinesRouter(t)
	tok, _ := login(t, r, 3005)
	now := time.Now().UTC().Truncate(time.Second)

	// Личный + групповой дедлайны.
	resp := doJSON(r, http.MethodPost, "/api/v1/deadlines", tok, map[string]any{
		"title": "P", "due_at": now.Add(2 * time.Hour).Format(time.RFC3339),
	})
	if resp.Code != http.StatusCreated {
		t.Fatalf("personal = %d", resp.Code)
	}
	gid := setupGroupWithAdmin(t, r, tok, "МИ-305")
	resp = doJSON(r, http.MethodPost, "/api/v1/deadlines", tok, map[string]any{
		"group_id": gid, "title": "G", "due_at": now.Add(time.Hour).Format(time.RFC3339),
	})
	if resp.Code != http.StatusCreated {
		t.Fatalf("group = %d", resp.Code)
	}

	// Без scope — только личные.
	resp = doJSON(r, http.MethodGet, "/api/v1/me/deadlines", tok, nil)
	var only []map[string]any
	_ = json.Unmarshal(resp.Body.Bytes(), &only)
	if resp.Code != http.StatusOK || len(only) != 1 || only[0]["title"] != "P" {
		t.Errorf("me/deadlines = %d %v, want only P", resp.Code, only)
	}

	// scope=all — оба, сортировка due_at ASC (G раньше P).
	resp = doJSON(r, http.MethodGet, "/api/v1/me/deadlines?scope=all", tok, nil)
	var all []map[string]any
	_ = json.Unmarshal(resp.Body.Bytes(), &all)
	if resp.Code != http.StatusOK || len(all) != 2 || all[0]["title"] != "G" || all[1]["title"] != "P" {
		t.Errorf("scope=all = %d %v", resp.Code, all)
	}

	// Фильтр окна: to = now+90min → только G.
	resp = doJSON(r, http.MethodGet,
		"/api/v1/me/deadlines?scope=all&to="+now.Add(90*time.Minute).Format(time.RFC3339), tok, nil)
	var win []map[string]any
	_ = json.Unmarshal(resp.Body.Bytes(), &win)
	if len(win) != 1 || win[0]["title"] != "G" {
		t.Errorf("window filter = %v", win)
	}

	// Мусорный from → 400.
	if resp = doJSON(r, http.MethodGet, "/api/v1/me/deadlines?from=не-дата", tok, nil); resp.Code != http.StatusBadRequest {
		t.Errorf("bad from = %d, want 400", resp.Code)
	}
	// Неизвестный scope → 400.
	if resp = doJSON(r, http.MethodGet, "/api/v1/me/deadlines?scope=bogus", tok, nil); resp.Code != http.StatusBadRequest {
		t.Errorf("bad scope = %d, want 400", resp.Code)
	}
}

func TestDeadlinesBadInput400(t *testing.T) {
	r := newTestDeadlinesRouter(t)
	tok, _ := login(t, r, 3006)

	// due_at не RFC3339 → 400.
	resp := doJSON(r, http.MethodPost, "/api/v1/deadlines", tok, map[string]any{
		"title": "T", "due_at": "завтра в пять",
	})
	if resp.Code != http.StatusBadRequest {
		t.Errorf("garbage due_at = %d, want 400; body: %s", resp.Code, resp.Body)
	}
	// due_at в прошлом → 400.
	resp = doJSON(r, http.MethodPost, "/api/v1/deadlines", tok, map[string]any{
		"title": "T", "due_at": time.Now().Add(-time.Hour).Format(time.RFC3339),
	})
	if resp.Code != http.StatusBadRequest {
		t.Errorf("past due_at = %d, want 400", resp.Code)
	}
	// Пустой title → 400.
	resp = doJSON(r, http.MethodPost, "/api/v1/deadlines", tok, map[string]any{
		"title": "", "due_at": time.Now().Add(time.Hour).Format(time.RFC3339),
	})
	if resp.Code != http.StatusBadRequest {
		t.Errorf("empty title = %d, want 400", resp.Code)
	}
	// Неизвестное поле → 400 (DisallowUnknownFields).
	resp = doJSON(r, http.MethodPost, "/api/v1/deadlines", tok, map[string]any{
		"title": "T", "due_at": time.Now().Add(time.Hour).Format(time.RFC3339), "bogus": 1,
	})
	if resp.Code != http.StatusBadRequest {
		t.Errorf("unknown field = %d, want 400", resp.Code)
	}
	// offset < 5 минут → 400.
	resp = doJSON(r, http.MethodPost, "/api/v1/deadlines", tok, map[string]any{
		"title": "T", "due_at": time.Now().Add(time.Hour).Format(time.RFC3339),
		"reminders": []map[string]any{{"kind": "preset", "offset_minutes": 4}},
	})
	if resp.Code != http.StatusBadRequest {
		t.Errorf("offset 4min = %d, want 400", resp.Code)
	}
	// Мусорный id в пути → 400, отсутствующий → 404.
	if resp = doJSON(r, http.MethodGet, "/api/v1/deadlines/abc", tok, nil); resp.Code != http.StatusBadRequest {
		t.Errorf("GET /deadlines/abc = %d, want 400", resp.Code)
	}
	if resp = doJSON(r, http.MethodGet, "/api/v1/deadlines/999999", tok, nil); resp.Code != http.StatusNotFound {
		t.Errorf("GET missing = %d, want 404", resp.Code)
	}
	// Неаутентифицированный → 401.
	if resp = doJSON(r, http.MethodPost, "/api/v1/deadlines", "", nil); resp.Code != http.StatusUnauthorized {
		t.Errorf("anonymous = %d, want 401", resp.Code)
	}
}

func TestDeadlinesForeignPersonal403(t *testing.T) {
	r := newTestDeadlinesRouter(t)
	ownerTok, _ := login(t, r, 3007)
	strangerTok, _ := login(t, r, 3008)
	due := time.Now().UTC().Add(72 * time.Hour)

	resp := doJSON(r, http.MethodPost, "/api/v1/deadlines", ownerTok, map[string]any{
		"title": "Личное", "due_at": due.Format(time.RFC3339),
	})
	if resp.Code != http.StatusCreated {
		t.Fatalf("create = %d", resp.Code)
	}
	var created createDeadlineResp
	_ = json.Unmarshal(resp.Body.Bytes(), &created)
	id := created.Deadline.ID

	if resp = doJSON(r, http.MethodGet, fmt.Sprintf("/api/v1/deadlines/%d", id), strangerTok, nil); resp.Code != http.StatusForbidden {
		t.Errorf("stranger GET = %d, want 403", resp.Code)
	}
	if resp = doJSON(r, http.MethodPatch, fmt.Sprintf("/api/v1/deadlines/%d", id), strangerTok,
		map[string]any{"title": "взлом"}); resp.Code != http.StatusForbidden {
		t.Errorf("stranger PATCH = %d, want 403", resp.Code)
	}
	if resp = doJSON(r, http.MethodDelete, fmt.Sprintf("/api/v1/deadlines/%d", id), strangerTok, nil); resp.Code != http.StatusForbidden {
		t.Errorf("stranger DELETE = %d, want 403", resp.Code)
	}
	if resp = doJSON(r, http.MethodPost, fmt.Sprintf("/api/v1/deadlines/%d/complete", id), strangerTok, nil); resp.Code != http.StatusForbidden {
		t.Errorf("stranger complete = %d, want 403", resp.Code)
	}
}
