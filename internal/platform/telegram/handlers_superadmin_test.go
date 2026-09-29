package telegram

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/go-telegram/bot/models"

	"github.com/sauron/deadliner/internal/domain"
	"github.com/sauron/deadliner/internal/i18n"
)

// fakeSuperadmin — журнал вызовов служебных операций; ошибки задаются полями,
// чтобы проверки шли без БД.
type fakeSuperadmin struct {
	mu sync.Mutex

	promoted []int64
	banned   []int64
	unbanned []int64
	deleted  []string
	stats    domain.Stats
	// actorIDs — user_id актора, дошедший до сервиса (гидратированный, а не
	// частичный из upsert).
	actorIDs []int64

	promoteErr error
	banErr     error
	unbanErr   error
	deleteErr  error
	statsErr   error
}

func (f *fakeSuperadmin) PromoteSuperadmin(ctx context.Context, actor *domain.User, telegramID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.actorIDs = append(f.actorIDs, actor.ID)
	f.promoted = append(f.promoted, telegramID)
	return f.promoteErr
}

func (f *fakeSuperadmin) BanUser(ctx context.Context, actor *domain.User, telegramID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.actorIDs = append(f.actorIDs, actor.ID)
	f.banned = append(f.banned, telegramID)
	return f.banErr
}

func (f *fakeSuperadmin) UnbanUser(ctx context.Context, actor *domain.User, telegramID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.actorIDs = append(f.actorIDs, actor.ID)
	f.unbanned = append(f.unbanned, telegramID)
	return f.unbanErr
}

func (f *fakeSuperadmin) DeleteGroup(ctx context.Context, actor *domain.User, slug string) (*domain.Group, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.actorIDs = append(f.actorIDs, actor.ID)
	f.deleted = append(f.deleted, slug)
	if f.deleteErr != nil {
		return nil, f.deleteErr
	}
	return &domain.Group{ID: 1, Slug: domain.Normalize(slug), Title: "Моя группа"}, nil
}

func (f *fakeSuperadmin) Stats(ctx context.Context, actor *domain.User) (domain.Stats, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.actorIDs = append(f.actorIDs, actor.ID)
	return f.stats, f.statsErr
}

// newSuperadminHarness — общий harness (handlers_bind_test.go) плюс служебные
// зависимости: users.me задаёт гидратированного вызывающего.
func newSuperadminHarness(me *domain.User) (*harness, *fakeSuperadmin) {
	hs := newHarness()
	sa := &fakeSuperadmin{}
	hs.users.me = me
	hs.h = NewHandlers(HandlersDeps{
		Users: hs.users, Binder: hs.binder, Sender: hs.sender,
		AdminChecker: hs.admin, Superadmin: sa,
		BotUserID: hs.botID, AppPublicURL: hs.appURL,
	}, hs.h.log)
	return hs, sa
}

func superadminUser(id, telegramID int64) *domain.User {
	return &domain.User{ID: id, TelegramID: telegramID, IsSuperadmin: true}
}

// --- guard ---

// Не-супер-админ в ЛС получает явный отказ, действие не выполняется.
func TestSuperadminOnlyGuard(t *testing.T) {
	hs, sa := newSuperadminHarness(&domain.User{ID: 7, TelegramID: 777})

	for _, cmd := range []string{"/promote 555", "/ban 555", "/unban 555", "/stats", "/delete_group ИКБО-33-21"} {
		hs.h.Handle(context.Background(), update(500, models.ChatTypePrivate, 777, "ivan", cmd))
		got := hs.sender.last(t)
		if got.text != i18n.T("superadmin.only") {
			t.Errorf("%s: text = %q, want superadmin.only", cmd, got.text)
		}
	}
	if len(sa.promoted)+len(sa.banned)+len(sa.unbanned)+len(sa.deleted) != 0 {
		t.Errorf("service calls = %+v, want none for a non-superadmin", sa)
	}
}

