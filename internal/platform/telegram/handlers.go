package telegram

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/go-telegram/bot/models"

	"github.com/sauron/deadliner/internal/app/groups"
	"github.com/sauron/deadliner/internal/domain"
	"github.com/sauron/deadliner/internal/i18n"
)

// Handlers — обработчики апдейтов (спека §6.1). Зависит только от узких
// интерфейсов (MessageSender, ChatAdminChecker), use case-поверхности
// GroupBinder и domain.UserRepo, поэтому тестируется на фейках с обычными
// models.Update, без сети.
type Handlers struct {
	users        domain.UserRepo
	binder       GroupBinder
	superadmin   Superadmin
	reports      SlugReporter
	sender       MessageSender
	adminChecker ChatAdminChecker
	botUserID    int64
	appURL       string
	log          *slog.Logger
}

// HandlersDeps — зависимости хендлеров.
type HandlersDeps struct {
	Users        domain.UserRepo
	Binder       GroupBinder
	Sender       MessageSender
	AdminChecker ChatAdminChecker
	// Superadmin — служебные команды (§6.1). nil отключает их (в тестах
	// административных сценариев передаётся явно).
	Superadmin Superadmin
	// Reports — жалоба на слаг (/report_slug, спека §3.3). nil отключает
	// команду: без use case отправлять жалобу некуда.
	Reports      SlugReporter
	BotUserID    int64
	AppPublicURL string
}

func NewHandlers(d HandlersDeps, log *slog.Logger) *Handlers {
	if log == nil {
		log = slog.Default()
	}
	return &Handlers{
		users: d.Users, binder: d.Binder, superadmin: d.Superadmin,
		reports: d.Reports,
		sender:  d.Sender, adminChecker: d.AdminChecker, botUserID: d.BotUserID,
		appURL: d.AppPublicURL, log: log,
	}
}

// commands — набор setMyCommands (спека §6.1). Superadmin-команды
// публикуются только в default-scope: их существование не должно попадать в
// меню администраторов чатов, где они бессмысленны.
func (h *Handlers) commands() []BotCommand {
	return []BotCommand{
		{Command: "start", Description: i18n.T("bot.cmd.start")},
		{Command: "help", Description: i18n.T("bot.cmd.help")},
		{Command: "groups", Description: i18n.T("bot.cmd.groups")},
		{Command: "new_deadline", Description: i18n.T("bot.cmd.new_deadline")},
		{Command: "bind_group", Description: i18n.T("bot.cmd.bind_group")},
		{Command: "unbind", Description: i18n.T("bot.cmd.unbind")},
		{Command: "promote", Description: i18n.T("bot.cmd.promote")},
		{Command: "ban", Description: i18n.T("bot.cmd.ban")},
		{Command: "unban", Description: i18n.T("bot.cmd.unban")},
		{Command: "stats", Description: i18n.T("bot.cmd.stats")},
		{Command: "delete_group", Description: i18n.T("bot.cmd.delete_group")},
		{Command: "report_slug", Description: i18n.T("bot.cmd.report_slug")},
	}
}

// Handle — разбор одного апдейта: обновление пользователя + маршрутизация
// команды. Любой апдейт в ЛС обновляет users и снимает bot_blocked (сам факт
// сообщения боту доказывает, что пользователь его не блокировал), в группах
// пользователь обновляется только при наличии message.from.
//
// Бан не проверяется на каждое сообщение: UpsertByTelegram возвращает частичного
// пользователя (is_banned не читается). API-пути защищены middleware.Auth и
// auth.Login; команды, живущие только в боте (/bind_group, /unbind, служебные),
// гидратируют вызывающего сами (Handlers.isBanned, handleSuperadmin).
func (h *Handlers) Handle(ctx context.Context, upd *models.Update) {
	if upd == nil || upd.Message == nil {
		// Прочие типы апдейтов (callback_query и т.п.) вне периметра v1.
		return
	}
	msg := upd.Message

	actor := h.touchUser(ctx, msg)
	text := strings.TrimSpace(msg.Text)
	if !strings.HasPrefix(text, "/") {
		return
	}
	cmd, arg := splitCommand(text)

	switch cmd {
	case "start":
		h.handleStart(ctx, msg)
	case "help":
		h.handleHelp(ctx, msg)
	case "groups":
		h.handleGroups(ctx, msg, actor)
	case "new_deadline":
		h.handleNewDeadline(ctx, msg)
	case "bind_group":
		h.handleBindGroup(ctx, msg, actor, arg)
	case "unbind":
		h.handleUnbind(ctx, msg, actor)
	case "promote", "ban", "unban", "stats", "delete_group":
		// Служебные команды (§6.1): guard is_superadmin внутри.
		h.handleSuperadmin(ctx, msg, actor, cmd, arg)
	case "report_slug":
		// Жалоба на слаг (§3.3): только ЛС, права проверяет use case.
		h.handleReportSlug(ctx, msg, actor, arg)
	}
	// Неизвестные команды молча игнорируются.
}

