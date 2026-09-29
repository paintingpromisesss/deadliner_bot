package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/sauron/deadliner/internal/app/claims"
	"github.com/sauron/deadliner/internal/domain"
	"github.com/sauron/deadliner/internal/i18n"
	"github.com/sauron/deadliner/internal/platform/httpapi/httpjson"
	"github.com/sauron/deadliner/internal/platform/httpapi/middleware"
)

// claimsController — эндпоинты claim-флоу (спека §5.2): старт (код в чат),
// подтверждение (роль admin), отзыв (admin группы).
type claimsController struct {
	svc *claims.Service
}

func newClaimsController(svc *claims.Service) *claimsController {
	return &claimsController{svc: svc}
}

// claimCodeLen — длина кода claim'а: 6 цифр (спека §3.1). Формат проверяется
// до обращения к сервису, чтобы мусор не тратил лимиты чата.
const claimCodeLen = 6

// writeClaimsError — маппинг ошибок claim-флоу: 429 получает Retry-After,
// «нет привязки» / «не отправилось» — 409 с точным текстом, «нет кода» — 404,
// неверный код — 403 (без подсказок, спека §3.1).
func writeClaimsError(w http.ResponseWriter, err error) {
	var rle *domain.RateLimitError
	if errors.As(err, &rle) {
		w.Header().Set("Retry-After", strconv.Itoa(int(rle.RetryAfter.Seconds())+1))
	}
	switch {
	case errors.Is(err, claims.ErrNoBinding):
		httpjson.WriteError(w, http.StatusConflict, "no_chat_binding", i18n.T("api.error.claim_no_binding"))
	case errors.Is(err, claims.ErrCodeSendFailed):
		httpjson.WriteError(w, http.StatusConflict, "claim_code_send_failed", i18n.T("api.error.claim_code_send"))
	case errors.Is(err, claims.ErrWrongCode):
		httpjson.WriteError(w, http.StatusForbidden, "claim_bad_code", i18n.T("api.error.claim_bad_code"))
	case errors.Is(err, claims.ErrCodeNotFound):
		httpjson.WriteError(w, http.StatusNotFound, "claim_code_not_found", i18n.T("api.error.claim_code_expired"))
	default:
		httpjson.WriteDomainError(w, err)
	}
}

// Start — POST /api/v1/groups/{id}/claim/start → 200 {expires_at}.
// Без привязки чата — 409, превышение лимита — 429, не участник — 403.
func (c *claimsController) Start(w http.ResponseWriter, r *http.Request) {
	actor := middleware.UserFrom(r.Context())
	if actor == nil {
		httpjson.WriteUnauthorized(w)
		return
	}
	id, ok := pathGroupID(w, r)
	if !ok {
		return
	}
	res, err := c.svc.StartClaim(r.Context(), actor, id)
	if err != nil {
		writeClaimsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"expires_at": res.ExpiresAt,
		"chat_id":    res.ChatID,
	})
}

// Confirm — POST /api/v1/groups/{id}/claim/confirm {code} → 200 {role:"admin"}.
// Неверный код — 403, истёкший/отсутствующий — 404.
func (c *claimsController) Confirm(w http.ResponseWriter, r *http.Request) {
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
		Code string `json:"code"`
	}
	if err := decodeStrict(r, &req); err != nil || !validClaimCode(req.Code) {
		httpjson.WriteError(w, http.StatusBadRequest, "validation", i18n.T("api.error.validation"))
		return
	}
	g, err := c.svc.Confirm(r.Context(), actor, id, req.Code)
	if err != nil {
		writeClaimsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"role":   string(domain.RoleAdmin),
		"group":  toGroupDTO(g),
		"status": string(g.Status),
	})
}

// Revoke — POST /api/v1/groups/{id}/claim/revoke → 204 (только admin группы).
func (c *claimsController) Revoke(w http.ResponseWriter, r *http.Request) {
	actor := middleware.UserFrom(r.Context())
	if actor == nil {
		httpjson.WriteUnauthorized(w)
		return
	}
	id, ok := pathGroupID(w, r)
	if !ok {
		return
	}
	if err := c.svc.Revoke(r.Context(), actor, id); err != nil {
		writeClaimsError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// validClaimCode — ровно 6 десятичных цифр (ведущие нули значимы).
func validClaimCode(code string) bool {
	if len(code) != claimCodeLen {
		return false
	}
	for _, r := range code {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
