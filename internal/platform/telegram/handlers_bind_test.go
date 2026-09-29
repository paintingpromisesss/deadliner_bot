package telegram

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/sauron/deadliner/internal/app/groups"
	"github.com/sauron/deadliner/internal/domain"
	"github.com/sauron/deadliner/internal/i18n"
)

// --- fakes ---

type sentMessage struct {
	chatID   int64
	threadID *int64
	text     string
	button   string
}

// fakeMsgSender — фейковый транспорт (implements both Sender and
// MessageSender, как BotSender); имя отличается от fakeSender из
// notifier_test.go, где проверяется голый минимальный Sender.
type fakeMsgSender struct {
	sent []sentMessage
	err  error
}

// SendMessage — минимальный контракт (fallback-путь без кнопки).
func (s *fakeMsgSender) SendMessage(ctx context.Context, chatID int64, threadID *int64, text string, linkPreviewOff bool) (int64, error) {
	return s.Send(ctx, OutMessage{ChatID: chatID, ThreadID: threadID, Text: text, LinkPreviewOff: linkPreviewOff})
}

func (s *fakeMsgSender) Send(ctx context.Context, m OutMessage) (int64, error) {
	if s.err != nil {
		return 0, s.err
	}
	s.sent = append(s.sent, sentMessage{chatID: m.ChatID, threadID: m.ThreadID, text: m.Text, button: m.ButtonText})
	return int64(len(s.sent)), nil
}

func (s *fakeMsgSender) last(t *testing.T) sentMessage {
	t.Helper()
	if len(s.sent) == 0 {
		t.Fatal("no messages sent")
	}
	return s.sent[len(s.sent)-1]
}

type fakeAdminChecker struct {
	admin bool
	err   error
	calls []string
}

func (c *fakeAdminChecker) IsChatAdmin(ctx context.Context, chatID, userID int64) (bool, error) {
	c.calls = append(c.calls, fmt.Sprintf("%d/%d", chatID, userID))
	return c.admin, c.err
}

type fakeUsers struct {
	upserted  []*domain.User
	unblocked []int64
	err       error
}

func (u *fakeUsers) GetByTelegramID(ctx context.Context, telegramID int64) (*domain.User, error) {
	return nil, domain.ErrNotFound
}
func (u *fakeUsers) GetByID(ctx context.Context, id int64) (*domain.User, error) {
	return nil, domain.ErrNotFound
}
func (u *fakeUsers) UpsertByTelegram(ctx context.Context, usr *domain.User) error {
	if u.err != nil {
		return u.err
	}
	u.upserted = append(u.upserted, usr)
	return nil
}
func (u *fakeUsers) UpdateSettings(ctx context.Context, id int64, tz string, dmNotifyDefault bool) error {
	return errors.New("not used")
}
func (u *fakeUsers) SetBanned(ctx context.Context, id int64, banned bool) error {
	return errors.New("not used")
}
func (u *fakeUsers) SetSuperadmin(ctx context.Context, id int64, superadmin bool) error {
	return errors.New("not used")
}
func (u *fakeUsers) MarkBotBlocked(ctx context.Context, telegramID int64, blocked bool) error {
	u.unblocked = append(u.unblocked, telegramID)
	return nil
}

type fakeBinder struct {
	bindChat   func(ctx context.Context, actor *domain.User, chatID int64, threadID *int64, slug, chatTitle string) (*domain.Group, error)
	unbindChat func(ctx context.Context, actor *domain.User, chatID int64, threadID *int64) (*domain.Group, error)
	mine       []groups.MyGroup
	lastSlug   string
	lastChat   int64
	lastThread *int64
}

func (b *fakeBinder) BindChat(ctx context.Context, actor *domain.User, chatID int64, threadID *int64, slug, chatTitle string) (*domain.Group, error) {
	b.lastSlug, b.lastChat, b.lastThread = slug, chatID, threadID
	if b.bindChat != nil {
		return b.bindChat(ctx, actor, chatID, threadID, slug, chatTitle)
	}
	return &domain.Group{ID: 1, Slug: domain.Normalize(slug), Title: chatTitle, Status: domain.GroupStatusPending}, nil
}

func (b *fakeBinder) UnbindChat(ctx context.Context, actor *domain.User, chatID int64, threadID *int64) (*domain.Group, error) {
	if b.unbindChat != nil {
		return b.unbindChat(ctx, actor, chatID, threadID)
	}
	return &domain.Group{ID: 1, Slug: "ИКБО-33-21", Title: "Моя группа"}, nil
}

func (b *fakeBinder) ListMine(ctx context.Context, actor *domain.User) ([]groups.MyGroup, error) {
	return b.mine, nil
}

