package domain

import (
	"fmt"
	"strings"
	"unicode"
)

// DefaultSlugRegex — значение SLUG_REGEX по умолчанию (спека §8): вузовский
// формат «КИРИЛЛИЦА/ЛАТИНИЦА/ЦИФРЫ, сегменты через один дефис». Константа
// домена, а не литерал в config: одну и ту же строку обязаны видеть
// config.Load (дефолт env) и local-провайдер слага (в тестах — тот же дефолт).
const DefaultSlugRegex = `^[А-ЯA-Z0-9]+(-[А-ЯA-Z0-9]+)*$`

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

// ValidateStrict проверяет НОРМАЛИЗОВАННЫЙ слаг по правилам Deadliner поверх
// настроенного SLUG_REGEX (спека §3.3, §8): длину 3..16 рун, непустые
// сегменты, разделённые одиночным дефисом, и анти-спам правило «минимум одна
// цифра» (М8О-401Б-23, ИКБО-33-21).
//
// Набор символов здесь НЕ проверяется: он целиком принадлежит SLUG_REGEX,
// который применяет local-провайдер (internal/platform/slugprovider) перед
// этим вызовом. Иначе оператор, сузивший или расширивший charset в
// SLUG_REGEX, не получил бы эффекта — ровно этот дефект (мёртвая настройка)
// и устраняется разделением ответственности. Правила ниже — собственные
// добавления Deadliner: они действуют при ЛЮБОМ SLUG_REGEX.
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