// Забаненный супер-админ не проходит guard: флаг бана проверяется после
// гидратации (бан выдаётся только не-супер-админам, но проверка бесплатна).
func TestSuperadminBannedCallerRejected(t *testing.T) {
	me := superadminUser(1, 777)
	me.IsBanned = true
	hs, sa := newSuperadminHarness(me)

	hs.h.Handle(context.Background(), update(500, models.ChatTypePrivate, 777, "ivan", "/stats"))
	if got := hs.sender.last(t); got.text != i18n.T("superadmin.only") {
		t.Errorf("text = %q, want superadmin.only", got.text)
	}
	if len(sa.actorIDs) != 0 {
		t.Errorf("service called for a banned caller: %+v", sa.actorIDs)
	}
}

// Неизвестный в БД вызывающий (GetByTelegramID → ErrNotFound) — тоже отказ,
// без паники и без обращения к сервису.
func TestSuperadminUnknownCallerIsRejected(t *testing.T) {
	hs, sa := newSuperadminHarness(nil) // me == nil → ErrNotFound
	hs.h.Handle(context.Background(), update(500, models.ChatTypePrivate, 777, "ivan", "/stats"))

	if got := hs.sender.last(t); got.text != i18n.T("bot.error.generic") {
		t.Errorf("text = %q, want bot.error.generic", got.text)
	}
	if len(sa.actorIDs) != 0 {
		t.Errorf("service called for an unknown user: %+v", sa.actorIDs)
	}
}

// В группе служебные команды игнорируются молча: ответ «только супер-админу»
// в общем чате раскрывал бы существование служебных команд.
func TestSuperadminCommandsSilentInGroups(t *testing.T) {
	hs, sa := newSuperadminHarness(superadminUser(1, 777))

	for _, cmd := range []string{"/promote 555", "/ban 555", "/unban 555", "/stats", "/delete_group ИКБО-33-21"} {
		hs.h.Handle(context.Background(), update(-100500, models.ChatTypeSupergroup, 777, "ivan", cmd))
	}
	if len(hs.sender.sent) != 0 {
		t.Errorf("sent = %+v, want silence in group chats", hs.sender.sent)
	}
	if len(sa.actorIDs) != 0 {
		t.Errorf("service calls = %+v, want none in group chats", sa.actorIDs)
	}
}

// --- actions ---

func TestSuperadminPromote(t *testing.T) {
	hs, sa := newSuperadminHarness(superadminUser(1, 777))
	hs.h.Handle(context.Background(), update(500, models.ChatTypePrivate, 777, "ivan", "/promote 555"))

	if len(sa.promoted) != 1 || sa.promoted[0] != 555 {
		t.Errorf("promoted = %v, want [555]", sa.promoted)
	}
	// Сервис получает ГИДРАТИРОВАННОГО актора (id=1), а не частичного из upsert.
	if len(sa.actorIDs) != 1 || sa.actorIDs[0] != 1 {
		t.Errorf("actor ids = %v, want [1]", sa.actorIDs)
	}
	if got := hs.sender.last(t); !strings.Contains(got.text, "555") {
		t.Errorf("text = %q, want the telegram id", got.text)
	}
}

// Команда с суффиксом @botname и лишними пробелами.
func TestSuperadminPromoteWithBotSuffix(t *testing.T) {
	hs, sa := newSuperadminHarness(superadminUser(1, 777))
	hs.h.Handle(context.Background(), update(500, models.ChatTypePrivate, 777, "ivan", "/promote@DeadlinerBot  555 "))

	if len(sa.promoted) != 1 || sa.promoted[0] != 555 {
		t.Errorf("promoted = %v, want [555]", sa.promoted)
	}
}

