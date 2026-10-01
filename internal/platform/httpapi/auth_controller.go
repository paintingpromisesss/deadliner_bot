package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sauron/deadliner/internal/app/auth"
	"github.com/sauron/deadliner/internal/domain"
	"github.com/sauron/deadliner/internal/i18n"
	"github.com/sauron/deadliner/internal/platform/httpapi/httpjson"
	"github.com/sauron/deadliner/internal/platform/httpapi/middleware"
)

// authController — эндпоинты авторизации и профиля (спека §5.2).
type authController struct {
	auth     *auth.Service
	users    domain.UserRepo
	sessions domain.SessionRepo
	ttl      time.Duration
}

func newAuthController(a *auth.Service, users domain.UserRepo, sessions domain.SessionRepo, ttl time.Duration) *authController {
	return &authController{auth: a, users: users, sessions: sessions, ttl: ttl}
}

// userDTO — публичное представление пользователя в ответах API.
type userDTO struct {
	ID              int64  `json:"id"`
	TelegramID      int64  `json:"telegram_id"`
	Username        string `json:"username"`
	FirstName       string `json:"first_name"`
	TZ              string `json:"tz"`
	DMNotifyDefault bool   `json:"dm_notify_default"`
	IsSuperadmin    bool   `json:"is_superadmin"`
}

func toUserDTO(u *domain.User) userDTO {
	return userDTO{
		ID:              u.ID,
		TelegramID:      u.TelegramID,
		Username:        u.Username,
		FirstName:       u.FirstName,
		TZ:              u.TZ,
		DMNotifyDefault: u.DMNotifyDefault,
		IsSuperadmin:    u.IsSuperadmin,
	}
}

// Telegram — POST /api/v1/auth/telegram {initData} → {token, user}.
func (c *authController) Telegram(w http.ResponseWriter, r *http.Request) {
	var req struct {
		InitData string `json:"initData"`
	}
	if err := decodeStrict(r, &req); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "validation", i18n.T("api.error.validation"))
		return
	}
	if req.InitData == "" {
		httpjson.WriteError(w, http.StatusBadRequest, "validation", i18n.T("api.error.validation"))
		return
	}

	user, token, err := c.auth.Login(r.Context(), req.InitData)
	if err != nil {
		httpjson.WriteDomainError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"token": token,
		"user":  toUserDTO(user),
	})
}

// Me — GET /api/v1/me → профиль текущего пользователя.
func (c *authController) Me(w http.ResponseWriter, r *http.Request) {
	u := middleware.UserFrom(r.Context())
	if u == nil {
		httpjson.WriteUnauthorized(w)
		return
	}
	writeJSON(w, http.StatusOK, toUserDTO(u))
}

// patchMeRequest — тело PATCH /api/v1/me; неизвестные поля → 400.
// first_name — из спеки §5.2: имя меняется тем же запросом, что tz и
// dm_notify_default (отдельного PATCH /me/profile нет).
type patchMeRequest struct {
	TZ              *string `json:"tz"`
	DMNotifyDefault *bool   `json:"dm_notify_default"`
	FirstName       *string `json:"first_name"`
}

// maxFirstNameRunes — граница имени (как у title дедлайна): имя показывается в
// списках участников и в тексте жалобы, бесконтрольная длина ломала бы вёрстку.
const maxFirstNameRunes = 64

// PatchMe — PATCH /api/v1/me {tz?, dm_notify_default?, first_name?} →
// обновлённый профиль. Пустой first_name — валидационная ошибка: сброс имени
// оставил бы пользователя безымянным в списках участников (имя из initData
// приходит всегда).
func (c *authController) PatchMe(w http.ResponseWriter, r *http.Request) {
	u := middleware.UserFrom(r.Context())
	if u == nil {
		httpjson.WriteUnauthorized(w)
		return
	}

	var req patchMeRequest
	if err := decodeStrict(r, &req); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "validation", i18n.T("api.error.validation"))
		return
	}

	tz := u.TZ
	if req.TZ != nil {
		if _, err := time.LoadLocation(*req.TZ); err != nil {
			httpjson.WriteError(w, http.StatusBadRequest, "validation", i18n.T("api.error.validation"))
			return
		}
		tz = *req.TZ
	}
	dm := u.DMNotifyDefault
	if req.DMNotifyDefault != nil {
		dm = *req.DMNotifyDefault
	}
	firstName := u.FirstName
	if req.FirstName != nil {
		name := strings.TrimSpace(*req.FirstName)
		if n := utf8.RuneCountInString(name); n < 1 || n > maxFirstNameRunes {
			httpjson.WriteError(w, http.StatusBadRequest, "validation", i18n.T("api.error.first_name"))
			return
		}
		firstName = name
	}

	if err := c.users.UpdateSettings(r.Context(), u.ID, tz, dm); err != nil {
		httpjson.WriteDomainError(w, err)
		return
	}
	if req.FirstName != nil {
		// Второй UPDATE только когда имя реально пришло: UpdateSettings —
		// основной путь настроек, и лишняя запись на каждый PATCH /me была бы
		// платой за редкий случай.
		if err := c.users.UpdateProfile(r.Context(), u.ID, firstName); err != nil {
			httpjson.WriteDomainError(w, err)
			return
		}
	}

	updated, err := c.users.GetByID(r.Context(), u.ID)
	if err != nil {
		httpjson.WriteDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toUserDTO(updated))
}

// Logout — POST /api/v1/me/logout → 204, токен отзывается.
func (c *authController) Logout(w http.ResponseWriter, r *http.Request) {
	sess := middleware.SessionFrom(r.Context())
	if sess == nil {
		httpjson.WriteUnauthorized(w)
		return
	}
	if err := c.sessions.Revoke(r.Context(), sess.TokenHash); err != nil {
		if !errors.Is(err, domain.ErrNotFound) {
			httpjson.WriteDomainError(w, err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// decodeStrict декодирует JSON, отклоняя неизвестные поля (spec: 400 ValidationError).
func decodeStrict(r *http.Request, dst any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
