package config

import (
	"fmt"
	"time"

	"github.com/joho/godotenv"
	"github.com/spf13/viper"
)

// Config holds all environment-based settings for the application.
type Config struct {
	Env             string         `mapstructure:"APP_ENV"`
	BotToken        string         `mapstructure:"BOT_TOKEN"`
	BotName         string         `mapstructure:"BOT_NAME"`
	WebAppURL       string         `mapstructure:"WEBAPP_URL"`
	DatabaseURL     string         `mapstructure:"DATABASE_URL"`
	DBHost          string         `mapstructure:"DB_HOST"`
	DBPort          string         `mapstructure:"DB_PORT"`
	DBUser          string         `mapstructure:"DB_USER"`
	DBPassword      string         `mapstructure:"DB_PASSWORD"`
	DBName          string         `mapstructure:"DB_NAME"`
	DBSSLMode       string         `mapstructure:"DB_SSL_MODE"`
	HttpAddr        string         `mapstructure:"HTTP_ADDR"`
	CronSpec        string         `mapstructure:"CRON_SPEC"`
	TelegramDumpID  int64          `mapstructure:"TELEGRAM_DUMP_CHAT_ID"`
	DeadlineTopicID int            `mapstructure:"DEADLINE_TOPIC_ID"`
	Timezone        string         `mapstructure:"TZ"`
	Location        *time.Location `mapstructure:"-"`
	ReadTimeout     time.Duration  `mapstructure:"HTTP_READ_TIMEOUT"`
	WriteTimeout    time.Duration  `mapstructure:"HTTP_WRITE_TIMEOUT"`
}

// Load reads environment variables into Config using Viper.
func Load() (*Config, error) {
	_ = godotenv.Load()

	v := viper.New()
	v.SetEnvPrefix("")
	v.AutomaticEnv()

	keys := []string{
		"APP_ENV",
		"BOT_TOKEN",
		"BOT_NAME",
		"WEBAPP_URL",
		"DATABASE_URL",
		"DB_HOST",
		"DB_PORT",
		"DB_USER",
		"DB_PASSWORD",
		"DB_NAME",
		"DB_SSL_MODE",
		"HTTP_ADDR",
		"CRON_SPEC",
		"TELEGRAM_DUMP_CHAT_ID",
		"DEADLINE_TOPIC_ID",
		"TZ",
		"HTTP_READ_TIMEOUT",
		"HTTP_WRITE_TIMEOUT",
	}
	for _, key := range keys {
		_ = v.BindEnv(key)
		v.SetDefault(key, "")
	}

	v.SetDefault("APP_ENV", "development")
	v.SetDefault("HTTP_ADDR", ":8080")
	v.SetDefault("CRON_SPEC", "@every 1m")
	v.SetDefault("TZ", "Europe/Moscow")
	v.SetDefault("HTTP_READ_TIMEOUT", "10s")
	v.SetDefault("HTTP_WRITE_TIMEOUT", "10s")
	v.SetDefault("DB_HOST", "localhost")
	v.SetDefault("DB_PORT", "5432")
	v.SetDefault("DB_SSL_MODE", "disable")

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, err
	}

	if cfg.BotToken == "" {
		return nil, fmt.Errorf("BOT_TOKEN is empty")
	}
	if cfg.BotName == "" {
		return nil, fmt.Errorf("BOT_NAME is empty")
	}
	if cfg.DatabaseURL == "" {
		if cfg.DBHost == "" || cfg.DBPort == "" || cfg.DBUser == "" || cfg.DBPassword == "" || cfg.DBName == "" {
			return nil, fmt.Errorf("DATABASE_URL is empty and DB_* is incomplete")
		}
		cfg.DatabaseURL = fmt.Sprintf(
			"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
			cfg.DBHost,
			cfg.DBPort,
			cfg.DBUser,
			cfg.DBPassword,
			cfg.DBName,
			cfg.DBSSLMode,
		)
	}
	if cfg.Timezone != "" {
		loc, err := time.LoadLocation(cfg.Timezone)
		if err != nil {
			return nil, fmt.Errorf("invalid TZ: %w", err)
		}
		cfg.Location = loc
	}

	return &cfg, nil
}