func TestSuperadminBanAndUnban(t *testing.T) {
	hs, sa := newSuperadminHarness(superadminUser(1, 777))
	hs.h.Handle(context.Background(), update(500, models.ChatTypePrivate, 777, "ivan", "/ban 555"))
	if len(sa.banned) != 1 || sa.banned[0] != 555 {
		t.Errorf("banned = %v, want [555]", sa.banned)
	}
	if got := hs.sender.last(t); !strings.Contains(got.text, "555") {
		t.Errorf("ban reply = %q", got.text)
	}

	hs.h.Handle(context.Background(), update(500, models.ChatTypePrivate, 777, "ivan", "/unban 555"))
	if len(sa.unbanned) != 1 || sa.unbanned[0] != 555 {
		t.Errorf("unbanned = %v, want [555]", sa.unbanned)
	}
}

// Бан несуществующего/самого себя (доменный ErrNotFound/ErrForbidden) —
// конкретный текст, не generic.
func TestSuperadminBanErrors(t *testing.T) {
	hs, sa := newSuperadminHarness(superadminUser(1, 777))

	sa.banErr = fmt.Errorf("%w: user telegram_id=555", domain.ErrNotFound)
	hs.h.Handle(context.Background(), update(500, models.ChatTypePrivate, 777, "ivan", "/ban 555"))
	if got := hs.sender.last(t); got.text != i18n.T("superadmin.error.not_found") {
		t.Errorf("not-found text = %q", got.text)
	}

	sa.banErr = fmt.Errorf("%w: cannot ban a superadmin", domain.ErrForbidden)
	hs.h.Handle(context.Background(), update(500, models.ChatTypePrivate, 777, "ivan", "/ban 555"))
	if got := hs.sender.last(t); got.text != i18n.T("superadmin.error.ban_self") {
		t.Errorf("forbidden text = %q", got.text)
	}

	sa.banErr = errors.New("db down")
	hs.h.Handle(context.Background(), update(500, models.ChatTypePrivate, 777, "ivan", "/ban 555"))
	if got := hs.sender.last(t); got.text != i18n.T("bot.error.generic") {
		t.Errorf("transient error text = %q, want bot.error.generic", got.text)
	}
}

func TestSuperadminDeleteGroup(t *testing.T) {
	hs, sa := newSuperadminHarness(superadminUser(1, 777))
	hs.h.Handle(context.Background(), update(500, models.ChatTypePrivate, 777, "ivan", "/delete_group икбо-33-21"))

	if len(sa.deleted) != 1 || sa.deleted[0] != "икбо-33-21" {
		t.Errorf("deleted = %v, want the raw slug (normalization belongs to the service)", sa.deleted)
	}
	got := hs.sender.last(t)
	if !strings.Contains(got.text, "ИКБО-33-21") {
		t.Errorf("text = %q, want the normalized slug", got.text)
	}

	sa.deleteErr = fmt.Errorf("%w: group slug_norm=НЕТ", domain.ErrNotFound)
	hs.h.Handle(context.Background(), update(500, models.ChatTypePrivate, 777, "ivan", "/delete_group НЕТ-1-1"))
	if got := hs.sender.last(t); got.text != i18n.T("superadmin.error.not_found") {
		t.Errorf("not-found text = %q", got.text)
	}
}

// /stats печатает все восемь счётчиков (спека §6.1 + §7.2 п.4: failed видно).
func TestSuperadminStatsRendersNumbers(t *testing.T) {
	hs, sa := newSuperadminHarness(superadminUser(1, 777))
	sa.statsFixture()
	hs.h.Handle(context.Background(), update(500, models.ChatTypePrivate, 777, "ivan", "/stats"))

	got := hs.sender.last(t)
	for _, want := range []string{"42", "7", "3", "4", "11", "9", "2", "5"} {
		if !strings.Contains(got.text, want) {
			t.Errorf("stats text %q missing %q", got.text, want)
		}
	}
	if strings.Contains(got.text, "%!") {
		t.Errorf("stats text has a format artifact: %q", got.text)
	}
	if !strings.Contains(got.text, i18n.T("superadmin.stats",
		"42", "7", "3", "4", "11", "9", "2", "5")) {
		t.Errorf("stats text = %q, want the rendered template", got.text)
	}
}

