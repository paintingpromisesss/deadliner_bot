package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	botpkg "github.com/paintingpromisesss/deadliner_bot/internal/bot"
	"github.com/paintingpromisesss/deadliner_bot/internal/config"
	"github.com/paintingpromisesss/deadliner_bot/internal/database"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	log.Printf("config loaded, env=%s, http=%s", cfg.Env, cfg.HttpAddr)

	if cfg.Location != nil {
		time.Local = cfg.Location
	}

	db, err := database.Init(cfg)
	if err != nil {
		log.Fatalf("init database: %v", err)
	}
	_ = db
	log.Printf("database connected")

	b, err := bot.New(
		cfg.BotToken,
		bot.WithDefaultHandler(func(ctx context.Context, b *bot.Bot, update *models.Update) {
			_ = b
			_ = update
		}),
		bot.WithMessageTextHandler("/start", bot.MatchTypeExact, func(ctx context.Context, b *bot.Bot, update *models.Update) {
			if update == nil || update.Message == nil {
				return
			}
			if cfg.DeadlineTopicID > 0 && update.Message.MessageThreadID != cfg.DeadlineTopicID {
				return
			}
			botpkg.StartHandler(ctx, b, update)
		}),
	)
	if err != nil {
		log.Fatalf("init bot: %v", err)
	}

	log.Printf("bot starting")
	b.Start(ctx)
	log.Printf("bot stopped")
}
