package bot

import (
	"context"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// StartHandler handles /start command and greets the user in the same thread.
func StartHandler(ctx context.Context, b *tgbot.Bot, update *models.Update) {
	if update == nil || update.Message == nil {
		return
	}

	chatID := update.Message.Chat.ID
	_, _ = b.SendMessage(ctx, &tgbot.SendMessageParams{
		ChatID:          chatID,
		MessageThreadID: update.Message.MessageThreadID,
		Text:            "Привет! Я бот для управления дедлайнами.",
	})
}

// StartHandlerInTopic sends /start response to a specific topic.
func StartHandlerInTopic(ctx context.Context, b *tgbot.Bot, update *models.Update, topicID int) {
	if update == nil || update.Message == nil {
		return
	}

	chatID := update.Message.Chat.ID
	threadID := update.Message.MessageThreadID
	if topicID > 0 {
		threadID = topicID
	}

	_, _ = b.SendMessage(ctx, &tgbot.SendMessageParams{
		ChatID:          chatID,
		MessageThreadID: threadID,
		Text:            "Привет! Я бот для управления дедлайнами.",
	})
}
