package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/sauron/deadliner/internal/app/auth"
	"github.com/sauron/deadliner/internal/app/groups"
	"github.com/sauron/deadliner/internal/app/notifications"
	"github.com/sauron/deadliner/internal/domain"
	"github.com/sauron/deadliner/internal/i18n"
	"github.com/sauron/deadliner/internal/platform/repo"
)

// newTestNotificationsRouter — роутер на реальных репозиториях (общий
// postgres-контейнер пакета) с сервисом настроек уведомлений.
func newTestNotificationsRouter(t *testing.T) http.Handler {
	t.Helper()
	pool := newTestDB(t)
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	i18n.MustLoad(i18n.Locales)
	users := repo.NewUsers(pool)
	return New(Deps{
		Auth: auth.NewService(users, repo.NewSessions(pool), auth.Config{
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
		Notifications: notifications.NewService(users, repo.NewMemberships(pool), repo.NewGroups(pool)),
		Users:         users,
		Sessions:      repo.NewSessions(pool),
		Log:           log,
		I18nLoaded:    true,
		SessionTTL:    sessionTTL,
	})
}

// notificationsGroupDTO — элемент groups[] ответа настроек.
type notificationsGroupDTO struct {
	GroupID  int64  `json:"group_id"`
	Slug     string `json:"slug"`
	Title    string `json:"title"`
	DMNotify bool   `json:"dm_notify"`
	Override bool   `json:"override"`
}

// notificationsResp — форма ответа GET/PATCH /api/v1/notifications/settings.
type notificationsResp struct {
	DMNotifyDefault bool                    `json:"dm_notify_default"`
	Groups          []notificationsGroupDTO `json:"groups"`
}

func decodeNotifications(t *testing.T, body []byte) notificationsResp {
	t.Helper()
	var got notificationsResp
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode notifications: %v (%s)", err, body)
	}
	return got
}

// groupFor — настройка конкретной группы из ответа.
func groupFor(t *testing.T, got notificationsResp, groupID int64) notificationsGroupDTO {
	t.Helper()
	for _, g := range got.Groups {
		if g.GroupID == groupID {
			return g
		}
	}
	t.Fatalf("group %d missing from the response: %+v", groupID, got.Groups)
	return notificationsGroupDTO{}
}

// groupPath — /api/v1/notifications/settings?group_id=<id>.
func groupPath(groupID int64) string {
	return "/api/v1/notifications/settings?group_id=" + strconv.FormatInt(groupID, 10)
}

func TestNotificationsGetDefaultsAndGroups(t *testing.T) {
	r := newTestNotificationsRouter(t)
	tok, _ := login(t, r, 4001)
	gid := setupGroupWithAdmin(t, r, tok, "икбо-44-21")

	// Свежий пользователь: дефолт false, единственная группа наследует его.
	resp := doJSON(r, http.MethodGet, "/api/v1/notifications/settings", tok, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("GET settings = %d; body: %s", resp.Code, resp.Body)
	}
	got := decodeNotifications(t, resp.Body.Bytes())
	if got.DMNotifyDefault {
		t.Errorf("dm_notify_default = true, want false")
	}
	g := groupFor(t, got, gid)
	if g.DMNotify || g.Override {
		t.Errorf("group = %+v, want inherited false without override", g)
	}
	if g.Slug == "" || g.Title == "" {
		t.Errorf("group = %+v, want slug and title", g)
	}

	// group_id → ровно эта группа.
	resp = doJSON(r, http.MethodGet, groupPath(gid), tok, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("GET settings?group_id = %d; body: %s", resp.Code, resp.Body)
	}
	single := decodeNotifications(t, resp.Body.Bytes())
	if len(single.Groups) != 1 || single.Groups[0].GroupID != gid {
		t.Fatalf("groups = %+v, want exactly group %d", single.Groups, gid)
	}
}

// Не участник → 404 (не 403: чужие группы не раскрываются).
func TestNotificationsGetForeignGroupNotFound(t *testing.T) {
	r := newTestNotificationsRouter(t)
	ownerTok, _ := login(t, r, 4002)
	gid := setupGroupWithAdmin(t, r, ownerTok, "икбо-44-22")

	otherTok, _ := login(t, r, 4003)
	resp := doJSON(r, http.MethodGet, groupPath(gid), otherTok, nil)
	if resp.Code != http.StatusNotFound {
		t.Fatalf("GET foreign group = %d, want 404; body: %s", resp.Code, resp.Body)
	}
}

// Мусор в group_id → 400.
func TestNotificationsGetBadGroupID(t *testing.T) {
	r := newTestNotificationsRouter(t)
	tok, _ := login(t, r, 4004)
	resp := doJSON(r, http.MethodGet, "/api/v1/notifications/settings?group_id=abc", tok, nil)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("GET bad group_id = %d, want 400; body: %s", resp.Code, resp.Body)
	}
}

