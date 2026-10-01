package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/sauron/deadliner/internal/app/deadlines"
	"github.com/sauron/deadliner/internal/domain"
	"github.com/sauron/deadliner/internal/i18n"
	"github.com/sauron/deadliner/internal/platform/httpapi/httpjson"
	"github.com/sauron/deadliner/internal/platform/httpapi/middleware"
)

// deadlinesController — эндпоинты дедлайнов (спека §5.2).
type deadlinesController struct {
	svc *deadlines.Service
}

func newDeadlinesController(svc *deadlines.Service) *deadlinesController {
	return &deadlinesController{svc: svc}
}

// reminderDTO — напоминание в ответах API.
type reminderDTO struct {
	ID            int64      `json:"id"`
	Kind          string     `json:"kind"`
	OffsetMinutes *int       `json:"offset_minutes,omitempty"`
	FireAt        time.Time  `json:"fire_at"`
	Status        string     `json:"status"`
	SentAt        *time.Time `json:"sent_at,omitempty"`
}

// deadlineDTO — публичное представление дедлайна. created_by нужен клиенту:
// TMA показывает действия записи автору дедлайна, даже если он не админ группы
// (спека §5.2 «автор/admin», backend — requireWrite).
type deadlineDTO struct {
	ID          int64     `json:"id"`
	GroupID     *int64    `json:"group_id,omitempty"`
	OwnerUserID *int64    `json:"owner_user_id,omitempty"`
	CreatedBy   int64     `json:"created_by"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	DueAt       time.Time `json:"due_at"`
	TZ          string    `json:"tz"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func toDeadlineDTO(d *domain.Deadline) deadlineDTO {
	return deadlineDTO{
		ID: d.ID, GroupID: d.GroupID, OwnerUserID: d.OwnerUserID,
		CreatedBy: d.CreatedBy,
		Title:     d.Title, Description: d.Description, DueAt: d.DueAt,
		TZ: d.TZ, Status: string(d.Status), CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt,
	}
}

func toReminderDTOs(list []domain.Reminder) []reminderDTO {
	out := make([]reminderDTO, 0, len(list))
	for _, r := range list {
		out = append(out, reminderDTO{
			ID: r.ID, Kind: string(r.Kind), OffsetMinutes: r.OffsetMinutes,
			FireAt: r.FireAt, Status: string(r.Status), SentAt: r.SentAt,
		})
	}
	return out
}

// writeView — единый ответ {deadline, reminders} для single-deadline операций.
func writeView(w http.ResponseWriter, status int, view *deadlines.View) {
	writeJSON(w, status, map[string]any{
		"deadline":  toDeadlineDTO(view.Deadline),
		"reminders": toReminderDTOs(view.Reminders),
	})
}

// pathDeadlineID — числовой {id} дедлайна из URL; мусор → 400.
func pathDeadlineID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		httpjson.WriteError(w, http.StatusBadRequest, "validation", i18n.T("api.error.validation"))
		return 0, false
	}
	return id, true
}

// reminderRequest — элемент reminders[] в теле POST/PATCH: kind + offset или fire_at.
type reminderRequest struct {
	Kind          string     `json:"kind"`
	OffsetMinutes *int       `json:"offset_minutes"`
	FireAt        *time.Time `json:"fire_at"`
}

func toReminderSpecs(reqs []reminderRequest) []deadlines.ReminderSpec {
	out := make([]deadlines.ReminderSpec, 0, len(reqs))
	for _, rq := range reqs {
		out = append(out, deadlines.ReminderSpec{
			Kind:          domain.ReminderKind(rq.Kind),
			OffsetMinutes: rq.OffsetMinutes,
			FireAt:        rq.FireAt,
		})
	}
	return out
}

// createDeadlineRequest — тело POST /api/v1/deadlines.
type createDeadlineRequest struct {
	GroupID     *int64            `json:"group_id"`
	Title       string            `json:"title"`
	Description string            `json:"description"`
	DueAt       time.Time         `json:"due_at"`
	TZ          string            `json:"tz"`
	Reminders   []reminderRequest `json:"reminders"`
}

// Create — POST /api/v1/deadlines {group_id?, title, description?, due_at,
// tz?, reminders?} → 201 {deadline, reminders}. due_at — RFC3339; мусор → 400.
func (c *deadlinesController) Create(w http.ResponseWriter, r *http.Request) {
	actor := middleware.UserFrom(r.Context())
	if actor == nil {
		httpjson.WriteUnauthorized(w)
		return
	}
	var req createDeadlineRequest
	if err := decodeStrict(r, &req); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "validation", i18n.T("api.error.deadline_bad_request"))
		return
	}
	if req.DueAt.IsZero() {
		httpjson.WriteError(w, http.StatusBadRequest, "validation", i18n.T("api.error.deadline_bad_due_at"))
		return
	}
	view, err := c.svc.Create(r.Context(), actor, deadlines.CreateInput{
		GroupID:     req.GroupID,
		Title:       req.Title,
		Description: req.Description,
		DueAt:       req.DueAt,
		TZ:          req.TZ,
		Reminders:   toReminderSpecs(req.Reminders),
	})
	if err != nil {
		httpjson.WriteDomainError(w, err)
		return
	}
	writeView(w, http.StatusCreated, view)
}

// Get — GET /api/v1/deadlines/{id} → 200 {deadline, reminders}.
func (c *deadlinesController) Get(w http.ResponseWriter, r *http.Request) {
	actor := middleware.UserFrom(r.Context())
	if actor == nil {
		httpjson.WriteUnauthorized(w)
		return
	}
	id, ok := pathDeadlineID(w, r)
	if !ok {
		return
	}
	view, err := c.svc.Get(r.Context(), actor, id)
	if err != nil {
		httpjson.WriteDomainError(w, err)
		return
	}
	writeView(w, http.StatusOK, view)
}

