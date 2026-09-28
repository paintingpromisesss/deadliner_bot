package i18n

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"unicode/utf8"
)

//go:embed testdata
var testdataFS embed.FS

func TestLoadFixtureAndT(t *testing.T) {
	if err := Load(testdataFS); err != nil {
		t.Fatalf("Load(testdata) = %v", err)
	}
	t.Cleanup(func() { _ = Load(Locales) })

	if got := T("fixture.hello"); got != "Привет, Deadliner!" {
		t.Errorf("T(fixture.hello) = %q", got)
	}
	if got, want := T("fixture.bind", "М8О-401Б-23"), "Чат привязан к группе М8О-401Б-23"; got != want {
		t.Errorf("T(fixture.bind) = %q, want %q", got, want)
	}
	if got, want := T("fixture.args", "a", 42), "a и 42"; got != want {
		t.Errorf("T(fixture.args) = %q, want %q", got, want)
	}
}

func TestMissingKeyReturnsKey(t *testing.T) {
	if err := Load(testdataFS); err != nil {
		t.Fatalf("Load(testdata) = %v", err)
	}
	t.Cleanup(func() { _ = Load(Locales) })

	if got := T("no.such.key"); got != "no.such.key" {
		t.Errorf("T(no.such.key) = %q, want the key itself", got)
	}
	if got := T("no.such.key", "arg"); got != "no.such.key" {
		t.Errorf("T(no.such.key, arg) = %q, want the key itself", got)
	}
}

func TestLoadInvalidJSON(t *testing.T) {
	var bad embed.FS // zero FS: no locales at all
	if err := Load(bad); err == nil {
		t.Error("Load(zero embed.FS) = nil, want error")
	}
}

func TestRealCatalogLoads(t *testing.T) {
	if err := Load(Locales); err != nil {
		t.Fatalf("Load(Locales) = %v", err)
	}

	// Ключи, зафиксированные брифом задачи 5.
	required := []string{
		"bot.start", "bot.help",
		"bot.bind.ok", "bot.bind.conflict_already_bound", "bot.bind.not_group_admin", "bot.bind.unknown_slug",
		"bot.unbind.ok", "bot.groups.empty",
		"reminder.group.title", "reminder.group.body", "reminder.personal.title", "reminder.dm_dup.title",
		"claim.code_message", "claim.success", "claim.revoked",
		"invite.created", "invite.redeemed", "invite.expired",
		"api.error.not_found", "api.error.conflict", "api.error.forbidden", "api.error.rate_limit", "api.error.validation",
		"cleanup.group_deleted",
	}
	for _, k := range required {
		if got := T(k); got == k {
			t.Errorf("key %q missing from ru.json", k)
		}
	}
}

func TestRuJSONValues(t *testing.T) {
	data, err := Locales.ReadFile("locales/ru.json")
	if err != nil {
		t.Fatalf("read ru.json: %v", err)
	}

	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal ru.json: %v", err)
	}
	if len(m) < 20 {
		t.Errorf("ru.json has %d keys, want >= 20", len(m))
	}
	for k, v := range m {
		if strings.TrimSpace(v) == "" {
			t.Errorf("ru.json key %q has empty value", k)
		}
		if !utf8.ValidString(v) {
			t.Errorf("ru.json key %q value is not valid UTF-8", k)
		}
	}

	// json.Unmarshal в map молча дедуплицирует ключи — проверяем дубли
	// подсчётом токенов декодером поверх той же карты.
	keys, err := countTopLevelKeys(data)
	if err != nil {
		t.Fatalf("count keys: %v", err)
	}
	if keys != len(m) {
		t.Errorf("ru.json contains %d raw keys but map has %d — duplicate keys present", keys, len(m))
	}
}

func TestReminderGroupFormat(t *testing.T) {
	if err := Load(Locales); err != nil {
		t.Fatalf("Load(Locales) = %v", err)
	}

	// Спека §6.2: "⏰ <b>Дедлайн через 24 часа</b>" + "📌 Курсовая работа по БД — М8О-401Б-23" + "🗓 29.09.2026 23:59 (MSK)".
	title := T("reminder.group.title", "24 часа")
	if want := "⏰ <b>Дедлайн через 24 часа</b>"; title != want {
		t.Errorf("reminder.group.title = %q, want %q", title, want)
	}
	body := T("reminder.group.body", "Курсовая работа по БД", "М8О-401Б-23", "29.09.2026 23:59", "MSK")
	want := strings.Join([]string{
		"📌 Курсовая работа по БД — М8О-401Б-23",
		"🗓 29.09.2026 23:59 (MSK)",
	}, "\n")
	if body != want {
		t.Errorf("reminder.group.body = %q, want %q", body, want)
	}
}

func countTopLevelKeys(data []byte) (int, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	n := 0
	depth := 0
	expectKey := false
	for {
		tok, err := dec.Token()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return n, nil
			}
			return 0, err
		}
		switch d := tok.(type) {
		case json.Delim:
			switch d {
			case '{':
				depth++
				if depth == 1 {
					expectKey = true
				}
			case '}':
				depth--
			}
		case string:
			// Плоская карта: на глубине 1 токены чередуются ключ/значение.
			if depth == 1 {
				if expectKey {
					n++
				}
				expectKey = !expectKey
			}
		}
	}
}