func TestNotificationsPatchDefaultAndOverride(t *testing.T) {
	r := newTestNotificationsRouter(t)
	tok, _ := login(t, r, 4005)
	gid := setupGroupWithAdmin(t, r, tok, "икбо-44-25")

	// Без group_id: меняется глобальный дефолт — группа наследует новое значение.
	resp := doJSON(r, http.MethodPatch, "/api/v1/notifications/settings", tok,
		map[string]any{"dm_notify": true})
	if resp.Code != http.StatusOK {
		t.Fatalf("PATCH default = %d; body: %s", resp.Code, resp.Body)
	}
	got := decodeNotifications(t, resp.Body.Bytes())
	if !got.DMNotifyDefault || !groupFor(t, got, gid).DMNotify {
		t.Fatalf("after PATCH default: %+v, want default true and inherited true", got)
	}

	// С group_id: явный override false — группа больше не наследует дефолт.
	resp = doJSON(r, http.MethodPatch, "/api/v1/notifications/settings", tok,
		map[string]any{"group_id": gid, "dm_notify": false})
	if resp.Code != http.StatusOK {
		t.Fatalf("PATCH override = %d; body: %s", resp.Code, resp.Body)
	}
	got = decodeNotifications(t, resp.Body.Bytes())
	g := groupFor(t, got, gid)
	if g.DMNotify || !g.Override {
		t.Fatalf("after override false: group = %+v, want dm_notify=false override=true", g)
	}
	if !got.DMNotifyDefault {
		t.Errorf("dm_notify_default = false, want the global default untouched")
	}

	// dm_notify: null с group_id — override снят, группа снова наследует дефолт.
	resp = doJSON(r, http.MethodPatch, "/api/v1/notifications/settings", tok,
		map[string]any{"group_id": gid, "dm_notify": nil})
	if resp.Code != http.StatusOK {
		t.Fatalf("PATCH clear override = %d; body: %s", resp.Code, resp.Body)
	}
	got = decodeNotifications(t, resp.Body.Bytes())
	g = groupFor(t, got, gid)
	if !g.DMNotify || g.Override {
		t.Fatalf("after clear: group = %+v, want inherited true override=false", g)
	}

	// Перезагрузка GET подтверждает, что состояние сохранилось в БД.
	resp = doJSON(r, http.MethodGet, "/api/v1/notifications/settings", tok, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("GET after PATCH = %d", resp.Code)
	}
	if g := groupFor(t, decodeNotifications(t, resp.Body.Bytes()), gid); !g.DMNotify || g.Override {
		t.Errorf("persisted group = %+v, want inherited true", g)
	}
}

// Пустой патч и мусорное поле → 400; null без group_id → 400.
func TestNotificationsPatchValidation(t *testing.T) {
	r := newTestNotificationsRouter(t)
	tok, _ := login(t, r, 4006)
	gid := setupGroupWithAdmin(t, r, tok, "икбо-44-26")

	cases := []struct {
		name string
		body any
		want int
	}{
		{"empty body", map[string]any{}, http.StatusBadRequest},
		{"unknown field", map[string]any{"dm_notify": true, "wat": 1}, http.StatusBadRequest},
		{"null without group", map[string]any{"dm_notify": nil}, http.StatusBadRequest},
		{"group without dm_notify", map[string]any{"group_id": gid}, http.StatusBadRequest},
		{"garbage dm_notify", map[string]any{"dm_notify": "yes"}, http.StatusBadRequest},
		// Несуществующая группа — тот же путь, что «не участник»: 404.
		{"unknown group", map[string]any{"group_id": gid + 9999, "dm_notify": true}, http.StatusNotFound},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := doJSON(r, http.MethodPatch, "/api/v1/notifications/settings", tok, c.body)
			if resp.Code != c.want {
				t.Fatalf("PATCH %s = %d, want %d; body: %s", c.name, resp.Code, c.want, resp.Body)
			}
		})
	}
}

// Патч чужой группы → 404.
func TestNotificationsPatchForeignGroupNotFound(t *testing.T) {
	r := newTestNotificationsRouter(t)
	ownerTok, _ := login(t, r, 4007)
	gid := setupGroupWithAdmin(t, r, ownerTok, "икбо-44-27")

	otherTok, _ := login(t, r, 4008)
	resp := doJSON(r, http.MethodPatch, "/api/v1/notifications/settings", otherTok,
		map[string]any{"group_id": gid, "dm_notify": true})
	if resp.Code != http.StatusNotFound {
		t.Fatalf("PATCH foreign group = %d, want 404; body: %s", resp.Code, resp.Body)
	}
}

// Эндпоинты за auth-middleware: без токена — 401.
func TestNotificationsRequireAuth(t *testing.T) {
	r := newTestNotificationsRouter(t)
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/notifications/settings"},
		{http.MethodPatch, "/api/v1/notifications/settings"},
	} {
		resp := doJSON(r, c.method, c.path, "", map[string]any{"dm_notify": true})
		if resp.Code != http.StatusUnauthorized {
			t.Errorf("%s %s = %d, want 401", c.method, c.path, resp.Code)
		}
	}
}
