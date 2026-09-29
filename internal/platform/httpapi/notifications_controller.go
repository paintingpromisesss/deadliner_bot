package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/sauron/deadliner/internal/app/notifications"
	"github.com/sauron/deadliner/internal/i18n"
	"github.com/sauron/deadliner/internal/platform/httpapi/httpjson"
	"github.com/sauron/deadliner/internal/platform/httpapi/middleware"
)

// notificationsController — настройки уведомлений (спека §5.2): глобальный
// дефолт «дубль в ЛС» и переопределение для конкретной группы. Эффективное
// значение считает сервис (COALESCE(membership.dm_notify, users.dm_notify_default)).
type notificationsController struct {
	svc *notifications.Service
}

func newNotificationsController(svc *notifications.Service) *notificationsController {
	return &notificationsController{svc: svc}
}

// notificationGroupDTO — настройка одной группы: effective-значение и признак
// явного переопределения (override=true: группа не наследует дефолт).
type notificationGroupDTO struct {
	GroupID  int64  `json:"group_id"`
	Slug     string `json:"slug"`
	Title    string `json:"title"`
	DMNotify bool   `json:"dm_notify"`
	Override bool   `json:"override"`
}

// notificationSettingsDTO — тело ответа GET и PATCH (форма одна: PATCH
// возвращает актуальные настройки целиком).
type notificationSettingsDTO struct {
	DMNotifyDefault bool                   `json:"dm_notify_default"`
	Groups          []notificationGroupDTO `json:"groups"`
}

func toNotificationsSettingsDTO(s *notifications.Settings) notificationSettingsDTO {
	groups := make([]notificationGroupDTO, 0, len(s.Groups))
	for _, g := range s.Groups {
		groups = append(groups, notificationGroupDTO{
			GroupID: g.GroupID, Slug: g.Slug, Title: g.Title,
			DMNotify: g.DMNotify, Override: g.Override,
		})
	}
	return notificationSettingsDTO{DMNotifyDefault: s.DMNotifyDefault, Groups: groups}
}

// queryGroupID — необязательный ?group_id= в фильтре GET; отсутствие → nil,
// мусор/неположительное → 400 (ok=false).
func queryGroupID(w http.ResponseWriter, r *http.Request) (*int64, bool) {
	raw := r.URL.Query().Get("group_id")
	if raw == "" {
		return nil, true
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		httpjson.WriteError(w, http.StatusBadRequest, "validation", i18n.T("api.error.validation"))
		return nil, false
	}
	return &id, true
}

// Get — GET /api/v1/notifications/settings?group_id= → 200 {dm_notify_default,
// groups[]}. Без group_id — все группы участника; с group_id — только она
// (404, если пользователь в ней не состоит).
func (c *notificationsController) Get(w http.ResponseWriter, r *http.Request) {
	actor := middleware.UserFrom(r.Context())
	if actor == nil {
		httpjson.WriteUnauthorized(w)
		return
	}
	groupID, ok := queryGroupID(w, r)
	if !ok {
		return
	}
	settings, err := c.svc.Get(r.Context(), actor, groupID)
	if err != nil {
		httpjson.WriteDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toNotificationsSettingsDTO(settings))
}

// patchNotificationsRequest — тело PATCH. dm_notify читается как RawMessage
// (а не *bool): нужно отличать dm_notify:false от dm_notify:null («снять
// переопределение») и от отсутствия поля — при разборе в *bool все три
// выглядят как nil.
type patchNotificationsRequest struct {
	GroupID  *int64          `json:"group_id"`
	DMNotify json.RawMessage `json:"dm_notify"`
}

// dmNotify — разобранное значение dm_notify: ok=false, если поля в теле нет
// (RawMessage тогда nil); value=nil при dm_notify:null; мусор (не true/false/
// null) → err.
func (req *patchNotificationsRequest) dmNotify() (value *bool, ok bool, err error) {
	if len(req.DMNotify) == 0 {
		return nil, false, nil
	}
	if strings.TrimSpace(string(req.DMNotify)) == "null" {
		return nil, true, nil
	}
	var v bool
	if err := json.Unmarshal(req.DMNotify, &v); err != nil {
		return nil, false, err
	}
	return &v, true, nil
}

// Patch — PATCH /api/v1/notifications/settings {group_id?, dm_notify} → 200
// с актуальными настройками. Без group_id меняется users.dm_notify_default;
// с group_id — membership.dm_notify (null снимает переопределение).
func (c *notificationsController) Patch(w http.ResponseWriter, r *http.Request) {
	actor := middleware.UserFrom(r.Context())
	if actor == nil {
		httpjson.WriteUnauthorized(w)
		return
	}
	var req patchNotificationsRequest
	if err := decodeStrict(r, &req); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "validation", i18n.T("api.error.validation"))
		return
	}
	dmNotify, hasDMNotify, err := req.dmNotify()
	if err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "validation", i18n.T("api.error.validation"))
		return
	}
	settings, err := c.svc.Update(r.Context(), actor, notifications.UpdateInput{
		GroupID:     req.GroupID,
		DMNotify:    dmNotify,
		HasDMNotify: hasDMNotify,
	})
	if err != nil {
		httpjson.WriteDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toNotificationsSettingsDTO(settings))
}
