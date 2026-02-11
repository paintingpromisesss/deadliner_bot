package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/paintingpromisesss/deadliner_bot/internal/api"
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

	server := api.NewServer(cfg.HttpAddr, cfg.ReadTimeout, cfg.WriteTimeout, "web")
	go func() {
		log.Printf("http server starting on %s", cfg.HttpAddr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http server error: %v", err)
		}
	}()

	b, err := bot.New(
		cfg.BotToken,
		bot.WithDefaultHandler(func(ctx context.Context, b *bot.Bot, update *models.Update) {
			_ = b
			_ = update
		}),
		bot.WithMessageTextHandler("/start", bot.MatchTypePrefix, botpkg.StartHandler),
		bot.WithMessageTextHandler("/settings", bot.MatchTypePrefix, func(ctx context.Context, b *bot.Bot, update *models.Update) {
			botpkg.SettingsHandler(ctx, b, update, cfg.BotName)
		}),
	)
	if err != nil {
		log.Fatalf("init bot: %v", err)
	}

	log.Printf("bot starting")
	b.Start(ctx)
	log.Printf("bot stopped")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("http server shutdown: %v", err)
	}
}
