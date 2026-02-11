package bot

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"strings"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// StartHandler handles /start command and greets the user in a private chat only.
func StartHandler(ctx context.Context, b *tgbot.Bot, update *models.Update) {
	if update == nil || update.Message == nil {
		return
	}
	if update.Message.Chat.Type != models.ChatTypePrivate {
		return
	}

	chatID := update.Message.Chat.ID
	_, _ = b.SendMessage(ctx, &tgbot.SendMessageParams{
		ChatID: chatID,
		Text:   "Привет! Чтобы начать работу, добавьте меня в беседу.",
	})
}

// SettingsHandler handles /settings command in group chats.
func SettingsHandler(ctx context.Context, b *tgbot.Bot, update *models.Update, botName string) {
	if update == nil || update.Message == nil {
		return
	}
	chatType := update.Message.Chat.Type
	if chatType != models.ChatTypeGroup && chatType != models.ChatTypeSupergroup {
		return
	}

	chatID := update.Message.Chat.ID
	botName = strings.TrimPrefix(botName, "@")
	if botName == "" {
		_, err := b.SendMessage(ctx, &tgbot.SendMessageParams{
			ChatID: chatID,
			Text:   "Имя бота не настроено.",
		})
		if err != nil {
			log.Printf("send settings message: %v", err)
		}
		return
	}

	payload := url.QueryEscape("settings")
	link := fmt.Sprintf("https://t.me/%s?startapp=%s", botName, payload)

	_, err := b.SendMessage(ctx, &tgbot.SendMessageParams{
		ChatID: chatID,
		Text:   "Откройте Mini App для настроек и управления дедлайнами.",
		ReplyMarkup: &models.InlineKeyboardMarkup{
			InlineKeyboard: [][]models.InlineKeyboardButton{{
				{
					Text: "Открыть Mini App",
					URL:  link,
				},
			}},
		},
	})

	if err != nil {
		log.Printf("send settings message: %v", err)
	}
}