// --- harness ---

type harness struct {
	h      *Handlers
	sender *fakeMsgSender
	admin  *fakeAdminChecker
	users  *fakeUsers
	binder *fakeBinder
	botID  int64
	appURL string
}

func newHarness() *harness {
	i18n.MustLoad(i18n.Locales)
	h := &harness{
		sender: &fakeMsgSender{},
		admin:  &fakeAdminChecker{admin: true},
		users:  &fakeUsers{},
		binder: &fakeBinder{},
		botID:  42,
		appURL: "https://deadliner.example/app",
	}
	h.h = NewHandlers(HandlersDeps{
		Users: h.users, Binder: h.binder, Sender: h.sender,
		AdminChecker: h.admin, BotUserID: h.botID, AppPublicURL: h.appURL,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return h
}

// update строит models.Update так, как его прислал бы Telegram.
func update(chatID int64, chatType models.ChatType, userID int64, username, text string) *models.Update {
	return &models.Update{
		ID: 1,
		Message: &models.Message{
			ID:   10,
			From: &models.User{ID: userID, Username: username, FirstName: "Иван"},
			Chat: models.Chat{ID: chatID, Type: chatType, Title: "Чат группы"},
			Text: text,
		},
	}
}

// withThread помечает чат форумом и задаёт топик апдейта.
func withThread(u *models.Update, id int) *models.Update {
	u.Message.MessageThreadID = id
	u.Message.Chat.IsForum = true
	return u
}

// withoutFrom убирает автора сообщения (анонимный пост канала).
func withoutFrom(u *models.Update) *models.Update {
	u.Message.From = nil
	return u
}

// --- /start, /help ---

func TestStartPrivateSendsWelcomeWithWebAppButton(t *testing.T) {
	hs := newHarness()
	hs.h.Handle(context.Background(), update(500, models.ChatTypePrivate, 7, "ivan", "/start"))

	got := hs.sender.last(t)
	if got.chatID != 500 {
		t.Errorf("chat = %d, want 500", got.chatID)
	}
	if got.text != i18n.T("bot.start") {
		t.Errorf("text = %q, want bot.start", got.text)
	}
	if got.button != i18n.T("bot.button.open_app") {
		t.Errorf("button = %q, want %q", got.button, i18n.T("bot.button.open_app"))
	}
	// ЛС-апдейт обновляет пользователя и снимает bot_blocked.
	if len(hs.users.upserted) != 1 || hs.users.upserted[0].TelegramID != 7 {
		t.Errorf("upserted = %+v, want one user telegram_id=7", hs.users.upserted)
	}
	if len(hs.users.unblocked) != 1 || hs.users.unblocked[0] != 7 {
		t.Errorf("unblocked = %v, want [7] for a private-chat update", hs.users.unblocked)
	}
}

// F-5: групповой апдейт пользователя обновляет, но bot_blocked НЕ снимает:
// пользователь мог заблокировать бота в ЛС и продолжать писать в общий чат —
// сброс флага заставил бы воркер снова тратить 403-отправки (§7.3).
func TestGroupUpdateDoesNotClearBotBlocked(t *testing.T) {
	hs := newHarness()
	for _, upd := range []*models.Update{
		update(-100500, models.ChatTypeSupergroup, 7, "ivan", "просто текст"),
		update(-100500, models.ChatTypeSupergroup, 7, "ivan", "/help"),
		withThread(update(-100500, models.ChatTypeSupergroup, 8, "petr", "/bind_group ИКБО-33-21"), 5),
	} {
		hs.h.Handle(context.Background(), upd)
	}
	if len(hs.users.unblocked) != 0 {
		t.Errorf("unblocked = %v, want none for group updates", hs.users.unblocked)
	}
	if len(hs.users.upserted) != 3 {
		t.Errorf("upserted = %d, want 3 (user refresh still happens in groups)", len(hs.users.upserted))
	}
}

// F-5 (companion): приватный апдейт снимает флаг (пишущий боту не блокировал его).
func TestPrivateUpdateClearsBotBlocked(t *testing.T) {
	hs := newHarness()
	hs.h.Handle(context.Background(), update(500, models.ChatTypePrivate, 7, "ivan", "привет"))
	if len(hs.users.unblocked) != 1 || hs.users.unblocked[0] != 7 {
		t.Errorf("unblocked = %v, want [7]", hs.users.unblocked)
	}
}

// /start в группе молчит: приветствие в чате группы неуместно.
func TestStartInGroupSilent(t *testing.T) {
	hs := newHarness()
	hs.h.Handle(context.Background(), update(-100500, models.ChatTypeSupergroup, 7, "ivan", "/start"))
	if len(hs.sender.sent) != 0 {
		t.Errorf("sent = %+v, want no messages in group", hs.sender.sent)
	}
	// Пользователь всё равно обновлён (message.from присутствует).
	if len(hs.users.upserted) != 1 {
		t.Errorf("upserted = %+v, want one", hs.users.upserted)
	}
}

func TestHelpInBothChats(t *testing.T) {
	hs := newHarness()
	hs.h.Handle(context.Background(), update(-100500, models.ChatTypeSupergroup, 7, "ivan", "/help"))
	if got := hs.sender.last(t); got.text != i18n.T("bot.help") {
		t.Errorf("group help = %q, want bot.help", got.text)
	}
	hs.h.Handle(context.Background(), update(500, models.ChatTypePrivate, 7, "ivan", "/help"))
	if got := hs.sender.last(t); got.chatID != 500 || got.button == "" {
		t.Errorf("private help = %+v, want chat 500 with button", got)
	}
}

// /help завершается топиком форума: ответ должен уйти в тот же топик.
func TestHelpKeepsThread(t *testing.T) {
	hs := newHarness()
	hs.h.Handle(context.Background(),
		withThread(update(-100500, models.ChatTypeSupergroup, 7, "ivan", "/help"), 33))
	got := hs.sender.last(t)
	if got.threadID == nil || *got.threadID != 33 {
		t.Errorf("thread = %v, want 33", got.threadID)
	}
}

// --- /bind_group ---

func TestBindGroupSuccess(t *testing.T) {
	hs := newHarness()
	hs.h.Handle(context.Background(),
		update(-100500, models.ChatTypeSupergroup, 7, "ivan", "/bind_group икбо-33-21"))

	got := hs.sender.last(t)
	const want = "✅ Чат привязан к группе Чат группы."
	if got.text != want {
		t.Errorf("text = %q, want %q", got.text, want)
	}
	if strings.Contains(got.text, "%!") {
		t.Errorf("text contains a format artifact: %q", got.text)
	}
	if got.threadID != nil {
		t.Errorf("thread = %v, want nil for a non-forum chat", got.threadID)
	}
	if hs.binder.lastSlug != "икбо-33-21" {
		t.Errorf("slug passed = %q, want raw argument", hs.binder.lastSlug)
	}
	if hs.binder.lastChat != -100500 {
		t.Errorf("chat passed = %d, want -100500", hs.binder.lastChat)
	}
	// Бот — админ: проверка вызвана с id бота.
	if len(hs.admin.calls) != 1 || hs.admin.calls[0] != "-100500/42" {
		t.Errorf("admin checks = %v, want [-100500/42]", hs.admin.calls)
	}
}

// Не-админ чата: отказ с текстом bot.bind.not_chat_admin, use case не вызван.
func TestBindGroupBotNotChatAdmin(t *testing.T) {
	hs := newHarness()
	hs.admin.admin = false
	hs.h.Handle(context.Background(),
		update(-100500, models.ChatTypeSupergroup, 7, "ivan", "/bind_group ИКБО-33-21"))

	if got := hs.sender.last(t); got.text != i18n.T("bot.bind.not_chat_admin") {
		t.Errorf("text = %q, want bot.bind.not_chat_admin", got.text)
	}
	if hs.binder.lastChat != 0 {
		t.Errorf("BindChat must not be called when the bot is not a chat admin")
	}
}

// Топик форума: thread_id доезжает до use case.
func TestBindGroupForumTopic(t *testing.T) {
	hs := newHarness()
	hs.h.Handle(context.Background(),
		withThread(update(-100500, models.ChatTypeSupergroup, 7, "ivan", "/bind_group@DeadlinerBot ИКБО-33-21"), 99))

	if hs.binder.lastThread == nil || *hs.binder.lastThread != 99 {
		t.Errorf("thread passed = %v, want 99", hs.binder.lastThread)
	}
	if hs.binder.lastSlug != "ИКБО-33-21" {
		t.Errorf("slug = %q, want ИКБО-33-21 (bot suffix stripped)", hs.binder.lastSlug)
	}
}

// Второй слаг на тот же чат → конфликт с подсказкой про /unbind.
func TestBindGroupChatConflict(t *testing.T) {
	hs := newHarness()
	hs.binder.bindChat = func(context.Context, *domain.User, int64, *int64, string, string) (*domain.Group, error) {
		return nil, fmt.Errorf("%w: %w", domain.ErrConflict, groups.ErrChatAlreadyBound)
	}
	hs.h.Handle(context.Background(),
		update(-100500, models.ChatTypeSupergroup, 7, "ivan", "/bind_group ИКБО-33-21"))

	if got := hs.sender.last(t); got.text != i18n.T("bot.bind.conflict_already_bound") {
		t.Errorf("text = %q, want bot.bind.conflict_already_bound", got.text)
	}
}

// Группа уже привязана к другому чату → отдельный текст с названием группы.
func TestBindGroupGroupAlreadyBound(t *testing.T) {
	hs := newHarness()
	hs.binder.bindChat = func(context.Context, *domain.User, int64, *int64, string, string) (*domain.Group, error) {
		return nil, fmt.Errorf("%w: %w", domain.ErrConflict, groups.ErrGroupAlreadyBound)
	}
	hs.h.Handle(context.Background(),
		update(-100500, models.ChatTypeSupergroup, 7, "ivan", "/bind_group икбо-33-21"))

	got := hs.sender.last(t)
	if !strings.Contains(got.text, "ИКБО-33-21") {
		t.Errorf("text = %q, want the normalized slug in the message", got.text)
	}
}

// Неизвестный слаг → bot.bind.unknown_slug.
func TestBindGroupUnknownSlug(t *testing.T) {
	hs := newHarness()
	hs.binder.bindChat = func(context.Context, *domain.User, int64, *int64, string, string) (*domain.Group, error) {
		return nil, fmt.Errorf("%w: group slug_norm=АБВ", domain.ErrNotFound)
	}
	hs.h.Handle(context.Background(),
		update(-100500, models.ChatTypeSupergroup, 7, "ivan", "/bind_group АБВ-99-1"))

	if got := hs.sender.last(t); got.text != i18n.T("bot.bind.unknown_slug", "АБВ-99-1") {
		t.Errorf("text = %q, want bot.bind.unknown_slug", got.text)
	}
}

// Вызывающий не участник группы → bot.bind.not_member.
func TestBindGroupNotMember(t *testing.T) {
	hs := newHarness()
	hs.binder.bindChat = func(context.Context, *domain.User, int64, *int64, string, string) (*domain.Group, error) {
		return nil, fmt.Errorf("%w: %w", domain.ErrForbidden, groups.ErrNotMember)
	}
	hs.h.Handle(context.Background(),
		update(-100500, models.ChatTypeSupergroup, 7, "ivan", "/bind_group ИКБО-33-21"))

	if got := hs.sender.last(t); got.text != i18n.T("bot.bind.not_member") {
		t.Errorf("text = %q, want bot.bind.not_member", got.text)
	}
}

// ЛС → bot.bind.wrong_chat; без аргумента → подсказка об использовании.
func TestBindGroupWrongChatAndUsage(t *testing.T) {
	hs := newHarness()
	hs.h.Handle(context.Background(), update(500, models.ChatTypePrivate, 7, "ivan", "/bind_group ИКБО-33-21"))
	if got := hs.sender.last(t); got.text != i18n.T("bot.bind.wrong_chat") {
		t.Errorf("private text = %q, want bot.bind.wrong_chat", got.text)
	}

	hs.h.Handle(context.Background(), update(-100500, models.ChatTypeGroup, 7, "ivan", "/bind_group"))
	if got := hs.sender.last(t); got.text != i18n.T("bot.bind.usage") {
		t.Errorf("usage text = %q, want bot.bind.usage", got.text)
	}
}

// --- /unbind ---

func TestUnbindSuccess(t *testing.T) {
	hs := newHarness()
	hs.h.Handle(context.Background(), update(-100500, models.ChatTypeSupergroup, 7, "ivan", "/unbind"))
	// Литерал, а не повторный вызов i18n.T с теми же аргументами: тест должен
	// ловить отсутствие глагола в шаблоне (F-1: раньше выводилось
	// «Привязка чата снята.%!(EXTRA string=Моя группа)»).
	got := hs.sender.last(t)
	const want = "Привязка чата «Моя группа» снята."
	if got.text != want {
		t.Errorf("text = %q, want %q", got.text, want)
	}
	if strings.Contains(got.text, "%!") {
		t.Errorf("text contains a format artifact: %q", got.text)
	}
}

func TestUnbindErrors(t *testing.T) {
	hs := newHarness()
	hs.binder.unbindChat = func(context.Context, *domain.User, int64, *int64) (*domain.Group, error) {
		return nil, fmt.Errorf("%w: %w", domain.ErrForbidden, errors.New("nope"))
	}
	hs.h.Handle(context.Background(), update(-100500, models.ChatTypeSupergroup, 7, "ivan", "/unbind"))
	if got := hs.sender.last(t); got.text != i18n.T("bot.unbind.not_admin") {
		t.Errorf("forbidden text = %q, want bot.unbind.not_admin", got.text)
	}

	hs.binder.unbindChat = func(context.Context, *domain.User, int64, *int64) (*domain.Group, error) {
		return nil, fmt.Errorf("%w: %w", domain.ErrNotFound, groups.ErrBindingNotFound)
	}
	hs.h.Handle(context.Background(), update(-100500, models.ChatTypeSupergroup, 7, "ivan", "/unbind"))
	if got := hs.sender.last(t); got.text != i18n.T("bot.unbind.not_bound") {
		t.Errorf("not bound text = %q, want bot.unbind.not_bound", got.text)
	}
}

// --- /groups, /new_deadline ---

func TestGroupsPrivate(t *testing.T) {
	hs := newHarness()
	hs.binder.mine = []groups.MyGroup{
		{Group: domain.Group{Slug: "ИКБО-33-21", Title: "Моя группа"}, Role: domain.RoleAdmin},
		{Group: domain.Group{Slug: "М8О-401Б-23", Title: "Другая"}, Role: domain.RoleMember},
	}
	hs.h.Handle(context.Background(), update(500, models.ChatTypePrivate, 7, "ivan", "/groups"))

	got := hs.sender.last(t)
	for _, want := range []string{"ИКБО-33-21", i18n.T("bot.role.admin"), "М8О-401Б-23", i18n.T("bot.role.member")} {
		if !strings.Contains(got.text, want) {
			t.Errorf("text = %q, missing %q", got.text, want)
		}
	}
}

func TestGroupsEmptyAndGroupChat(t *testing.T) {
	hs := newHarness()
	hs.h.Handle(context.Background(), update(500, models.ChatTypePrivate, 7, "ivan", "/groups"))
	if got := hs.sender.last(t); got.text != i18n.T("bot.groups.empty") {
		t.Errorf("empty text = %q, want bot.groups.empty", got.text)
	}

	hs.h.Handle(context.Background(), update(-100500, models.ChatTypeGroup, 7, "ivan", "/groups"))
	if got := hs.sender.last(t); got.text != i18n.T("bot.command.private_only") {
		t.Errorf("group text = %q, want bot.command.private_only", got.text)
	}
}

func TestNewDeadlinePrivate(t *testing.T) {
	hs := newHarness()
	hs.h.Handle(context.Background(), update(500, models.ChatTypePrivate, 7, "ivan", "/new_deadline"))
	if got := hs.sender.last(t); got.text != i18n.T("bot.new_deadline") || got.button == "" {
		t.Errorf("msg = %+v, want bot.new_deadline with a button", got)
	}
}

// --- служебные инварианты ---

// Апдейт без автора (анонимный пост канала): пользователь не обновляется,
// команда не выполняется — но обработка не падает.
func TestUpdateWithoutFromInGroup(t *testing.T) {
	hs := newHarness()
	hs.h.Handle(context.Background(),
		withoutFrom(update(-100500, models.ChatTypeChannel, 0, "", "/bind_group ИКБО-33-21")))

	if len(hs.users.upserted) != 0 {
		t.Errorf("upserted = %+v, want none", hs.users.upserted)
	}
	if got := hs.sender.last(t); got.text != i18n.T("bot.error.generic") {
		t.Errorf("text = %q, want bot.error.generic", got.text)
	}
}

// Некоманда (просто текст) — не отвечаем, но пользователь обновлён.
func TestPlainTextIgnored(t *testing.T) {
	hs := newHarness()
	hs.h.Handle(context.Background(), update(500, models.ChatTypePrivate, 7, "ivan", "привет"))
	if len(hs.sender.sent) != 0 {
		t.Errorf("sent = %+v, want none", hs.sender.sent)
	}
	if len(hs.users.upserted) != 1 {
		t.Errorf("upserted = %+v, want one", hs.users.upserted)
	}
}

// Superadmin-команды Task 12 не реализованы и не отвечают.
func TestSuperadminCommandsNotImplemented(t *testing.T) {
	hs := newHarness()
	for _, cmd := range []string{"/promote 1 ИКБО-33-21", "/ban 1", "/stats", "/delete_group ИКБО-33-21"} {
		hs.h.Handle(context.Background(), update(500, models.ChatTypePrivate, 7, "ivan", cmd))
	}
	if len(hs.sender.sent) != 0 {
		t.Errorf("sent = %+v, want none for Task 12 commands", hs.sender.sent)
	}
}

// Callback-запросы и пустые апдейты игнорируются.
func TestNonMessageUpdatesIgnored(t *testing.T) {
	hs := newHarness()
	hs.h.Handle(context.Background(), &models.Update{ID: 5, CallbackQuery: &models.CallbackQuery{ID: "1"}})
	hs.h.Handle(context.Background(), nil)
	if len(hs.sender.sent) != 0 || len(hs.users.upserted) != 0 {
		t.Errorf("non-message updates must be ignored")
	}
}

func TestSplitCommand(t *testing.T) {
	cases := []struct{ in, cmd, arg string }{
		{"/start", "start", ""},
		{"/help", "help", ""},
		{"/bind_group ИКБО-33-21", "bind_group", "ИКБО-33-21"},
		{"/bind_group@DeadlinerBot ИКБО-33-21", "bind_group", "ИКБО-33-21"},
		{"/unbind", "unbind", ""},
		{"/start@DeadlinerBot", "start", ""},
		{"/Bind_Group м8о-401б-23", "bind_group", "м8о-401б-23"},
	}
	for _, c := range cases {
		cmd, arg := splitCommand(c.in)
		if cmd != c.cmd || arg != c.arg {
			t.Errorf("splitCommand(%q) = (%q, %q), want (%q, %q)", c.in, cmd, arg, c.cmd, c.arg)
		}
	}
}

// commands() перечисляет только клиентские команды v1 (Task 12 — отдельно).
func TestCommandsList(t *testing.T) {
	hs := newHarness()
	cmds := hs.h.commands()
	want := []string{"start", "help", "groups", "new_deadline", "bind_group", "unbind"}
	if len(cmds) != len(want) {
		t.Fatalf("commands = %d, want %d", len(cmds), len(want))
	}
	for i, c := range cmds {
		if c.Command != want[i] {
			t.Errorf("command[%d] = %q, want %q", i, c.Command, want[i])
		}
		if c.Description == "" || c.Description == "i18n."+c.Command {
			t.Errorf("command %q has no localized description (%q)", c.Command, c.Description)
		}
	}
}

// --- BotSender (транспорт) ---

// Смоук BotSender на httptest-заглушке Telegram API: проверяем, что запрос
// собирается, parse_mode/reply_markup доезжают, а 429/403 маппятся в доменные.
func TestBotSenderSendAgainstStubAPI(t *testing.T) {
	var gotForm map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("parse form: %v", err)
		}
		gotForm = map[string]string{}
		for k, v := range r.MultipartForm.Value {
			gotForm[k] = v[0]
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true,"result":{"message_id":77,"chat":{"id":-100500,"type":"supergroup"},"date":1}}`)
	}))
	defer srv.Close()

	api, err := tgbot.New("42:TEST", tgbot.WithServerURL(srv.URL), tgbot.WithSkipGetMe())
	if err != nil {
		t.Fatalf("bot.New: %v", err)
	}
	sender := &BotSender{api: api}

	thread := int64(9)
	id, err := sender.Send(context.Background(), OutMessage{
		ChatID: -100500, ThreadID: &thread, Text: "код 012345",
		ButtonText: "Открыть Deadliner", ButtonURL: "https://example/app", LinkPreviewOff: true,
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if id != 77 {
		t.Errorf("message id = %d, want 77", id)
	}
	if gotForm["chat_id"] != "-100500" || gotForm["text"] != "код 012345" {
		t.Errorf("form = %+v", gotForm)
	}
	if gotForm["message_thread_id"] != "9" {
		t.Errorf("message_thread_id = %q, want 9", gotForm["message_thread_id"])
	}
	// F-6: тексты каталога размечены под HTML (спека §6.2), поэтому parse_mode
	// обязан доехать до API — иначе теги <b> уйдут в чат как текст.
	if gotForm["parse_mode"] != "HTML" {
		t.Errorf("parse_mode = %q, want HTML", gotForm["parse_mode"])
	}
	// Групповой чат (chat_id < 0): обычная url-кнопка, web_app запрещён Telegram.
	if !strings.Contains(gotForm["reply_markup"], `"url":"https://example/app"`) {
		t.Errorf("reply_markup = %q, want a url button", gotForm["reply_markup"])
	}
}

func TestBotSenderPrivateUsesWebAppButton(t *testing.T) {
	var markup string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseMultipartForm(1 << 20)
		markup = r.MultipartForm.Value["reply_markup"][0]
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true,"result":{"message_id":1,"chat":{"id":7,"type":"private"},"date":1}}`)
	}))
	defer srv.Close()

	api, _ := tgbot.New("42:TEST", tgbot.WithServerURL(srv.URL), tgbot.WithSkipGetMe())
	sender := &BotSender{api: api}
	if _, err := sender.Send(context.Background(), OutMessage{
		ChatID: 7, Text: "hi", ButtonText: "Открыть Deadliner", ButtonURL: "https://example/app",
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !strings.Contains(markup, `"web_app":{"url":"https://example/app"}`) {
		t.Errorf("reply_markup = %q, want a web_app button", markup)
	}
}

// 429 → domain.RateLimitError с retry_after; 403 → domain.BotBlockedError.
func TestBotSenderErrorMapping(t *testing.T) {
	cases := []struct {
		name     string
		response string
		check    func(*testing.T, error)
	}{
		{
			name:     "429",
			response: `{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":7}}`,
			check: func(t *testing.T, err error) {
				var rl *domain.RateLimitError
				if !errors.As(err, &rl) || rl.RetryAfter.Seconds() != 7 {
					t.Fatalf("err = %v, want RateLimitError(7s)", err)
				}
			},
		},
		{
			name:     "403",
			response: `{"ok":false,"error_code":403,"description":"Forbidden: bot was blocked by the user"}`,
			check: func(t *testing.T, err error) {
				var bb *domain.BotBlockedError
				if !errors.As(err, &bb) || bb.UserID != 7 {
					t.Fatalf("err = %v, want BotBlockedError{7}", err)
				}
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, c.response)
			}))
			defer srv.Close()
			api, _ := tgbot.New("42:TEST", tgbot.WithServerURL(srv.URL), tgbot.WithSkipGetMe())
			sender := &BotSender{api: api}
			_, err := sender.Send(context.Background(), OutMessage{ChatID: 7, Text: "hi"})
			c.check(t, err)
		})
	}
}