// patchDeadlineRequest — тело PATCH /api/v1/deadlines/{id}; nil-поле = «не трогать».
type patchDeadlineRequest struct {
	Title       *string    `json:"title"`
	Description *string    `json:"description"`
	DueAt       *time.Time `json:"due_at"`
	TZ          *string    `json:"tz"`
}

// Update — PATCH /api/v1/deadlines/{id} → 200 {deadline, reminders};
// смена due_at перегенерирует reminders (спека §7.1).
func (c *deadlinesController) Update(w http.ResponseWriter, r *http.Request) {
	actor := middleware.UserFrom(r.Context())
	if actor == nil {
		httpjson.WriteUnauthorized(w)
		return
	}
	id, ok := pathDeadlineID(w, r)
	if !ok {
		return
	}
	var req patchDeadlineRequest
	if err := decodeStrict(r, &req); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "validation", i18n.T("api.error.deadline_bad_request"))
		return
	}
	view, err := c.svc.Update(r.Context(), actor, id, deadlines.UpdateInput{
		Title:       req.Title,
		Description: req.Description,
		DueAt:       req.DueAt,
		TZ:          req.TZ,
	})
	if err != nil {
		httpjson.WriteDomainError(w, err)
		return
	}
	writeView(w, http.StatusOK, view)
}

// Delete — DELETE /api/v1/deadlines/{id} → 204 (soft delete + cancel pending).
func (c *deadlinesController) Delete(w http.ResponseWriter, r *http.Request) {
	actor := middleware.UserFrom(r.Context())
	if actor == nil {
		httpjson.WriteUnauthorized(w)
		return
	}
	id, ok := pathDeadlineID(w, r)
	if !ok {
		return
	}
	if err := c.svc.Delete(r.Context(), actor, id); err != nil {
		httpjson.WriteDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Complete — POST /api/v1/deadlines/{id}/complete → 200 {deadline, reminders};
// статус done, pending-напоминания отменяются.
func (c *deadlinesController) Complete(w http.ResponseWriter, r *http.Request) {
	actor := middleware.UserFrom(r.Context())
	if actor == nil {
		httpjson.WriteUnauthorized(w)
		return
	}
	id, ok := pathDeadlineID(w, r)
	if !ok {
		return
	}
	view, err := c.svc.Complete(r.Context(), actor, id)
	if err != nil {
		httpjson.WriteDomainError(w, err)
		return
	}
	writeView(w, http.StatusOK, view)
}

// parseListQuery — общие query-фильтры списков: from/to (RFC3339), status.
// Мусор в любом поле → ok=false (400 уже записан).
func parseListQuery(w http.ResponseWriter, r *http.Request) (from, to *time.Time, status *domain.DeadlineStatus, ok bool) {
	q := r.URL.Query()
	if v := q.Get("from"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			httpjson.WriteError(w, http.StatusBadRequest, "validation", i18n.T("api.error.deadline_bad_due_at"))
			return nil, nil, nil, false
		}
		from = &t
	}
	if v := q.Get("to"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			httpjson.WriteError(w, http.StatusBadRequest, "validation", i18n.T("api.error.deadline_bad_due_at"))
			return nil, nil, nil, false
		}
		to = &t
	}
	if v := q.Get("status"); v != "" {
		st := domain.DeadlineStatus(v)
		switch st {
		case domain.DeadlineStatusActive, domain.DeadlineStatusDone, domain.DeadlineStatusArchived:
			status = &st
		default:
			httpjson.WriteError(w, http.StatusBadRequest, "validation", i18n.T("api.error.deadline_bad_status"))
			return nil, nil, nil, false
		}
	}
	return from, to, status, true
}

// ListGroup — GET /api/v1/groups/{id}/deadlines?from=&to=&status= → 200 [deadline].
func (c *deadlinesController) ListGroup(w http.ResponseWriter, r *http.Request) {
	actor := middleware.UserFrom(r.Context())
	if actor == nil {
		httpjson.WriteUnauthorized(w)
		return
	}
	id, ok := pathGroupID(w, r)
	if !ok {
		return
	}
	from, to, status, ok := parseListQuery(w, r)
	if !ok {
		return
	}
	list, err := c.svc.ListGroup(r.Context(), actor, id, deadlines.ListQuery{From: from, To: to, Status: status})
	if err != nil {
		httpjson.WriteDomainError(w, err)
		return
	}
	writeDeadlineList(w, list)
}

// ListMine — GET /api/v1/me/deadlines?from=&to=&status=&scope=all → 200 [deadline];
// scope=all добавляет групповые дедлайны из всех membership'ов пользователя.
func (c *deadlinesController) ListMine(w http.ResponseWriter, r *http.Request) {
	actor := middleware.UserFrom(r.Context())
	if actor == nil {
		httpjson.WriteUnauthorized(w)
		return
	}
	from, to, status, ok := parseListQuery(w, r)
	if !ok {
		return
	}
	scope := r.URL.Query().Get("scope")
	if scope != "" && scope != "all" {
		httpjson.WriteError(w, http.StatusBadRequest, "validation", i18n.T("api.error.validation"))
		return
	}
	list, err := c.svc.ListMine(r.Context(), actor, deadlines.ListQuery{
		From: from, To: to, Status: status, Scope: scope,
	})
	if err != nil {
		httpjson.WriteDomainError(w, err)
		return
	}
	writeDeadlineList(w, list)
}

func writeDeadlineList(w http.ResponseWriter, list []domain.Deadline) {
	out := make([]deadlineDTO, 0, len(list))
	for i := range list {
		out = append(out, toDeadlineDTO(&list[i]))
	}
	writeJSON(w, http.StatusOK, out)
}
