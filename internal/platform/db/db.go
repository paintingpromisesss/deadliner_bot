// Package db provides the PostgreSQL connection pool and schema migrations.
package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/sauron/deadliner/migrations"
)

// Connect creates a pgxpool pool for url (max poolMax connections) and
// verifies reachability with a ping bounded by a 5 second timeout.
func Connect(ctx context.Context, url string, poolMax int32) (*pgxpool.Pool, error) {
	if poolMax <= 0 {
		return nil, fmt.Errorf("db: invalid poolMax %d, must be > 0", poolMax)
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("db: parse url: %w", err)
	}
	cfg.MaxConns = poolMax

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("db: new pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("db: ping: %w", err)
	}
	return pool, nil
}

// newMigrate builds a golang-migrate instance over the embedded migrations
// and the postgres driver for url.
func newMigrate(url string) (*migrate.Migrate, error) {
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return nil, fmt.Errorf("db: migration source: %w", err)
	}
	poolCfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("db: parse url: %w", err)
	}
	sqlDB := stdlib.OpenDB(*poolCfg.ConnConfig)
	drv, err := postgres.WithInstance(sqlDB, &postgres.Config{})
	if err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("db: migration driver: %w", err)
	}
	m, err := migrate.NewWithInstance("iofs", src, "postgres", drv)
	if err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("db: new migrate: %w", err)
	}
	return m, nil
}

// RunUp applies all pending migrations from the embedded FS.
func RunUp(ctx context.Context, url string) error {
	return run(ctx, url, func(m *migrate.Migrate) error { return m.Up() })
}

// RunDown rolls back all applied migrations from the embedded FS.
func RunDown(ctx context.Context, url string) error {
	return run(ctx, url, func(m *migrate.Migrate) error { return m.Down() })
}

func run(ctx context.Context, url string, fn func(*migrate.Migrate) error) error {
	m, err := newMigrate(url)
	if err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() {
		var runErr error
		defer func() {
			if closeErr := closeMigrate(m); runErr == nil {
				runErr = closeErr
			}
			done <- runErr
		}()
		err := fn(m)
		if errors.Is(err, migrate.ErrNoChange) {
			err = nil
		}
		if err != nil {
			runErr = fmt.Errorf("db: migrate: %w", err)
		}
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func closeMigrate(m *migrate.Migrate) error {
	srcErr, dbErr := m.Close()
	return errors.Join(srcErr, dbErr)
}

// Version returns the currently applied migration version and whether the
// database is dirty. Version 0 means no migrations applied yet.
func Version(ctx context.Context, url string) (uint, bool, error) {
	m, err := newMigrate(url)
	if err != nil {
		return 0, false, err
	}
	defer closeMigrate(m)
	v, dirty, err := m.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("db: version: %w", err)
	}
	return v, dirty, nil
}