// IsChatAdmin: creator/administrator → true, member → false, без id бота → false.
func TestBotSenderIsChatAdmin(t *testing.T) {
	responses := map[string]string{
		"creator":       `{"ok":true,"result":{"status":"creator","user":{"id":42,"is_bot":true,"first_name":"B"},"is_anonymous":false}}`,
		"administrator": `{"ok":true,"result":{"status":"administrator","user":{"id":42,"is_bot":true,"first_name":"B"},"can_be_edited":false,"is_anonymous":false,"can_manage_chat":true,"can_delete_messages":true,"can_manage_video_chats":true,"can_restrict_members":true,"can_promote_members":false,"can_change_info":true,"can_invite_users":true}}`,
		"member":        `{"ok":true,"result":{"status":"member","user":{"id":42,"is_bot":true,"first_name":"B"}}}`,
		"left":          `{"ok":true,"result":{"status":"left","user":{"id":42,"is_bot":true,"first_name":"B"}}}`,
	}
	want := map[string]bool{"creator": true, "administrator": true, "member": false, "left": false}
	for status, body := range responses {
		t.Run(status, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, body)
			}))
			defer srv.Close()
			api, _ := tgbot.New("42:TEST", tgbot.WithServerURL(srv.URL), tgbot.WithSkipGetMe())
			sender := &BotSender{api: api}

			got, err := sender.IsChatAdmin(context.Background(), -100500, 42)
			if err != nil {
				t.Fatalf("IsChatAdmin: %v", err)
			}
			if got != want[status] {
				t.Errorf("IsChatAdmin(%s) = %v, want %v", status, got, want[status])
			}
		})
	}

	// Пустой userID (id бота неизвестен) — запрос не делается вовсе.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected API call for empty bot id")
	}))
	defer srv.Close()
	api, _ := tgbot.New("42:TEST", tgbot.WithServerURL(srv.URL), tgbot.WithSkipGetMe())
	sender := &BotSender{api: api}
	if ok, err := sender.IsChatAdmin(context.Background(), -1, 0); err != nil || ok {
		t.Errorf("IsChatAdmin(unknown bot id) = (%v, %v), want (false, nil)", ok, err)
	}
}

