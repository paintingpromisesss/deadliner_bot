// Package httpapi — REST API для TMA (спека §5): chi-роутер, middleware,
// JSON-контроллеры. Формат ошибок: {"error":{"code":…,"message":…}}.
package httpapi

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	"github.com/sauron/deadliner/internal/app/auth"
	"github.com/sauron/deadliner/internal/domain"
	"github.com/sauron/deadliner/internal/platform/httpapi/httpjson"
	"github.com/sauron/deadliner/internal/platform/httpapi/middleware"
)

// Deps — зависимости роутера; последующие задачи добавляют сюда свои поля.
type Deps struct {
	Auth       *auth.Service
	Users      domain.UserRepo
	Sessions   domain.SessionRepo
	Log        *slog.Logger
	I18nLoaded bool
	SessionTTL time.Duration
}

// New собирает HTTP-роутер приложения.
func New(d Deps) chi.Router {
	log := d.Log
	if log == nil {
		log = slog.Default()
	}

	r := chi.NewRouter()
	r.Use(chimw.RequestID)
	r.Use(chimw.RealIP)
	r.Use(middleware.RequestLogger(log))
	r.Use(chimw.Recoverer)

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok"))
	})

	authCtl := newAuthController(d.Auth, d.Users, d.Sessions, d.SessionTTL)

	r.Route("/api/v1", func(r chi.Router) {
		r.Post("/auth/telegram", authCtl.Telegram)

		r.Group(func(r chi.Router) {
			r.Use(middleware.Auth(d.Sessions, d.Users, d.SessionTTL))
			r.Get("/me", authCtl.Me)
			r.Patch("/me", authCtl.PatchMe)
			r.Post("/me/logout", authCtl.Logout)
		})
	})

	return r
}

// WriteError пишет ошибку в стандартном конверте {"error":{code,message}}.
// Реализация — в httpjson (общий пакет без циклов импортов с middleware).
func WriteError(w http.ResponseWriter, status int, code, msg string) {
	httpjson.WriteError(w, status, code, msg)
}

// WriteDomainError сопоставляет доменные ошибки-сентинелы с HTTP-статусами
// и локализованными сообщениями (i18n-каталог).
func WriteDomainError(w http.ResponseWriter, err error) {
	httpjson.WriteDomainError(w, err)
}
