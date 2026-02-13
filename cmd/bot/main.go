package main

import (
	"context"
	"errors"
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

	srvErrCh := make(chan error, 1)
	botDone := make(chan struct{})

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

	sqlDB, err := db.DB()
	if err != nil {
		log.Fatalf("get sql db: %v", err)
	}
	defer func() {
		if err := sqlDB.Close(); err != nil {
			log.Printf("close database: %v", err)
		}
	}()

	server := api.NewServer(cfg.HttpAddr, cfg.ReadTimeout, cfg.WriteTimeout, "web")
	go func() {
		log.Printf("http server starting on %s", server.Addr)

		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			select {
			case srvErrCh <- err:
			default:
			}
			stop()
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

	go func() {
		log.Printf("bot starting")
		b.Start(ctx)
		close(botDone)
		log.Printf("bot stopped")
	}()

	var srvErr error
	select {
	case <-ctx.Done():
	case srvErr = <-srvErrCh:
		log.Printf("http server error: %v", srvErr)
		stop()
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("http server shutdown: %v", err)
	}
	select {
	case <-botDone:
	case <-time.After(5 * time.Second):
		log.Printf("bot shutdown timeout")
	}
}