// Notifier расширен без ломания Sender: фейк, реализующий только SendMessage,
// продолжает работать, а SendToChatID возвращает message_id.
func TestNotifierSendToChatID(t *testing.T) {
	f := &minimalSender{}
	n := New(f, 1000, 1000)
	id, err := n.SendToChatID(context.Background(), -100500, 0, "код")
	if err != nil {
		t.Fatalf("SendToChatID: %v", err)
	}
	if id == 0 {
		t.Errorf("message id = 0, want non-zero")
	}
	if f.calls != 1 {
		t.Errorf("calls = %d, want 1", f.calls)
	}
}

// ClaimsSender: при MessageSender-транспорте кнопка уходит, message_id
// возвращается; при минимальном Sender — деградация до обычного SendMessage.
func TestClaimsSenderDecorations(t *testing.T) {
	t.Run("with MessageSender", func(t *testing.T) {
		fs := &fakeMsgSender{}
		n := New(fs, 1000, 1000)
		cs := NewClaimsSender(n, "https://example/app")
		id, err := cs.SendToChat(context.Background(), -100500, 0, "код")
		if err != nil {
			t.Fatalf("SendToChat: %v", err)
		}
		if id == 0 || len(fs.sent) != 1 || fs.sent[0].button != "Открыть Deadliner" {
			t.Errorf("sent = %+v (id=%d), want one message with a button", fs.sent, id)
		}
		if fs.sent[0].chatID != -100500 || fs.sent[0].text != "код" {
			t.Errorf("sent = %+v, want chat -100500 with the code text", fs.sent[0])
		}
	})
	t.Run("minimal Sender", func(t *testing.T) {
		// Транспорт без MessageSender: кнопки нет, но сообщение уходит — Sender
		// остаётся достаточным контрактом (расширять его было запрещено).
		fs := &minimalSender{}
		n := New(fs, 1000, 1000)
		cs := NewClaimsSender(n, "https://example/app")
		if _, err := cs.SendToChat(context.Background(), -100500, 0, "код"); err != nil {
			t.Fatalf("SendToChat: %v", err)
		}
		if fs.calls != 1 {
			t.Errorf("calls = %d, want 1", fs.calls)
		}
	})
	t.Run("SendToUser", func(t *testing.T) {
		fs := &minimalSender{}
		n := New(fs, 1000, 1000)
		cs := NewClaimsSender(n, "")
		if err := cs.SendToUser(context.Background(), 7, "внимание"); err != nil {
			t.Fatalf("SendToUser: %v", err)
		}
		if fs.calls != 1 || fs.lastChat != 7 {
			t.Errorf("calls=%d lastChat=%d, want 1/7", fs.calls, fs.lastChat)
		}
	})
}

