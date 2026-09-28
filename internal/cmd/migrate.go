package cmd

import (
	"context"
	"errors"
	"log/slog"

	"github.com/sauron/deadliner/internal/config"
	"github.com/sauron/deadliner/internal/platform/db"
)

// Migrate applies all pending database migrations from the embedded
// migrations FS and logs the resulting schema version.
func Migrate(ctx context.Context, cfg *config.Config, log *slog.Logger) error {
	if cfg.DB.URL == "" {
		return errors.New("migrate: DATABASE_URL is not set")
	}

	log.Info("migrate: applying migrations")
	if err := db.RunUp(ctx, cfg.DB.URL); err != nil {
		return err
	}

	version, dirty, err := db.Version(ctx, cfg.DB.URL)
	if err != nil {
		return err
	}
	log.Info("migrate: done", slog.Uint64("version", uint64(version)), slog.Bool("dirty", dirty))
	return nil
}
