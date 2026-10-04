package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/sauron/deadliner/internal/cmd"
	"github.com/sauron/deadliner/internal/config"
	"github.com/sauron/deadliner/internal/i18n"
	"github.com/sauron/deadliner/internal/platform/logx"
)

func main() {
	mode := "serve"
	if len(os.Args) > 1 {
		mode = os.Args[1]
	}

	switch mode {
	case "serve", "migrate", "admin":
	default:
		fmt.Fprintf(os.Stderr, "unknown mode %q\nusage: deadliner [serve|migrate|admin]\n", mode)
		os.Exit(2)
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		os.Exit(1)
	}

	log := logx.New(cfg.App.LogLevel, cfg.App.LogFormat)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	switch mode {
	case "serve":
		// Каталог строк должен быть загружен до сборки роутера.
		i18n.MustLoad(i18n.Locales)
		if err := cmd.Serve(ctx, cfg, log); err != nil {
			log.Error("serve failed", slog.String("error", err.Error()))
			os.Exit(1)
		}
		return
	case "migrate":
		if err := cmd.Migrate(ctx, cfg, log); err != nil {
			log.Error("migrate failed", slog.String("error", err.Error()))
			os.Exit(1)
		}
		return
	case "admin":
		if err := cmd.Admin(ctx, adminArgs(os.Args), log); err != nil {
			// CLI печатает детали сам; main выставляет код (1 — ошибка
			// операции, 2 — ошибка использования).
			os.Exit(cmd.ExitCode(err))
		}
		return
	}

	<-ctx.Done()
	log.Info("shutdown signal received")
}

// adminArgs — аргументы подрежима admin (всё после «admin»).
func adminArgs(args []string) []string {
	if len(args) >= 2 {
		return args[2:]
	}
	return nil
}
