package domain

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestRateLimitErrorWrapsSentinel(t *testing.T) {
	err := &RateLimitError{RetryAfter: 30 * time.Second}
	if !errors.Is(err, ErrRateLimit) {
		t.Errorf("errors.Is(RateLimitError, ErrRateLimit) = false")
	}
	wrapped := fmt.Errorf("telegram: %w", err)
	if !errors.Is(wrapped, ErrRateLimit) {
		t.Errorf("wrapped RateLimitError does not match ErrRateLimit")
	}
}

func TestValidationErrorWrapsSentinel(t *testing.T) {
	err := &ValidationError{Field: "title", Msg: "too long"}
	if !errors.Is(err, ErrValidation) {
		t.Errorf("errors.Is(ValidationError, ErrValidation) = false")
	}
	if err.Error() == "" {
		t.Error("Error() is empty")
	}
}
