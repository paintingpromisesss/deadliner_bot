// Package middleware — HTTP-middleware приложения: request logger и
// аутентификация по Bearer-токену сессии.
package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/sauron/deadliner/internal/app/auth"
	"github.com/sauron/deadliner/internal/domain"
	"github.com/sauron/deadliner/internal/platform/httpapi/httpjson"
)

type ctxKey int

const (
	userKey ctxKey = iota
	sessionKey
)

// UserFrom возвращает аутентифицированного пользователя из контекста запроса.
func UserFrom(ctx context.Context) *domain.User {
	u, _ := ctx.Value(userKey).(*domain.User)
	return u
}

// SessionFrom возвращает сессию текущего запроса (после Auth).
func SessionFrom(ctx context.Context) *domain.Session {
	s, _ := ctx.Value(sessionKey).(*domain.Session)
	return s
}

// touchInterval — как часто продлевается сессия при активности (sliding TTL).
const touchInterval = time.Hour

// Auth проверяет Authorization: Bearer <token>: сессия должна быть активной,
// пользователь — существовать. В контекст кладутся *domain.User и *domain.Session.
// При активности реже раза в час сессия продлевается на ttl (sliding renewal).
func Auth(sessions domain.SessionRepo, users domain.UserRepo, ttl time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw := bearerToken(r)
			if raw == "" {
				httpjson.WriteUnauthorized(w)
				return
			}

			now := time.Now().UTC()
			tokenHash := auth.HashToken(raw)
			sess, err := sessions.GetActive(r.Context(), tokenHash, now)
			if err != nil {
				httpjson.WriteUnauthorized(w)
				return
			}
			user, err := users.GetByID(r.Context(), sess.UserID)
			if err != nil {
				httpjson.WriteUnauthorized(w)
				return
			}
			if user.IsBanned {
				httpjson.WriteDomainError(w, domain.ErrForbidden)
				return
			}

			// Sliding renewal: продлеваем не чаще раза в час, одним UPDATE.
			if now.Sub(sess.LastSeen) >= touchInterval {
				_ = sessions.Touch(r.Context(), tokenHash, now, now.Add(ttl))
			}

			ctx := context.WithValue(r.Context(), sessionKey, sess)
			ctx = context.WithValue(ctx, userKey, user)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(h) > len(prefix) && strings.EqualFold(h[:len(prefix)], prefix) {
		return strings.TrimSpace(h[len(prefix):])
	}
	return ""
}

// RequestLogger логирует каждый запрос через slog: method, path, status,
// duration, request_id.
func RequestLogger(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			log.Info("http request",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", ww.Status()),
				slog.Duration("duration", time.Since(start)),
				slog.String("request_id", middleware.GetReqID(r.Context())),
			)
		})
	}
}