func (f *fakeSuperadmin) statsFixture() {
	f.stats = domain.Stats{
		Users: 42, GroupsTotal: 7, GroupsActive: 3, GroupsPending: 4,
		DeadlinesActive: 11, RemindersPending: 9, RemindersFailed: 2, SessionsActive: 5,
	}
}

// --- args ---

// Нечисловой/пустой аргумент → подсказка об использовании, сервис не вызван.
func TestSuperadminBadArgs(t *testing.T) {
	cases := []struct {
		text string
		want string
	}{
		{"/promote", i18n.T("superadmin.usage_promote")},
		{"/promote иван", i18n.T("superadmin.usage_promote")},
		{"/promote 12abc", i18n.T("superadmin.usage_promote")},
		{"/promote 0", i18n.T("superadmin.usage_promote")},
		{"/ban", i18n.T("superadmin.usage_user", "ban", "ban")},
		{"/ban -5", i18n.T("superadmin.usage_user", "ban", "ban")},
		{"/unban abc", i18n.T("superadmin.usage_user", "unban", "unban")},
		{"/delete_group", i18n.T("superadmin.usage_delete_group")},
	}
	for _, c := range cases {
		t.Run(c.text, func(t *testing.T) {
			hs, sa := newSuperadminHarness(superadminUser(1, 777))
			hs.h.Handle(context.Background(), update(500, models.ChatTypePrivate, 777, "ivan", c.text))

			got := hs.sender.last(t)
			if got.text != c.want {
				t.Errorf("text = %q, want %q", got.text, c.want)
			}
			if len(sa.actorIDs) != 0 {
				t.Errorf("service called with bad args: %+v", sa.actorIDs)
			}
		})
	}
}

// Плюс перед числом допускается (пользователи копируют «+123456» из клиента).
func TestParseTelegramIDArg(t *testing.T) {
	cases := map[string]struct {
		want int64
		ok   bool
	}{
		"555":        {555, true},
		"+555":       {555, true},
		" 555 ":      {555, true},
		"0":          {0, false},
		"":           {0, false},
		"-555":       {0, false},
		"5.5":        {0, false},
		"555a":       {0, false},
		"9999999999": {9999999999, true},
	}
	for in, want := range cases {
		got, ok := parseTelegramIDArg(in)
		if ok != want.ok || got != want.want {
			t.Errorf("parseTelegramIDArg(%q) = (%d, %v), want (%d, %v)", in, got, ok, want.want, want.ok)
		}
	}
}

// /stats без Superadmin-сервиса (nil) отвечает generic-ошибкой, а не паникует.
func TestSuperadminWithoutService(t *testing.T) {
	hs := newHarness()
	hs.users.me = superadminUser(1, 777)
	hs.h.Handle(context.Background(), update(500, models.ChatTypePrivate, 777, "ivan", "/stats"))

	if got := hs.sender.last(t); got.text != i18n.T("bot.error.generic") {
		t.Errorf("text = %q, want bot.error.generic", got.text)
	}
}

// Служебные команды не попадают в scope all_chat_administrators (там они
// бессмысленны: права выдаёт владелец инстанса, а не администратор чата).
func TestSuperadminCommandsNotInChatAdminScope(t *testing.T) {
	for _, cmd := range []string{"promote", "ban", "unban", "stats", "delete_group"} {
		if isAdminCommand(cmd) {
			t.Errorf("isAdminCommand(%q) = true, want false", cmd)
		}
	}
	for _, cmd := range []string{"bind_group", "unbind"} {
		if !isAdminCommand(cmd) {
			t.Errorf("isAdminCommand(%q) = false, want true", cmd)
		}
	}
}
