package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/sauron/deadliner/internal/app/groups"
	"github.com/sauron/deadliner/internal/domain"
	"github.com/sauron/deadliner/internal/i18n"
	"github.com/sauron/deadliner/internal/platform/httpapi/httpjson"
	"github.com/sauron/deadliner/internal/platform/httpapi/middleware"
)

// groupsController — эндпоинты групп, участников и инвайтов (спека §5.2).
type groupsController struct {
	svc *groups.Service
}

func newGroupsController(svc *groups.Service) *groupsController {
	return &groupsController{svc: svc}
}

// groupDTO — публичное представление группы; пресеты — в минутах (формат БД).
type groupDTO struct {
	ID             int64      `json:"id"`
	Slug           string     `json:"slug"`
	Title          string     `json:"title"`
	Status         string     `json:"status"`
	Official       bool       `json:"official"`
	CreatedBy      int64      `json:"created_by"`
	DefaultPresets []int64    `json:"default_presets"`
	ClaimExpiresAt *time.Time `json:"claim_expires_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

func toGroupDTO(g *domain.Group) groupDTO {
	presets := make([]int64, 0, len(g.DefaultPresets))
	for _, p := range g.DefaultPresets {
		presets = append(presets, int64(p/time.Minute))
	}
	return groupDTO{
		ID: g.ID, Slug: g.Slug, Title: g.Title, Status: string(g.Status),
		Official: g.Official, CreatedBy: g.CreatedBy, DefaultPresets: presets,
		ClaimExpiresAt: g.ClaimExpiresAt, CreatedAt: g.CreatedAt, UpdatedAt: g.UpdatedAt,
	}
}

// writeGroupsError — маппинг доменных ошибок на HTTP-статусы: 429 получает
// заголовок Retry-After, ErrInvalidSlug и ErrLastAdmin — свои сообщения.
func writeGroupsError(w http.ResponseWriter, err error) {
	var rle *domain.RateLimitError
	if errors.As(err, &rle) {
		w.Header().Set("Retry-After", strconv.Itoa(int(rle.RetryAfter/time.Second)+1))
	}
	switch {
	case errors.Is(err, domain.ErrInvalidSlug):
		httpjson.WriteError(w, http.StatusBadRequest, "slug_invalid", i18n.T("api.error.slug_invalid"))
	case errors.Is(err, groups.ErrLastAdmin):
		httpjson.WriteError(w, http.StatusConflict, "last_admin", i18n.T("api.error.last_admin"))
	case errors.Is(err, groups.ErrNoBinding):
		httpjson.WriteError(w, http.StatusConflict, "no_chat_binding", i18n.T("api.error.invite_no_binding"))
	case errors.Is(err, groups.ErrPublishFailed):
		httpjson.WriteError(w, http.StatusConflict, "invite_publish_failed", i18n.T("api.error.invite_publish_failed"))
	default:
		httpjson.WriteDomainError(w, err)
	}
}

// pathGroupID — числовой ID группы из URL (bigint); мусор → 400.
func pathGroupID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		httpjson.WriteError(w, http.StatusBadRequest, "validation", i18n.T("api.error.validation"))
		return 0, false
	}
	return id, true
}

// Create — POST /api/v1/groups {slug,title} → 201 {group}.
func (c *groupsController) Create(w http.ResponseWriter, r *http.Request) {
	actor := middleware.UserFrom(r.Context())
	if actor == nil {
		httpjson.WriteUnauthorized(w)
		return
	}
	var req struct {
		Slug  string `json:"slug"`
		Title string `json:"title"`
	}
	if err := decodeStrict(r, &req); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "validation", i18n.T("api.error.validation"))
		return
	}
	g, err := c.svc.Create(r.Context(), actor, req.Slug, req.Title)
	if err != nil {
		writeGroupsError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"group": toGroupDTO(g)})
}

// ListOrSearch — GET /api/v1/groups: с ?q= поиск по префиксу, без q — мои
// группы. Единая форма ответа: [{group, role}]; role="" для не-участника.
func (c *groupsController) ListOrSearch(w http.ResponseWriter, r *http.Request) {
	actor := middleware.UserFrom(r.Context())
	if actor == nil {
		httpjson.WriteUnauthorized(w)
		return
	}
	q := r.URL.Query().Get("q")
	var mine []groups.MyGroup
	var err error
	if q == "" {
		mine, err = c.svc.ListMine(r.Context(), actor)
	} else {
		mine, err = c.svc.Search(r.Context(), actor, q)
	}
	if err != nil {
		writeGroupsError(w, err)
		return
	}
	out := make([]map[string]any, 0, len(mine))
	for _, mg := range mine {
		dto := toGroupDTO(&mg.Group)
		out = append(out, map[string]any{"group": dto, "role": string(mg.Role)})
	}
	writeJSON(w, http.StatusOK, out)
}

// bindingDTO — привязка чата в деталях группы (nil, если чат не привязан).
type bindingDTO struct {
	ChatID          int64  `json:"chat_id"`
	MessageThreadID *int64 `json:"message_thread_id,omitempty"`
	ChatTitle       string `json:"chat_title"`
}

// Get — GET /api/v1/groups/{id} → {group, role, binding, members_count}.
func (c *groupsController) Get(w http.ResponseWriter, r *http.Request) {
	actor := middleware.UserFrom(r.Context())
	if actor == nil {
		httpjson.WriteUnauthorized(w)
		return
	}
	id, ok := pathGroupID(w, r)
	if !ok {
		return
	}
	view, err := c.svc.Get(r.Context(), actor, id)
	if err != nil {
		writeGroupsError(w, err)
		return
	}
	resp := map[string]any{
		"group":         toGroupDTO(view.Group),
		"role":          string(view.Role),
		"binding":       nil,
		"members_count": view.MembersCount,
	}
	if view.Binding != nil {
		resp["binding"] = bindingDTO{
			ChatID:          view.Binding.ChatID,
			MessageThreadID: view.Binding.MessageThreadID,
			ChatTitle:       view.Binding.ChatTitle,
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// patchGroupRequest — тело PATCH /api/v1/groups/{id}; пресеты — в минутах.
type patchGroupRequest struct {
	Title          *string  `json:"title"`
	DefaultPresets *[]int64 `json:"default_presets"`
}

// Update — PATCH /api/v1/groups/{id} {title?, default_presets?} → 200 {group}.
func (c *groupsController) Update(w http.ResponseWriter, r *http.Request) {
	actor := middleware.UserFrom(r.Context())
	if actor == nil {
		httpjson.WriteUnauthorized(w)
		return
	}
	id, ok := pathGroupID(w, r)
	if !ok {
		return
	}
	var req patchGroupRequest
	if err := decodeStrict(r, &req); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "validation", i18n.T("api.error.validation"))
		return
	}

	var presets *[]time.Duration
	if req.DefaultPresets != nil {
		ps := make([]time.Duration, 0, len(*req.DefaultPresets))
		for _, m := range *req.DefaultPresets {
			if m > maxPresetMinutes {
				httpjson.WriteError(w, http.StatusBadRequest, "validation", i18n.T("api.error.validation"))
				return
			}
			ps = append(ps, time.Duration(m)*time.Minute)
		}
		presets = &ps
	}
	g, err := c.svc.Update(r.Context(), actor, id, req.Title, presets)
	if err != nil {
		writeGroupsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"group": toGroupDTO(g)})
}

// Delete — DELETE /api/v1/groups/{id} → 204 (soft delete).
func (c *groupsController) Delete(w http.ResponseWriter, r *http.Request) {
	actor := middleware.UserFrom(r.Context())
	if actor == nil {
		httpjson.WriteUnauthorized(w)
		return
	}
	id, ok := pathGroupID(w, r)
	if !ok {
		return
	}
	if err := c.svc.Delete(r.Context(), actor, id); err != nil {
		writeGroupsError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// maxPresetMinutes — граница защиты от переполнения Duration: 10 лет в минутах.
const maxPresetMinutes = int64(10 * 365 * 24 * 60)

// inviteTTLNone — сигнальное значение ttl_hours для бессрочного инвайта.
const inviteTTLNone = -1

// maxTTLHours — верхняя граница TTL инвайта: 90 дней.
const maxTTLHours = 24 * 90

// CreateInvite — POST /api/v1/groups/{id}/invites
// {role, max_uses, ttl_hours, publish_to_chat} → 201 {code, expires_at};
// plaintext-код возвращается один раз.
// max_uses: -1 = без лимита (дефолт), ≥1 — число использований; 0 и < -1 → 400.
// ttl_hours: -1 = бессрочный; null/отсутствует → дефолт конфига; 0 запрещён,
// > 2160 → 400. publish_to_chat: бот отправляет призыв в привязанный чат
// (без привязки — 409).
func (c *groupsController) CreateInvite(w http.ResponseWriter, r *http.Request) {
	actor := middleware.UserFrom(r.Context())
	if actor == nil {
		httpjson.WriteUnauthorized(w)
		return
	}
	id, ok := pathGroupID(w, r)
	if !ok {
		return
	}
	var req struct {
		Role          string `json:"role"`
		MaxUses       *int   `json:"max_uses"`
		TTLHours      *int   `json:"ttl_hours"`
		PublishToChat bool   `json:"publish_to_chat"`
	}
	if err := decodeStrict(r, &req); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "validation", i18n.T("api.error.validation"))
		return
	}
	if req.Role == "" {
		req.Role = string(domain.RoleMember)
	}
	maxUses := -1
	if req.MaxUses != nil {
		maxUses = *req.MaxUses
	}
	// Поле отсутствует → inviteTTLNone (-1) → дефолт конфига; явный -1 в
	// запросе читается однозначно как «бессрочный».
	ttlHours := inviteTTLNone
	if req.TTLHours != nil {
		ttlHours = *req.TTLHours
	}
	if maxUses == 0 || maxUses < -1 || ttlHours == 0 || ttlHours < -1 || ttlHours > maxTTLHours {
		httpjson.WriteError(w, http.StatusBadRequest, "validation", i18n.T("api.error.validation"))
		return
	}
	// В сервис: 0 — бессрочный; < 0 — дефолт конфига.
	var ttl time.Duration
	switch {
	case ttlHours == inviteTTLNone:
		ttl = -1 // дефолт
	case ttlHours > 0:
		ttl = time.Duration(ttlHours) * time.Hour
	}
	code, inv, err := c.svc.CreateInvite(r.Context(), actor, id,
		domain.Role(req.Role), maxUses, ttl, req.PublishToChat)
	if err != nil {
		writeGroupsError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"code":       code,
		"expires_at": inv.ExpiresAt,
		"published":  req.PublishToChat && err == nil,
	})
}

// RedeemInvite — POST /api/v1/invites/redeem {code} → 200 {group}.
func (c *groupsController) RedeemInvite(w http.ResponseWriter, r *http.Request) {
	actor := middleware.UserFrom(r.Context())
	if actor == nil {
		httpjson.WriteUnauthorized(w)
		return
	}
	var req struct {
		Code string `json:"code"`
	}
	if err := decodeStrict(r, &req); err != nil || req.Code == "" {
		httpjson.WriteError(w, http.StatusBadRequest, "validation", i18n.T("api.error.validation"))
		return
	}
	g, err := c.svc.RedeemInvite(r.Context(), actor, req.Code)
	if err != nil {
		writeGroupsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"group": toGroupDTO(g)})
}

// InvitePreview — GET /api/v1/invites/{code} → 200 {group}: данные экрана
// «Вступить в группу?» при открытии Mini App по startapp. Лимит не
// расходуется: списание — только фактом «Вступить».
func (c *groupsController) InvitePreview(w http.ResponseWriter, r *http.Request) {
	actor := middleware.UserFrom(r.Context())
	if actor == nil {
		httpjson.WriteUnauthorized(w)
		return
	}
	code := chi.URLParam(r, "code")
	if code == "" {
		httpjson.WriteError(w, http.StatusBadRequest, "validation", i18n.T("api.error.validation"))
		return
	}
	g, err := c.svc.InvitePreview(r.Context(), code)
	if err != nil {
		writeGroupsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"group": toGroupDTO(g)})
}

// RevokeInvite — DELETE /api/v1/groups/{id}/invites/{code} → 204.
func (c *groupsController) RevokeInvite(w http.ResponseWriter, r *http.Request) {
	actor := middleware.UserFrom(r.Context())
	if actor == nil {
		httpjson.WriteUnauthorized(w)
		return
	}
	id, ok := pathGroupID(w, r)
	if !ok {
		return
	}
	code := chi.URLParam(r, "code")
	if code == "" {
		httpjson.WriteError(w, http.StatusBadRequest, "validation", i18n.T("api.error.validation"))
		return
	}
	if err := c.svc.RevokeInvite(r.Context(), actor, id, code); err != nil {
		writeGroupsError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type inviteDTO struct {
	ID        int64      `json:"id"`
	GroupID   int64      `json:"group_id"`
	Code      string     `json:"code"`
	Role      string     `json:"role"`
	MaxUses   int        `json:"max_uses"`
	UsedCount int        `json:"used_count"`
	CreatedBy int64      `json:"created_by"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
	Status    string     `json:"status"`
}

