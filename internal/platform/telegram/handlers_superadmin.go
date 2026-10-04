package telegram

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"

	"github.com/go-telegram/bot/models"

	"github.com/sauron/deadliner/internal/domain"
	"github.com/sauron/deadliner/internal/i18n"
)

// handleSuperadmin — /promote, /ban, /unban, /stats, /delete_group (спека §6.1,
// только ЛС superadmin). В группах команды игнорируются молча: ответ в общем
// чате раскрывал бы существование служебных команд; в ЛС отказ явный.
// UpsertByTelegram возвращает частичного пользователя (is_superadmin /
// is_banned не читаются), поэтому перед командой обязательна гидратация
// GetByTelegramID.
func (h *Handlers) handleSuperadmin(ctx context.Context, msg *models.Message, actor *domain.User, cmd, arg string) {
	if !isPrivate(msg.Chat.Type) {
		return
	}
	if h.superadmin == nil || h.users == nil {
		h.send(ctx, msg.Chat.ID, nil, i18n.T("bot.error.generic"), false)
		return
	}
	if actor == nil {
		h.send(ctx, msg.Chat.ID, nil, i18n.T("bot.error.generic"), false)
		return
	}

	caller, err := h.users.GetByTelegramID(ctx, actor.TelegramID)
	if err != nil {
		h.log.Warn("telegram: superadmin hydration failed",
			slog.Int64("telegram_id", actor.TelegramID), slog.String("error", err.Error()))
		h.send(ctx, msg.Chat.ID, nil, i18n.T("bot.error.generic"), false)
		return
	}
	if !caller.IsSuperadmin {
		h.send(ctx, msg.Chat.ID, nil, i18n.T("superadmin.only"), false)
		return
	}
	// Забаненный не считается супер-админом; проверка бесплатна —
	// пользователь уже гидратирован.
	if caller.IsBanned {
		h.send(ctx, msg.Chat.ID, nil, i18n.T("superadmin.only"), false)
		return
	}

	switch cmd {
	case "promote":
		tgID, ok := parseTelegramIDArg(arg)
		if !ok {
			h.send(ctx, msg.Chat.ID, nil, i18n.T("superadmin.usage_promote"), false)
			return
		}
		if err := h.superadmin.PromoteSuperadmin(ctx, caller, tgID); err != nil {
			h.send(ctx, msg.Chat.ID, nil, superadminErrorText(err), false)
			return
		}
		h.send(ctx, msg.Chat.ID, nil, i18n.T("superadmin.promoted", formatID(tgID)), false)

	case "ban":
		tgID, ok := parseTelegramIDArg(arg)
		if !ok {
			h.send(ctx, msg.Chat.ID, nil, i18n.T("superadmin.usage_user", cmd, cmd), false)
			return
		}
		if err := h.superadmin.BanUser(ctx, caller, tgID); err != nil {
			h.send(ctx, msg.Chat.ID, nil, superadminErrorText(err), false)
			return
		}
		h.send(ctx, msg.Chat.ID, nil, i18n.T("superadmin.banned", formatID(tgID)), false)

	case "unban":
		tgID, ok := parseTelegramIDArg(arg)
		if !ok {
			h.send(ctx, msg.Chat.ID, nil, i18n.T("superadmin.usage_user", cmd, cmd), false)
			return
		}
		if err := h.superadmin.UnbanUser(ctx, caller, tgID); err != nil {
			h.send(ctx, msg.Chat.ID, nil, superadminErrorText(err), false)
			return
		}
		h.send(ctx, msg.Chat.ID, nil, i18n.T("superadmin.unbanned", formatID(tgID)), false)

	case "delete_group":
		slug := strings.TrimSpace(arg)
		if slug == "" {
			h.send(ctx, msg.Chat.ID, nil, i18n.T("superadmin.usage_delete_group"), false)
			return
		}
		g, err := h.superadmin.DeleteGroup(ctx, caller, slug)
		if err != nil {
			h.send(ctx, msg.Chat.ID, nil, superadminErrorText(err), false)
			return
		}
		h.send(ctx, msg.Chat.ID, nil,
			i18n.T("superadmin.deleted", i18n.EscapeHTML(g.Slug), i18n.EscapeHTML(g.Title)), false)

	case "stats":
		st, err := h.superadmin.Stats(ctx, caller)
		if err != nil {
			h.send(ctx, msg.Chat.ID, nil, superadminErrorText(err), false)
			return
		}
		h.send(ctx, msg.Chat.ID, nil, statsText(st), false)
	}
}

// parseTelegramIDArg — telegram_id из аргумента команды: только цифры
// (ведущий «+» допускается), ноль и мусор отвергаются.
func parseTelegramIDArg(arg string) (int64, bool) {
	s := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(arg), "+"))
	if s == "" {
		return 0, false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil || id == 0 {
		return 0, false
	}
	return id, true
}

// statsText — сводка /stats (спека §6.1, §7.2 п.4: failed-напоминания видит
// супер-админ).
func statsText(st domain.Stats) string {
	return i18n.T("superadmin.stats",
		formatID(st.Users), formatID(st.GroupsTotal), formatID(st.GroupsActive),
		formatID(st.GroupsPending), formatID(st.DeadlinesActive),
		formatID(st.RemindersPending), formatID(st.RemindersFailed),
		formatID(st.SessionsActive))
}

// formatID — число для текста каталога (аргументы fmt принимают int64 как есть).
func formatID(n int64) string { return strconv.FormatInt(n, 10) }

// superadminErrorText — доменная ошибка → текст в ЛС супер-админу.
func superadminErrorText(err error) string {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return i18n.T("superadmin.error.not_found")
	case errors.Is(err, domain.ErrForbidden):
		return i18n.T("superadmin.error.ban_self")
	default:
		return i18n.T("bot.error.generic")
	}
}
