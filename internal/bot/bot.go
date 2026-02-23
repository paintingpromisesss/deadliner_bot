package bot

import (
	"context"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// New creates a Telegram bot instance and registers command handlers.
func New(token, botName string) (*tgbot.Bot, error) {
	return tgbot.New(
		token,
		tgbot.WithDefaultHandler(func(ctx context.Context, b *tgbot.Bot, update *models.Update) {
			_ = b
			_ = update
		}),
		tgbot.WithMessageTextHandler("/start", tgbot.MatchTypePrefix, StartHandler),
		tgbot.WithMessageTextHandler("/settings", tgbot.MatchTypePrefix, func(ctx context.Context, b *tgbot.Bot, update *models.Update) {
			SettingsHandler(ctx, b, update, botName)
		}),
	)
}
