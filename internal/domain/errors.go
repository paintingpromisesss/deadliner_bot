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
)

type RateLimitError struct {
	RetryAfter time.Duration
}

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("rate limit: retry after %s", e.RetryAfter)
}

func (e *RateLimitError) Unwrap() error { return ErrRateLimit }

type ValidationError struct {
	Field string
	Msg   string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("validation: %s: %s", e.Field, e.Msg)
}

func (e *ValidationError) Unwrap() error { return ErrValidation }
