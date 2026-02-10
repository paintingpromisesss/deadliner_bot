package database

import (
	"fmt"

	"github.com/paintingpromisesss/deadliner_bot/internal/config"
	"github.com/paintingpromisesss/deadliner_bot/internal/models"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// Init opens a database connection and runs auto-migrations.
func Init(cfg *config.Config) (*gorm.DB, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is nil")
	}
	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is empty")
	}

	db, err := gorm.Open(postgres.Open(cfg.DatabaseURL), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	if err := db.AutoMigrate(
		&models.Deadline{},
		&models.Attachment{},
		&models.Reminder{},
	); err != nil {
		return nil, fmt.Errorf("auto migrate: %w", err)
	}

	return db, nil
}
