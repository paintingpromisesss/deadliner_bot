package telegram

import (
	"context"
	"fmt"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// BotSender — адаптер go-telegram/bot под интерфейсы Sender (нотификатор),
// MessageSender (отправка с кнопкой) и ChatAdminChecker (проверка админства).
// Единственное место, где ошибки Telegram превращаются в доменные (429/403).
type BotSender struct {
	api *tgbot.Bot
}

var (
	_ Sender           = (*BotSender)(nil)
	_ MessageSender    = (*BotSender)(nil)
	_ ChatAdminChecker = (*BotSender)(nil)
)

// SendMessage — минимальный контракт нотификатора.
func (s *BotSender) SendMessage(ctx context.Context, chatID int64, threadID *int64, text string, linkPreviewOff bool) (int64, error) {
	return s.Send(ctx, OutMessage{
		ChatID: chatID, ThreadID: threadID, Text: text, LinkPreviewOff: linkPreviewOff,
	})
}

// Send отправляет OutMessage и возвращает message_id. Текст уходит с
// parse_mode=HTML (спека §6.2: шаблоны каталога содержат <b>), поэтому все
// пользовательские подстановки обязаны быть экранированы вызывающей стороной —
// scheduler.escapeHTML и claims.htmlEscape делают это на своих путях. Ошибки
// маппятся TelegramError (429/403 → доменные).
func (s *BotSender) Send(ctx context.Context, m OutMessage) (int64, error) {
	params := &tgbot.SendMessageParams{
		ChatID:    m.ChatID,
		Text:      m.Text,
		ParseMode: models.ParseModeHTML,
	}
	if m.ThreadID != nil && *m.ThreadID != 0 {
		// В SendMessageParams поле int с omitempty: в топик форума нужно
		// непустое значение, иначе сообщение уйдёт в General (и claim-код
		// окажется не в том топике).
		params.MessageThreadID = int(*m.ThreadID)
	}
	if m.LinkPreviewOff {
		off := true
		params.LinkPreviewOptions = &models.LinkPreviewOptions{IsDisabled: &off}
	}
	if m.ButtonURL != "" && m.ButtonText != "" {
		button := models.InlineKeyboardButton{Text: m.ButtonText}

		// web_app-кнопки разрешены только в ЛС: в группах Telegram отвечает
		// 400 BUTTON_TYPE_INVALID, поэтому там используется url-кнопка на тот
		// же TMA-адрес.
		if m.ChatID > 0 {
			button.WebApp = &models.WebAppInfo{URL: m.ButtonURL}
		} else {
			button.URL = m.ButtonURL
		}
		params.ReplyMarkup = models.InlineKeyboardMarkup{
			InlineKeyboard: [][]models.InlineKeyboardButton{{button}},
		}
	}

	msg, err := s.api.SendMessage(ctx, params)
	if err != nil {
		return 0, TelegramError(err, m.ChatID)
	}
	if msg == nil {
		return 0, nil
	}
	return int64(msg.ID), nil
}

// IsChatAdmin — статус участника чата: creator/administrator.
func (s *BotSender) IsChatAdmin(ctx context.Context, chatID, userID int64) (bool, error) {
	if userID == 0 {
		// Идентификатор бота неизвестен (getMe не вызывался) — безопаснее
		// считать «не админ», чем разрешить привязку без проверки.
		return false, nil
	}
	member, err := s.api.GetChatMember(ctx, &tgbot.GetChatMemberParams{
		ChatID: chatID,
		UserID: userID,
	})
	if err != nil {
		return false, TelegramError(err, chatID)
	}
	if member == nil {
		return false, nil
	}
	return member.Type == models.ChatMemberTypeOwner ||
		member.Type == models.ChatMemberTypeAdministrator, nil
}

// SetCommands — setMyCommands в двух scope (спека §6.1: personal +
// all_chat_administrators): пользователи видят клиентские команды, а
// администраторы чатов — ещё и команды привязки, которыми они распоряжаются.
func (s *BotSender) SetCommands(ctx context.Context, cmds []BotCommand) error {
	all := make([]models.BotCommand, 0, len(cmds))
	adminOnly := make([]models.BotCommand, 0, len(cmds))
	for _, c := range cmds {
		bc := models.BotCommand{Command: c.Command, Description: c.Description}
		all = append(all, bc)
		if isAdminCommand(c.Command) {
			adminOnly = append(adminOnly, bc)
		}
	}

	scopes := []struct {
		name  string
		cmds  []models.BotCommand
		scope models.BotCommandScope
	}{
		{"default", all, &models.BotCommandScopeDefault{}},
		{"all_chat_administrators", adminOnly, &models.BotCommandScopeAllChatAdministrators{}},
	}
	for _, sc := range scopes {
		if _, err := s.api.SetMyCommands(ctx, &tgbot.SetMyCommandsParams{
			Commands: sc.cmds,
			Scope:    sc.scope,
		}); err != nil {
			return fmt.Errorf("telegram: setMyCommands (%s): %w", sc.name, err)
		}
	}
	return nil
}

// isAdminCommand — команды, которые имеет смысл показывать только
// администраторам чатов (привязка/отвязка чата, спека §6.1).
func isAdminCommand(cmd string) bool {
	return cmd == "bind_group" || cmd == "unbind"
}

// SetMenuButton — menu button типа web_app (спека §6.1).
func (s *BotSender) SetMenuButton(ctx context.Context, text, url string) error {
	if _, err := s.api.SetChatMenuButton(ctx, &tgbot.SetChatMenuButtonParams{
		MenuButton: &models.MenuButtonWebApp{
			Text:   text,
			WebApp: models.WebAppInfo{URL: url},
		},
	}); err != nil {
		return fmt.Errorf("telegram: setChatMenuButton: %w", err)
	}
	return nil
}

// BotCommand — команда для setMyCommands (описание локализовано i18n).
type BotCommand struct {
	Command     string
	Description string
}
