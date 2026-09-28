// Package httpjson — общий конверт ошибок REST API (спека §5):
// {"error":{"code":…,"message":…}}. Используется роутером, middleware
// и контроллерами; вынесен отдельно, чтобы middleware и httpapi не
// образовывали цикл импортов.
package httpjson

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/sauron/deadliner/internal/domain"
	"github.com/sauron/deadliner/internal/i18n"
)

// WriteError пишет ошибку в стандартном конверте с указанным HTTP-статусом.
func WriteError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(envelope{Error: body{Code: code, Message: msg}})
}

// WriteDomainError сопоставляет доменные ошибки-сентинелы с HTTP-статусами
// и локализованными сообщениями (i18n-каталог).
func WriteDomainError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		WriteError(w, http.StatusNotFound, "not_found", i18n.T("api.error.not_found"))
	case errors.Is(err, domain.ErrForbidden):
		WriteError(w, http.StatusForbidden, "forbidden", i18n.T("api.error.forbidden"))
	case errors.Is(err, domain.ErrConflict):
		WriteError(w, http.StatusConflict, "conflict", i18n.T("api.error.conflict"))
	case errors.Is(err, domain.ErrValidation):
		WriteError(w, http.StatusBadRequest, "validation", i18n.T("api.error.validation"))
	case errors.Is(err, domain.ErrRateLimit):
		WriteError(w, http.StatusTooManyRequests, "rate_limit", i18n.T("api.error.rate_limit"))
	default:
		WriteError(w, http.StatusInternalServerError, "internal", i18n.T("api.error.internal"))
	}
}

// WriteUnauthorized — 401 с локализованным сообщением.
func WriteUnauthorized(w http.ResponseWriter) {
	WriteError(w, http.StatusUnauthorized, "unauthorized", i18n.T("api.error.unauthorized"))
}

type envelope struct {
	Error body `json:"error"`
}

type body struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
