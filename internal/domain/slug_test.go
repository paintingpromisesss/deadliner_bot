package domain

import (
	"errors"
	"testing"
)

func TestNormalize(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{" м8о-401б-23 ", "М8О-401Б-23"},
		{"ё9", "Ё9"},
		{"ab-1", "AB-1"},
		{" икбо 33 21 ", "ИКБО3321"},
		{"", ""},
	}
	for _, c := range cases {
		if got := Normalize(c.raw); got != c.want {
			t.Errorf("Normalize(%q) = %q, want %q", c.raw, got, c.want)
		}
	}
}

// ValidateStrict отвечает за длину, структуру сегментов и цифру; набор
// символов проверяет SLUG_REGEX (см. slugprovider) — поэтому «Ё9» или «Х8!»
// здесь проверяются с настроенной регуляркой, а не с зашитым алфавитом.
func TestValidateStrictAccepts(t *testing.T) {
	valid := []string{"М8О-401Б-23", "ИКБО-33-21", "AB-1", "М8О"}
	for _, s := range valid {
		if err := ValidateStrict(s); err != nil {
			t.Errorf("ValidateStrict(%q) = %v, want nil", s, err)
		}
	}
}

func TestValidateStrictRejects(t *testing.T) {
	invalid := []string{
		"",
		"AB",
		"ASDF",
		"М8О--401",
		"-М8О",
		"М8О-",
		"А1-Б2-В3-Г4-Д5-Е6",
	}
	for _, s := range invalid {
		err := ValidateStrict(s)
		if err == nil {
			t.Errorf("ValidateStrict(%q) = nil, want error", s)
			continue
		}
		if !errors.Is(err, ErrInvalidSlug) {
			t.Errorf("ValidateStrict(%q) = %v, want wrapping ErrInvalidSlug", s, err)
		}
	}
}

// Набор символов — ответственность SLUG_REGEX (local-провайдер), поэтому
// символ вне дефолтного алфавита здесь структуру проходит: домен проверяет
// длину, сегменты и цифру, а не буквы.
func TestValidateStrictIsCharsetAgnostic(t *testing.T) {
	if err := ValidateStrict("ЁЁ9"); err != nil {
		t.Errorf("ValidateStrict(%q) = %v, want nil (charset is SLUG_REGEX's job)", "ЁЁ9", err)
	}
}
