package telegram

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/go-telegram/bot/models"

	"github.com/sauron/deadliner/internal/domain"
	"github.com/sauron/deadliner/internal/i18n"
)

// fakeSlugReporter — журнал вызовов ReportSlug: права и рассылка живут в use
// case (groups.Service), здесь проверяется маршрутизация команды и ответ.
type fakeSlugReporter struct {
	calls   []slugReportCall
	err     error
	groupID int64
	slug    string
}

type slugReportCall struct {
	actorID  int64
	slug     string
	wasActor bool
}

func (r *fakeSlugReporter) ReportSlug(ctx context.Context, actor *domain.User, slug string) (*domain.Group, error) {
	call := slugReportCall{slug: slug}
	if actor != nil {
		call.actorID, call.wasActor = actor.ID, true
	}
	r.calls = append(r.calls, call)
	if r.err != nil {
		return nil, r.err
	}
	return &domain.Group{ID: r.groupID, Slug: domain.Normalize(slug)}, nil
}

func newReportHarness(me *domain.User) (*harness, *fakeSlugReporter) {
	hs := newHarness()
	hs.users.me = me
	rep := &fakeSlugReporter{groupID: 77}
	hs.h = NewHandlers(HandlersDeps{
		Users: hs.users, Binder: hs.binder, Sender: hs.sender,
		AdminChecker: hs.admin, Reports: rep,
		BotUserID: hs.botID, AppPublicURL: hs.appURL,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return hs, rep
}

// Админ группы пишет /report_slug в ЛС — use case вызывается с нормализованным
// (в use case) слагом, ответ обобщённый.
func TestReportSlugPrivateCallsUseCase(t *testing.T) {
	hs, rep := newReportHarness(&domain.User{ID: 7, TelegramID: 777, FirstName: "Иван"})

	hs.h.Handle(context.Background(), update(777, models.ChatTypePrivate, 777, "ivan", "/report_slug ИКБО-33-21"))

	if len(rep.calls) != 1 {
		t.Fatalf("ReportSlug calls = %d, want 1", len(rep.calls))
	}
	if rep.calls[0].slug != "ИКБО-33-21" {
		t.Errorf("slug = %q, want the raw argument passed through", rep.calls[0].slug)
	}
	if got := hs.sender.last(t).text; got != i18n.T("bot.report.sent") {
		t.Errorf("reply = %q, want bot.report.sent", got)
	}
}

// Суффикс @botname снимается тем же разбором команды, что у остальных.
func TestReportSlugStripsBotMention(t *testing.T) {
	hs, rep := newReportHarness(&domain.User{ID: 7, TelegramID: 777})

	hs.h.Handle(context.Background(),
		update(777, models.ChatTypePrivate, 777, "ivan", "/report_slug@deadliner_bot ИКБО-33-21"))

	if len(rep.calls) != 1 || rep.calls[0].slug != "ИКБО-33-21" {
		t.Fatalf("calls = %+v, want one call with ИКБО-33-21", rep.calls)
	}
}

// В группе команда молча игнорируется: ни вызова use case, ни ответа — иначе
// участники чата узнали бы о существовании команды и о конфликте слагов.
func TestReportSlugInGroupIsSilent(t *testing.T) {
	hs, rep := newReportHarness(&domain.User{ID: 7, TelegramID: 777})

	hs.h.Handle(context.Background(), update(-100500, models.ChatTypeSupergroup, 777, "ivan", "/report_slug ИКБО-33-21"))

	if len(rep.calls) != 0 {
		t.Errorf("ReportSlug calls = %d, want 0 in a group chat", len(rep.calls))
	}
	if len(hs.sender.sent) != 0 {
		t.Errorf("sent = %d messages, want silence in a group chat", len(hs.sender.sent))
	}
}

// Не-админа use case отбивает ErrForbidden — вызывающему уходит ТОТ ЖЕ текст,
// что при успехе: ответ не должен быть оракулом прав и существования слага.
func TestReportSlugNonAdminGetsGenericReply(t *testing.T) {
	hs, rep := newReportHarness(&domain.User{ID: 7, TelegramID: 777})
	rep.err = domain.ErrForbidden

	hs.h.Handle(context.Background(), update(777, models.ChatTypePrivate, 777, "ivan", "/report_slug ИКБО-33-21"))

	if len(rep.calls) != 1 {
		t.Fatalf("ReportSlug calls = %d, want 1", len(rep.calls))
	}
	got := hs.sender.last(t).text
	if got != i18n.T("bot.report.sent") {
		t.Errorf("reply = %q, want the same generic text as on success", got)
	}
}

// Неизвестный слаг — тоже обобщённый ответ.
func TestReportSlugUnknownSlugGetsGenericReply(t *testing.T) {
	hs, rep := newReportHarness(&domain.User{ID: 7, TelegramID: 777})
	rep.err = domain.ErrNotFound

	hs.h.Handle(context.Background(), update(777, models.ChatTypePrivate, 777, "ivan", "/report_slug НЕТ-ТАК-1"))

	if got := hs.sender.last(t).text; got != i18n.T("bot.report.sent") {
		t.Errorf("reply = %q, want bot.report.sent", got)
	}
}

// Прочие сбои (БД, транспорт) — не обобщённый «принято», а generic: пользователь
// не должен думать, что жалоба дошла.
func TestReportSlugUnexpectedFailureIsGeneric(t *testing.T) {
	hs, rep := newReportHarness(&domain.User{ID: 7, TelegramID: 777})
	rep.err = errors.New("db down")

	hs.h.Handle(context.Background(), update(777, models.ChatTypePrivate, 777, "ivan", "/report_slug ИКБО-33-21"))

	got := hs.sender.last(t).text
	if got == i18n.T("bot.report.sent") {
		t.Errorf("reply = %q, want a generic error, not the success text", got)
	}
	if got != i18n.T("bot.error.generic") {
		t.Errorf("reply = %q, want bot.error.generic", got)
	}
}

// Без аргумента — подсказка об использовании, use case не вызывается.
func TestReportSlugWithoutArgumentShowsUsage(t *testing.T) {
	hs, rep := newReportHarness(&domain.User{ID: 7, TelegramID: 777})

	hs.h.Handle(context.Background(), update(777, models.ChatTypePrivate, 777, "ivan", "/report_slug"))

	if len(rep.calls) != 0 {
		t.Errorf("ReportSlug calls = %d, want 0 without an argument", len(rep.calls))
	}
	if got := hs.sender.last(t).text; got != i18n.T("bot.report.usage") {
		t.Errorf("reply = %q, want bot.report.usage", got)
	}
}

// Забаненный вызывающий отсекается до use case (та же логика, что в
// /bind_group: actor из touchUser частичный, is_banned читает гидратация).
func TestReportSlugBannedCallerRejected(t *testing.T) {
	me := &domain.User{ID: 7, TelegramID: 777, IsBanned: true}
	hs, rep := newReportHarness(me)

	hs.h.Handle(context.Background(), update(777, models.ChatTypePrivate, 777, "ivan", "/report_slug ИКБО-33-21"))

	if len(rep.calls) != 0 {
		t.Errorf("ReportSlug calls = %d, want 0 for a banned caller", len(rep.calls))
	}
	if got := hs.sender.last(t).text; got != i18n.T("bot.forbidden") {
		t.Errorf("reply = %q, want bot.forbidden", got)
	}
}

// Сбой гидратации (не «забанен», а «не знаем») — generic, use case не зовётся.
func TestReportSlugHydrationFailureIsGeneric(t *testing.T) {
	hs, rep := newReportHarness(&domain.User{ID: 7, TelegramID: 777})
	hs.users.meErr = errors.New("db down")

	hs.h.Handle(context.Background(), update(777, models.ChatTypePrivate, 777, "ivan", "/report_slug ИКБО-33-21"))

	if len(rep.calls) != 0 {
		t.Errorf("ReportSlug calls = %d, want 0 when the caller cannot be hydrated", len(rep.calls))
	}
	if got := hs.sender.last(t).text; got != i18n.T("bot.error.generic") {
		t.Errorf("reply = %q, want bot.error.generic", got)
	}
}

// Команда публикуется в default-скоупе setMyCommands (как и служебные):
// пользователь должен видеть её в меню личного чата.
func TestReportSlugIsInCommands(t *testing.T) {
	hs := newHarness()
	var found bool
	for _, c := range hs.h.commands() {
		if c.Command == "report_slug" {
			found = true
			if c.Description == "" || strings.HasPrefix(c.Description, "bot.cmd.") {
				t.Errorf("description = %q, want a catalog string", c.Description)
			}
		}
	}
	if !found {
		t.Error("/report_slug missing from setMyCommands")
	}
}