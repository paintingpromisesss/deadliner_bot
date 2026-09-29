package scheduler

import (
	"fmt"
	"strings"
	"time"

	"github.com/sauron/deadliner/internal/domain"
	"github.com/sauron/deadliner/internal/i18n"
)

const (
	keyGroupTitle    = "reminder.group.title"
	keyGroupBody     = "reminder.group.body"
	keyPersonalTitle = "reminder.personal.title"
	keyPersonalBody  = "reminder.personal.body"
	keyDMDupTitle    = "reminder.dm_dup.title"
	keyOverdueTitle  = "reminder.overdue.title"
)

// formatWhen — «29.09.2026 23:59 (MSK)»: локальное время в tz дедлайна
// (спека §6.2). Неверная зона → UTC.
func formatWhen(t time.Time, tz string) string {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.UTC
	}
	local := t.In(loc)
	abbr, _ := local.Zone()
	return fmt.Sprintf("%s (%s)", local.Format("02.01.2006 15:04"), abbr)
}

// pluralizeRu — русская форма числительного: «1 день», «2 дня», «5 дней»,
// «21 день» (n%100 в 11..14 → родительный множественный).
func pluralizeRu(n int, one, few, many string) string {
	nAbs := n
	if nAbs < 0 {
		nAbs = -nAbs
	}
	switch {
	case nAbs%100 >= 11 && nAbs%100 <= 14:
		return many
	case nAbs%10 == 1:
		return one
	case nAbs%10 >= 2 && nAbs%10 <= 4:
		return few
	default:
		return many
	}
}

// titleArg — аргумент заголовка: «24 часа», «3 дня» (в шаблон уже входит
// «через»); при просроченном дедлайне — «просрочен на 1 час» + overdue=true
// (используется ключ reminder.overdue.title).
func titleArg(fireAt, dueAt time.Time) (arg string, overdue bool) {
	d := dueAt.Sub(fireAt)
	if d <= 0 {
		return "просрочен на " + humanDuration(-d), true
	}
	return humanDuration(d), false
}

// humanDuration — грубая человекочитаемая длительность: до 24ч включительно
// — часы (пресет «24 часа» из спеки §6.2), дальше — дни.
func humanDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "менее минуты"
	case d < time.Hour:
		n := int(d.Minutes())
		if n == 0 {
			n = 1
		}
		return fmt.Sprintf("%d %s", n, pluralizeRu(n, "минуту", "минуты", "минут"))
	case d <= 24*time.Hour:
		n := int(d.Hours())
		if n == 0 {
			n = 1
		}
		return fmt.Sprintf("%d %s", n, pluralizeRu(n, "час", "часа", "часов"))
	default:
		n := int(d.Hours() / 24)
		return fmt.Sprintf("%d %s", n, pluralizeRu(n, "день", "дня", "дней"))
	}
}

// body — тело сообщения (строка после заголовка, спека §6.2):
// «📌 title — slug\n🗓 когда». Пустой слаг (личный дедлайн) рендерится
// отдельным шаблоном, чтобы не оставалось висящего « — ».
func body(dl domain.Deadline, slug string) string {
	when := formatWhen(dl.DueAt, dl.TZ)
	datePart, tzPart, _ := strings.Cut(when, " (")
	tz := strings.TrimSuffix(tzPart, ")")
	if slug == "" {
		return i18n.T(keyPersonalBody, dl.Title, datePart, tz)
	}
	return i18n.T(keyGroupBody, dl.Title, slug, datePart, tz)
}

// title — строка заголовка для i18n-ключа kind-зависимого.
func title(fireAt, dueAt time.Time, normalKey string, extraArgs ...any) string {
	arg, overdue := titleArg(fireAt, dueAt)
	if overdue {
		return i18n.T(keyOverdueTitle, arg)
	}
	return i18n.T(normalKey, append([]any{arg}, extraArgs...)...)
}

// groupMessage — напоминание в чат группы (HTML, спека §6.2).
func groupMessage(rem domain.Reminder, dl domain.Deadline, slug string) string {
	return title(rem.FireAt, dl.DueAt, keyGroupTitle) + "\n" + body(dl, slug)
}

// personalMessage — ЛС-напоминание (персональный дедлайн).
func personalMessage(rem domain.Reminder, dl domain.Deadline) string {
	return title(rem.FireAt, dl.DueAt, keyPersonalTitle) + "\n" + body(dl, "")
}

// dmDupMessage — дубли в ЛС (группа): «… (группа %s)».
func dmDupMessage(rem domain.Reminder, dl domain.Deadline, slug string) string {
	return title(rem.FireAt, dl.DueAt, keyDMDupTitle, slug) + "\n" + body(dl, slug)
}
