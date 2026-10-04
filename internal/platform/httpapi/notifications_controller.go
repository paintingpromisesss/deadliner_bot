package httpapi

import (
	"encoding/json"
	"fmt"
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

// queryGroupID — необязательный ?group_id= в фильтре GET: отсутствие ключа →
// nil; пустое/нечисловое/неположительное значение → 400.
func queryGroupID(w http.ResponseWriter, r *http.Request) (*int64, bool) {
	vals, present := r.URL.Query()["group_id"]
	if !present {
		return nil, true
	}
	id, err := parseGroupID(vals[0])
	if err != nil {
		writeValidationError(w)
		return nil, false
	}
	return &id, true
}

// parseGroupID — положительный числовой идентификатор группы: id <= 0 и мусор
// неотличимы для клиента (такой группы не существует).
func parseGroupID(raw string) (int64, error) {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || !validGroupID(id) {
		return 0, fmt.Errorf("invalid group_id %q", raw)
	}
	return id, nil
}

// validGroupID — один критерий валидности id для query и тела: неположительный
// id → 400 до обращения к БД, а не 404.
func validGroupID(id int64) bool { return id > 0 }

func writeValidationError(w http.ResponseWriter) {
	httpjson.WriteError(w, http.StatusBadRequest, "validation", i18n.T("api.error.validation"))
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
		writeValidationError(w)
		return
	}
	// group_id валидируется ДО обращения к членству: id <= 0 — невалидный
	// параметр (400), как и в GET, а не «нет доступа к группе» (404).
	if req.GroupID != nil && !validGroupID(*req.GroupID) {
		writeValidationError(w)
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