func inviteStatus(inv domain.Invite, now time.Time) string {
	if inv.RevokedAt != nil {
		return "revoked"
	}
	if !inv.ExpiresAt.IsZero() && inv.ExpiresAt.Before(now) {
		return "expired"
	}
	if inv.MaxUses >= 0 && inv.UsedCount >= inv.MaxUses {
		return "exhausted"
	}
	return "active"
}

// ListInvites — GET /api/v1/groups/{id}/invites → 200 {invites: []}.
func (c *groupsController) ListInvites(w http.ResponseWriter, r *http.Request) {
	actor := middleware.UserFrom(r.Context())
	if actor == nil {
		httpjson.WriteUnauthorized(w)
		return
	}
	id, ok := pathGroupID(w, r)
	if !ok {
		return
	}
	invs, err := c.svc.ListInvites(r.Context(), actor, id)
	if err != nil {
		writeGroupsError(w, err)
		return
	}
	now := time.Now().UTC()
	out := make([]inviteDTO, 0, len(invs))
	for _, inv := range invs {
		var exp *time.Time
		if !inv.ExpiresAt.IsZero() {
			exp = &inv.ExpiresAt
		}
		out = append(out, inviteDTO{
			ID:        inv.ID,
			GroupID:   inv.GroupID,
			Code:      inv.Code,
			Role:      string(inv.Role),
			MaxUses:   inv.MaxUses,
			UsedCount: inv.UsedCount,
			CreatedBy: inv.CreatedBy,
			ExpiresAt: exp,
			RevokedAt: inv.RevokedAt,
			Status:    inviteStatus(inv, now),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"invites": out})
}

// memberDTO — участник в списке группы.
type memberDTO struct {
	UserID    int64     `json:"user_id"`
	Username  string    `json:"username"`
	FirstName string    `json:"first_name"`
	Role      string    `json:"role"`
	JoinedAt  time.Time `json:"joined_at"`
}

// ListMembers — GET /api/v1/groups/{id}/members → полный список участников.
func (c *groupsController) ListMembers(w http.ResponseWriter, r *http.Request) {
	actor := middleware.UserFrom(r.Context())
	if actor == nil {
		httpjson.WriteUnauthorized(w)
		return
	}
	id, ok := pathGroupID(w, r)
	if !ok {
		return
	}
	mems, err := c.svc.ListMembers(r.Context(), actor, id)
	if err != nil {
		writeGroupsError(w, err)
		return
	}
	out := make([]memberDTO, 0, len(mems))
	for _, m := range mems {
		out = append(out, memberDTO{
			UserID: m.UserID, Username: m.Username, FirstName: m.FirstName,
			Role: string(m.Role), JoinedAt: m.JoinedAt,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// pathUserID — числовой {user_id} из URL; мусор → 400.
func pathUserID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "user_id"), 10, 64)
	if err != nil || id <= 0 {
		httpjson.WriteError(w, http.StatusBadRequest, "validation", i18n.T("api.error.validation"))
		return 0, false
	}
	return id, true
}

// SetMemberRole — PATCH /api/v1/groups/{id}/members/{user_id} {role} → 200.
func (c *groupsController) SetMemberRole(w http.ResponseWriter, r *http.Request) {
	actor := middleware.UserFrom(r.Context())
	if actor == nil {
		httpjson.WriteUnauthorized(w)
		return
	}
	id, ok := pathGroupID(w, r)
	if !ok {
		return
	}
	userID, ok := pathUserID(w, r)
	if !ok {
		return
	}
	var req struct {
		Role string `json:"role"`
	}
	if err := decodeStrict(r, &req); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "validation", i18n.T("api.error.validation"))
		return
	}
	if err := c.svc.SetRole(r.Context(), actor, id, userID, domain.Role(req.Role)); err != nil {
		writeGroupsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user_id": userID, "role": req.Role})
}

// KickMember — DELETE /api/v1/groups/{id}/members/{user_id} → 204.
func (c *groupsController) KickMember(w http.ResponseWriter, r *http.Request) {
	actor := middleware.UserFrom(r.Context())
	if actor == nil {
		httpjson.WriteUnauthorized(w)
		return
	}
	id, ok := pathGroupID(w, r)
	if !ok {
		return
	}
	userID, ok := pathUserID(w, r)
	if !ok {
		return
	}
	if err := c.svc.RemoveMember(r.Context(), actor, id, userID); err != nil {
		writeGroupsError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Leave — DELETE /api/v1/groups/{id}/me → 204.
func (c *groupsController) Leave(w http.ResponseWriter, r *http.Request) {
	actor := middleware.UserFrom(r.Context())
	if actor == nil {
		httpjson.WriteUnauthorized(w)
		return
	}
	id, ok := pathGroupID(w, r)
	if !ok {
		return
	}
	if err := c.svc.Leave(r.Context(), actor, id); err != nil {
		writeGroupsError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