// touchUser обновляет users (UpsertByTelegram) и — только в ЛС — снимает
// bot_blocked: групповой апдейт не доказывает, что бот не заблокирован в ЛС
// (§7.3). Возвращает данные автора или nil, если автора нет / БД недоступна.
func (h *Handlers) touchUser(ctx context.Context, msg *models.Message) *domain.User {
	if msg.From == nil || h.users == nil {
		return nil
	}
	from := msg.From
	u := &domain.User{
		TelegramID: from.ID,
		Username:   from.Username,
		FirstName:  from.FirstName,
	}
	if err := h.users.UpsertByTelegram(ctx, u); err != nil {
		h.log.Warn("telegram: upsert user failed",
			slog.Int64("telegram_id", from.ID), slog.String("error", err.Error()))
		return nil
	}
	if !isPrivate(msg.Chat.Type) {
		return u
	}
	// Личное сообщение боту — доказательство, что он не заблокирован.
	if err := h.users.MarkBotBlocked(ctx, from.ID, false); err != nil {
		h.log.Warn("telegram: mark bot unblocked failed",
			slog.Int64("telegram_id", from.ID), slog.String("error", err.Error()))
	}
	return u
}

// splitCommand — «/bind_group@MyBot ИКБО-33-21» → ("bind_group", "ИКБО-33-21"):
// снимает слэш, суффикс @botname и отделяет аргумент.
func splitCommand(text string) (cmd, arg string) {
	rest := strings.TrimPrefix(text, "/")
	if i := strings.IndexAny(rest, " \n\t"); i >= 0 {
		arg = strings.TrimSpace(rest[i+1:])
		rest = rest[:i]
	}
	if i := strings.IndexByte(rest, '@'); i >= 0 {
		rest = rest[:i]
	}
	return strings.ToLower(rest), arg
}

// handleStart — /start в ЛС: приветствие + inline web_app-кнопка (спека §6.1).
// В группе /start ничего не постит: приветствие там неуместно.
func (h *Handlers) handleStart(ctx context.Context, msg *models.Message) {
	if !isPrivate(msg.Chat.Type) {
		return
	}
	h.send(ctx, msg.Chat.ID, nil, i18n.T("bot.start"), true)
}

// handleHelp — /help в любом чате. Разметка намеренно отключена: в тексте есть
// угловые скобки («<slug>»), которые Telegram отверг бы как HTML.
func (h *Handlers) handleHelp(ctx context.Context, msg *models.Message) {
	h.send(ctx, msg.Chat.ID, threadIDOf(msg), i18n.T("bot.help"), isPrivate(msg.Chat.Type))
}

