package telegram

import (
	"context"

	"github.com/go-telegram/bot/models"

	"github.com/sauron/deadliner/internal/i18n"
)

const addDeadlineHash = "#/add"

// handleNewDeadline — /new_deadline в ЛС: форма живёт в TMA, поэтому бот
// отвечает подсказкой с web_app-кнопкой прямо на форму (§6.1, deeplink #add).
// В группе команда бессмысленна (личная форма) — отвечаем как /groups.
func (h *Handlers) handleNewDeadline(ctx context.Context, msg *models.Message) {
	if !isPrivate(msg.Chat.Type) {
		h.send(ctx, msg.Chat.ID, threadIDOf(msg), i18n.T("bot.command.private_only"), false)
		return
	}
	if h.appURL == "" {
		// Кнопку отдавать нечем (APP_PUBLIC_URL не задан) — текст без ссылки:
		// пустую web_app-кнопку Telegram отвергнет (400).
		h.send(ctx, msg.Chat.ID, nil, i18n.T("bot.new_deadline"), false)
		return
	}
	h.deliver(ctx, OutMessage{
		ChatID: msg.Chat.ID, Text: i18n.Plain(i18n.T("bot.new_deadline")),
		ButtonText: i18n.T("bot.button.add_deadline"),
		ButtonURL:  h.appURL + addDeadlineHash,
		// Превью ссылки не нужно: цель ведёт кнопка.
		LinkPreviewOff: true,
	})
}
