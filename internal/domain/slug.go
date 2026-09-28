package domain

import (
	"fmt"
	"strings"
	"unicode"
)

func Normalize(raw string) string {
	s := strings.TrimFunc(raw, unicode.IsSpace)
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ToUpper(s)
	return strings.ReplaceAll(s, "ё", "Ё")
}

const (
	slugMinLen = 3
	slugMaxLen = 16
)

// ValidateStrict checks the NORMALIZED slug: charset А-ЯA-Z0-9 and '-',
// non-empty single-hyphen-separated segments, length 3..16 runes, and the
// university-slug rule — at least one digit (М8О-401Б-23, ИКБО-33-21).
func ValidateStrict(s string) error {
	if n := len([]rune(s)); n < slugMinLen || n > slugMaxLen {
		return fmt.Errorf("%w: length must be 3..16, got %d", ErrInvalidSlug, n)
	}
	hasDigit := false
	for _, seg := range strings.Split(s, "-") {
		if seg == "" {
			return fmt.Errorf("%w: empty segment", ErrInvalidSlug)
		}
		for _, r := range seg {
			switch {
			case r >= 'А' && r <= 'Я', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			default:
				return fmt.Errorf("%w: invalid character %q", ErrInvalidSlug, r)
			}
			if r >= '0' && r <= '9' {
				hasDigit = true
			}
		}
	}
	if !hasDigit {
		return fmt.Errorf("%w: must contain at least one digit", ErrInvalidSlug)
	}
	return nil
}