// handleGroups — /groups в ЛС (спека §6.1): мои группы и роли.
func (h *Handlers) handleGroups(ctx context.Context, msg *models.Message, actor *domain.User) {
	if !isPrivate(msg.Chat.Type) {
		h.send(ctx, msg.Chat.ID, threadIDOf(msg), i18n.T("bot.command.private_only"), false)
		return
	}
	if actor == nil || h.binder == nil {
		h.send(ctx, msg.Chat.ID, nil, i18n.T("bot.error.generic"), false)
		return
	}
	mine, err := h.binder.ListMine(ctx, actor)
	if err != nil {
		h.log.Warn("telegram: /groups failed", slog.String("error", err.Error()))
		h.send(ctx, msg.Chat.ID, nil, i18n.T("bot.error.generic"), false)
		return
	}
	if len(mine) == 0 {
		h.send(ctx, msg.Chat.ID, nil, i18n.T("bot.groups.empty"), true)
		return
	}
	lines := make([]string, 0, len(mine)+1)
	lines = append(lines, i18n.T("bot.groups.header"))
	for _, mg := range mine {
		lines = append(lines, i18n.T("bot.groups.item",
			i18n.EscapeHTML(mg.Group.Slug),
			i18n.EscapeHTML(i18n.T("bot.role."+string(mg.Role))),
			i18n.EscapeHTML(mg.Group.Title)))
	}
	h.send(ctx, msg.Chat.ID, nil, strings.Join(lines, "\n"), true)
}

// handleNewDeadline живёт в handlers_deadline.go.

// handleBindGroup — /bind_group <slug> (спека §6.1): привязать этот
// chat_id(+thread_id) к группе. Порядок: чат (не ЛС) → аргумент → бан
// вызывающего → бот — админ чата → use case.
func (h *Handlers) handleBindGroup(ctx context.Context, msg *models.Message, actor *domain.User, arg string) {
	if isPrivate(msg.Chat.Type) {
		h.send(ctx, msg.Chat.ID, nil, i18n.T("bot.bind.wrong_chat"), false)
		return
	}
	thread := threadIDOf(msg)
	if arg == "" {
		h.send(ctx, msg.Chat.ID, thread, i18n.T("bot.bind.usage"), false)
		return
	}
	if actor == nil || h.binder == nil || h.adminChecker == nil {
		h.send(ctx, msg.Chat.ID, thread, i18n.T("bot.error.generic"), false)
		return
	}
	if banned, err := h.isBanned(ctx, actor.TelegramID); err != nil {
		h.send(ctx, msg.Chat.ID, thread, i18n.T("bot.error.generic"), false)
		return
	} else if banned {
		h.send(ctx, msg.Chat.ID, thread, i18n.T("bot.forbidden"), false)
		return
	}

	ok, err := h.adminChecker.IsChatAdmin(ctx, msg.Chat.ID, h.botUserID)
	if err != nil {
		h.log.Warn("telegram: /bind_group admin check failed", slog.String("error", err.Error()))
		h.send(ctx, msg.Chat.ID, thread, i18n.T("bot.error.generic"), false)
		return
	}
	if !ok {
		h.send(ctx, msg.Chat.ID, thread, i18n.T("bot.bind.not_chat_admin"), false)
		return
	}

	g, err := h.binder.BindChat(ctx, actor, msg.Chat.ID, thread, arg, msg.Chat.Title)
	if err != nil {
		h.send(ctx, msg.Chat.ID, thread, bindErrorText(arg, err), false)
		return
	}
	h.send(ctx, msg.Chat.ID, thread, i18n.T("bot.bind.ok", i18n.EscapeHTML(g.Title)), false)
}

// handleUnbind — /unbind: снять привязку (роль admin группы, спека §6.1).
// Забаненный вызывающий отсекается как в /bind_group: путь живёт только в
// боте, middleware.Auth его не прикрывает.
func (h *Handlers) handleUnbind(ctx context.Context, msg *models.Message, actor *domain.User) {
	if isPrivate(msg.Chat.Type) {
		h.send(ctx, msg.Chat.ID, nil, i18n.T("bot.bind.wrong_chat"), false)
		return
	}
	thread := threadIDOf(msg)
	if actor == nil || h.binder == nil {
		h.send(ctx, msg.Chat.ID, thread, i18n.T("bot.error.generic"), false)
		return
	}
	if banned, err := h.isBanned(ctx, actor.TelegramID); err != nil {
		h.send(ctx, msg.Chat.ID, thread, i18n.T("bot.error.generic"), false)
		return
	} else if banned {
		h.send(ctx, msg.Chat.ID, thread, i18n.T("bot.forbidden"), false)
		return
	}
	g, err := h.binder.UnbindChat(ctx, actor, msg.Chat.ID, thread)
	if err != nil {
		h.send(ctx, msg.Chat.ID, thread, unbindErrorText(err), false)
		return
	}
	h.send(ctx, msg.Chat.ID, thread, i18n.T("bot.unbind.ok", i18n.EscapeHTML(g.Title)), false)
}

