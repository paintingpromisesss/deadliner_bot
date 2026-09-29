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
	"github.com/sauron/deadliner/internal/app/claims"
	"github.com/sauron/deadliner/internal/app/deadlines"
	"github.com/sauron/deadliner/internal/app/groups"
	"github.com/sauron/deadliner/internal/domain"
	"github.com/sauron/deadliner/internal/platform/httpapi/httpjson"
	"github.com/sauron/deadliner/internal/platform/httpapi/middleware"
)

// Deps — зависимости роутера; последующие задачи добавляют сюда свои поля.
type Deps struct {
	Auth       *auth.Service
	Groups     *groups.Service
	Deadlines  *deadlines.Service
	Claims     *claims.Service
	Users      domain.UserRepo
	Sessions   domain.SessionRepo
	Log        *slog.Logger
	I18nLoaded bool
	SessionTTL time.Duration
	// WebhookHandler — POST /webhook бота в webhook-режиме (Task 10:
	// telegram.Bot.WebhookHandler()). nil в polling-режиме.
	WebhookHandler http.Handler
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
			r.Use(middleware.Auth(middleware.AuthDeps{
				Sessions: d.Sessions,
				Users:    d.Users,
				Clock:    domain.SystemClock{},
				Log:      log,
				TTL:      d.SessionTTL,
			}))
			r.Get("/me", authCtl.Me)
			r.Patch("/me", authCtl.PatchMe)
			r.Post("/me/logout", authCtl.Logout)

			if d.Groups != nil {
				groupsCtl := newGroupsController(d.Groups)
				r.Get("/groups", groupsCtl.ListOrSearch)
				r.Post("/groups", groupsCtl.Create)
				r.Get("/groups/{id}", groupsCtl.Get)
				r.Patch("/groups/{id}", groupsCtl.Update)
				r.Delete("/groups/{id}", groupsCtl.Delete)
				r.Post("/groups/{id}/invites", groupsCtl.CreateInvite)
				r.Delete("/groups/{id}/invites/{code}", groupsCtl.RevokeInvite)
				r.Get("/groups/{id}/members", groupsCtl.ListMembers)
				r.Patch("/groups/{id}/members/{user_id}", groupsCtl.SetMemberRole)
				r.Delete("/groups/{id}/members/{user_id}", groupsCtl.KickMember)
				r.Delete("/groups/{id}/me", groupsCtl.Leave)
				r.Post("/invites/redeem", groupsCtl.RedeemInvite)
			}

			if d.Deadlines != nil {
				deadlinesCtl := newDeadlinesController(d.Deadlines)
				r.Post("/deadlines", deadlinesCtl.Create)
				r.Get("/deadlines/{id}", deadlinesCtl.Get)
				r.Patch("/deadlines/{id}", deadlinesCtl.Update)
				r.Delete("/deadlines/{id}", deadlinesCtl.Delete)
				r.Post("/deadlines/{id}/complete", deadlinesCtl.Complete)
				r.Get("/groups/{id}/deadlines", deadlinesCtl.ListGroup)
				r.Get("/me/deadlines", deadlinesCtl.ListMine)
			}

			if d.Claims != nil {
				claimsCtl := newClaimsController(d.Claims)
				r.Post("/groups/{id}/claim/start", claimsCtl.Start)
				r.Post("/groups/{id}/claim/confirm", claimsCtl.Confirm)
				r.Post("/groups/{id}/claim/revoke", claimsCtl.Revoke)
			}
		})
	})

	// POST /webhook — приём апдейтов Telegram в webhook-режиме (Task 16
	// передаёт сюда telegram.Bot.WebhookHandler). Вне /api/v1: это не REST API
	// TMA, а канал Telegram, и секрет проверяется библиотекой.
	if d.WebhookHandler != nil {
		r.Post("/webhook", d.WebhookHandler.ServeHTTP)
	}

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