// minimalSender реализует ровно Sender (без MessageSender) — проверяем, что
// нотификатор и подписант не требуют большего.
type minimalSender struct {
	calls    int
	lastChat int64
	lastText string
}

func (s *minimalSender) SendMessage(ctx context.Context, chatID int64, threadID *int64, text string, linkPreviewOff bool) (int64, error) {
	s.calls++
	s.lastChat, s.lastText = chatID, text
	return int64(s.calls) + 1000, nil
}

// F-6: ответы бота тоже уходят с parse_mode=HTML, поэтому пользовательские
// значения в них экранируются — иначе Telegram отвергнет сообщение (400) или
// покажет чужую разметку.
func TestBotRepliesEscapeUserText(t *testing.T) {
	hs := newHarness()
	// Слаг с HTML-спецсимволами доходит до текста об ошибке.
	hs.binder.bindChat = func(context.Context, *domain.User, int64, *int64, string, string) (*domain.Group, error) {
		return nil, fmt.Errorf("%w: group slug_norm=X", domain.ErrNotFound)
	}
	hs.h.Handle(context.Background(),
		update(-100500, models.ChatTypeSupergroup, 7, "ivan", "/bind_group <b>ЗЛОЙ</b>"))

	got := hs.sender.last(t)
	if strings.Contains(got.text, "<b>ЗЛОЙ</b>") {
		t.Errorf("slug leaked unescaped into the reply: %q", got.text)
	}
	if !strings.Contains(got.text, "&lt;B&gt;ЗЛОЙ&lt;/B&gt;") {
		t.Errorf("escaped slug missing in the reply: %q", got.text)
	}

	// Название группы в /groups и в подтверждении привязки — тоже.
	hs.binder.mine = []groups.MyGroup{{
		Group: domain.Group{Slug: "ИКБО-33-21", Title: "<i>&</i> Моя группа"},
		Role:  domain.RoleAdmin,
	}}
	hs.h.Handle(context.Background(), update(500, models.ChatTypePrivate, 7, "ivan", "/groups"))
	if got := hs.sender.last(t); strings.Contains(got.text, "<i>&</i>") {
		t.Errorf("group title leaked unescaped into /groups: %q", got.text)
	}

	hs.binder.bindChat = nil
	hs.h.Handle(context.Background(),
		update(-100500, models.ChatTypeSupergroup, 7, "ivan", "/bind_group ИКБО-33-21"))
	if got := hs.sender.last(t); strings.Contains(got.text, "<") {
		t.Errorf("unexpected markup in the bind confirmation: %q", got.text)
	}
}

// Разметка каталога в ответах бота снимается (i18n.Plain): пользователь не
// должен видеть литеральные теги, даже если шаблон их содержит.
func TestBotRepliesStripCatalogMarkup(t *testing.T) {
	hs := newHarness()
	hs.h.Handle(context.Background(), update(500, models.ChatTypePrivate, 7, "ivan", "/start"))
	if got := hs.sender.last(t); strings.Contains(got.text, "<b>") || strings.Contains(got.text, "</b>") {
		t.Errorf("catalog markup leaked into the reply: %q", got.text)
	}
}
