package domain

import (
	"errors"
	"fmt"
	"time"
)

var (
	ErrNotFound    = errors.New("not found")
	ErrConflict    = errors.New("conflict")
	ErrForbidden   = errors.New("forbidden")
	ErrRateLimit   = errors.New("rate limit")
	ErrInvalidSlug = errors.New("invalid slug")
	ErrValidation  = errors.New("validation failed")
	// ErrBotBlocked: пользователь заблокировал бота (403 от Telegram на ЛС) —
	// dm_dup fan-out больше не должен создавать для него детей (спека §7.3).
	ErrBotBlocked = errors.New("bot blocked")
)

type RateLimitError struct {
	RetryAfter time.Duration
}

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("rate limit: retry after %s", e.RetryAfter)
}

func (e *RateLimitError) Unwrap() error { return ErrRateLimit }

// BotBlockedError — отправка в ЛС не удалась, потому что пользователь
// заблокировал бота. Notifier возвращает её из SendToUser; воркер карантинит
// reminder на неделю (дешёвый ретрай), use case-ы могут звать
// UserRepo.MarkBotBlocked.
type BotBlockedError struct {
	UserID int64
}

func (e *BotBlockedError) Error() string {
	return fmt.Sprintf("bot blocked by user %d", e.UserID)
}

func (e *BotBlockedError) Unwrap() error { return ErrBotBlocked }

type ValidationError struct {
	Field string
	Msg   string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("validation: %s: %s", e.Field, e.Msg)
}

func (e *ValidationError) Unwrap() error { return ErrValidation }