// isBanned — гидратация вызывающего ради флага бана. Вызывается только из
// команд записи, доступных из чата (/bind_group, /unbind): actor из touchUser
// частичный (is_banned не читается), а эти пути живут только в боте —
// middleware.Auth их не прикрывает. Одно чтение на команду, а не на каждое
// сообщение: команды редкие и именно они меняют состояние чата. Ошибка чтения
// трактуется вызывающим как generic: пропустить забаненного хуже, чем
// отказать в привязке при сбое БД.
func (h *Handlers) isBanned(ctx context.Context, telegramID int64) (bool, error) {
	if h.users == nil {
		return false, nil
	}
	u, err := h.users.GetByTelegramID(ctx, telegramID)
	if err != nil {
		h.log.Warn("telegram: ban check hydration failed",
			slog.Int64("telegram_id", telegramID), slog.String("error", err.Error()))
		return false, err
	}
	return u.IsBanned, nil
}

// bindErrorText — доменная ошибка → текст для чата: максимально конкретно,
// команду пишет человек, который не знает состояние группы.
func bindErrorText(slug string, err error) string {
	switch {
	case errors.Is(err, domain.ErrInvalidSlug):
		return i18n.T("api.error.slug_invalid")
	case errors.Is(err, groups.ErrNotMember):
		return i18n.T("bot.bind.not_member")
	case errors.Is(err, groups.ErrChatAlreadyBound):
		return i18n.T("bot.bind.conflict_already_bound")
	case errors.Is(err, groups.ErrGroupAlreadyBound):
		return i18n.T("bot.bind.group_bound_elsewhere", i18n.EscapeHTML(domain.Normalize(slug)))
	case errors.Is(err, domain.ErrForbidden):
		return i18n.T("bot.bind.not_member")
	case errors.Is(err, domain.ErrNotFound):
		return i18n.T("bot.bind.unknown_slug", i18n.EscapeHTML(domain.Normalize(slug)))
	default:
		return i18n.T("bot.error.generic")
	}
}

// unbindErrorText — то же для /unbind.
func unbindErrorText(err error) string {
	switch {
	case errors.Is(err, groups.ErrBindingNotFound):
		return i18n.T("bot.unbind.not_bound")
	case errors.Is(err, domain.ErrForbidden):
		return i18n.T("bot.unbind.not_admin")
	default:
		return i18n.T("bot.error.generic")
	}
}

// send — отправка в чат апдейта (тема форума сохраняется). Ошибка отправки не
// роняет обработку апдейта: логируется и проглатывается (ответ — не транзакция).
func (h *Handlers) send(ctx context.Context, chatID int64, threadID *int64, text string, withButton bool) {
	// Тексты команд — без разметки: i18n.Plain снимает HTML-теги каталога,
	// чтобы пользователь не увидел литеральные <b> при любом parse_mode.
	m := OutMessage{ChatID: chatID, ThreadID: threadID, Text: i18n.Plain(text), LinkPreviewOff: true}
	if withButton && h.appURL != "" {
		m.ButtonText = i18n.T("bot.button.open_app")
		m.ButtonURL = h.appURL
	}
	h.deliver(ctx, m)
}

// deliver отправляет готовый OutMessage и проглатывает ошибку отправки: ответ
// пользователю — не транзакция, падать из-за него незачем.
func (h *Handlers) deliver(ctx context.Context, m OutMessage) {
	if h.sender == nil || m.Text == "" {
		return
	}
	if _, err := h.sender.Send(ctx, m); err != nil {
		h.log.Warn("telegram: send failed",
			slog.Int64("chat_id", m.ChatID), slog.String("error", err.Error()))
	}
}

// threadIDOf — message_thread_id апдейта (nil для обычных чатов): привязка в
// форуме идёт по конкретному топику.
func threadIDOf(msg *models.Message) *int64 {
	if msg.MessageThreadID == 0 {
		return nil
	}
	id := int64(msg.MessageThreadID)
	return &id
}

func isPrivate(t models.ChatType) bool { return t == models.ChatTypePrivate }
