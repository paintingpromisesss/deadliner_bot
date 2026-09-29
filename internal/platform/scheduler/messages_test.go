package scheduler

import (
	"strings"
	"testing"
	"time"

	"github.com/sauron/deadliner/internal/domain"
	"github.com/sauron/deadliner/internal/i18n"
)

func TestPluralizeRu(t *testing.T) {
	cases := []struct {
		n    int
		want string
	}{
		{1, "день"}, {2, "дня"}, {3, "дня"}, {4, "дня"}, {5, "дней"},
		{11, "дней"}, {12, "дней"}, {14, "дней"}, {15, "дней"}, {20, "дней"},
		{21, "день"}, {22, "дня"}, {24, "дня"}, {25, "дней"},
		{100, "дней"}, {101, "день"}, {111, "дней"}, {121, "день"},
		{1, "часа"}, // перезаписывается ниже
	}
	// Последний кейс фиксирует порядок аргументов: (one, few, many).
	cases[len(cases)-1] = struct {
		n    int
		want string
	}{1, "день"}
	for _, c := range cases {
		if got := pluralizeRu(c.n, "день", "дня", "дней"); got != c.want {
			t.Errorf("pluralizeRu(%d) = %q, want %q", c.n, got, c.want)
		}
	}
	// Часы: 1 час, 2 часа, 5 часов, 21 час.
	for n, want := range map[int]string{1: "час", 2: "часа", 5: "часов", 21: "час", 24: "часа"} {
		if got := pluralizeRu(n, "час", "часа", "часов"); got != want {
			t.Errorf("pluralizeRu(%d, часы) = %q, want %q", n, got, want)
		}
	}
	// Отрицательные — модуль.
	if got := pluralizeRu(-3, "день", "дня", "дней"); got != "дня" {
		t.Errorf("pluralizeRu(-3) = %q", got)
	}
}

func TestFormatWhenMSK(t *testing.T) {
	// 2026-09-29 23:59 UTC → 30.09.2026 02:59 (MSK).
	tm := time.Date(2026, 9, 29, 23, 59, 0, 0, time.UTC)
	if got, want := formatWhen(tm, "Europe/Moscow"), "30.09.2026 02:59 (MSK)"; got != want {
		t.Errorf("formatWhen MSK = %q, want %q", got, want)
	}
	// Время уже в нужной зоне.
	loc, _ := time.LoadLocation("Europe/Moscow")
	tm2 := time.Date(2026, 9, 29, 23, 59, 0, 0, loc)
	if got, want := formatWhen(tm2, "Europe/Moscow"), "29.09.2026 23:59 (MSK)"; got != want {
		t.Errorf("formatWhen local = %q, want %q", got, want)
	}
	// Несуществующая зона → UTC.
	if got := formatWhen(tm, "Mars/Olympus"); got != "29.09.2026 23:59 (UTC)" {
		t.Errorf("formatWhen bad tz = %q", got)
	}
}

func TestRelTitleArg(t *testing.T) {
	due := time.Date(2026, 9, 30, 23, 59, 0, 0, time.UTC)
	cases := []struct {
		fire    time.Time
		want    string
		overdue bool
	}{
		{due.Add(-24 * time.Hour), "24 часа", false},
		{due.Add(-3 * 24 * time.Hour), "3 дня", false},
		{due.Add(-7 * 24 * time.Hour), "7 дней", false},
		{due.Add(-21 * 24 * time.Hour), "21 день", false},
		{due.Add(-30 * time.Minute), "30 минут", false},
		{due, "просрочен на менее минуты", true},
		{due.Add(time.Hour), "просрочен на 1 час", true},
	}
	for _, c := range cases {
		if got, od := titleArg(c.fire, due); got != c.want || od != c.overdue {
			t.Errorf("titleArg(%v) = (%q, %v), want (%q, %v)", c.fire, got, od, c.want, c.overdue)
		}
	}
}

func TestGroupMessage(t *testing.T) {
	if err := i18n.Load(i18n.Locales); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = i18n.Load(i18n.Locales) })

	loc, _ := time.LoadLocation("Europe/Moscow")
	due := time.Date(2026, 9, 29, 23, 59, 0, 0, loc)
	dl := domain.Deadline{
		ID: 1, Title: "Курсовая работа по БД", DueAt: due, TZ: "Europe/Moscow",
	}
	rem := domain.Reminder{Kind: domain.KindPreset, FireAt: due.Add(-24 * time.Hour)}
	got := groupMessage(rem, dl, "М8О-401Б-23")
	want := "⏰ <b>Дедлайн через 24 часа</b>\n📌 Курсовая работа по БД — М8О-401Б-23\n🗓 29.09.2026 23:59 (MSK)"
	if got != want {
		t.Errorf("groupMessage =\n%q\nwant\n%q", got, want)
	}
}

func TestDMDupMessage(t *testing.T) {
	if err := i18n.Load(i18n.Locales); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = i18n.Load(i18n.Locales) })

	loc, _ := time.LoadLocation("Europe/Moscow")
	due := time.Date(2026, 9, 29, 23, 59, 0, 0, loc)
	dl := domain.Deadline{ID: 1, Title: "Курсовая", DueAt: due, TZ: "Europe/Moscow"}
	rem := domain.Reminder{Kind: domain.KindDMDup, FireAt: due.Add(-24 * time.Hour)}
	got := dmDupMessage(rem, dl, "ИКБО-33-21")
	want := "⏰ <b>Дедлайн через 24 часа</b> (группа ИКБО-33-21)\n📌 Курсовая — ИКБО-33-21\n🗓 29.09.2026 23:59 (MSK)"
	if got != want {
		t.Errorf("dmDupMessage =\n%q\nwant\n%q", got, want)
	}
}

func TestPersonalMessage(t *testing.T) {
	if err := i18n.Load(i18n.Locales); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = i18n.Load(i18n.Locales) })

	loc, _ := time.LoadLocation("Europe/Moscow")
	due := time.Date(2026, 9, 29, 23, 59, 0, 0, loc)
	dl := domain.Deadline{ID: 1, Title: "Лаба", DueAt: due, TZ: "Europe/Moscow"}
	rem := domain.Reminder{Kind: domain.KindCustomAt, FireAt: due.Add(-24 * time.Hour)}
	got := personalMessage(rem, dl)
	// Личный дедлайн — без сегмента слага (нет висящего « — »).
	want := "⏰ <b>Дедлайн через 24 часа</b>\n📌 Лаба\n🗓 29.09.2026 23:59 (MSK)"
	if got != want {
		t.Errorf("personalMessage =\n%q\nwant\n%q", got, want)
	}
	if strings.Contains(got, " — ") {
		t.Errorf("personalMessage contains dangling dash: %q", got)
	}
}
