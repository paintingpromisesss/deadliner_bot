package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	App       App
	DB        DB
	Bot       Bot
	Scheduler Scheduler
	Limits    Limits
}

type App struct {
	PublicURL      string
	SessionTTLDays int
	AuthDateMaxAge time.Duration
	DefaultTZ      string
	LogLevel       string
	LogFormat      string
}

type DB struct {
	URL     string
	PoolMax int
}

type Bot struct {
	Token         string
	APIBase       string
	PollingMode   string
	WebhookURL    string
	WebhookSecret string
	WebhookPort   int
	RateGlobal    int
	RatePerChat   int
}

// PollingModeWebhook — значение POLLING_MODE, включающее webhook-режим.
const PollingModeWebhook = "webhook"

// usesWebhook — режим приёма апдейтов: POLLING_MODE=webhook или заданный
// WEBHOOK_URL. Используется валидацией секрета (fail-closed) и serve.
func (b Bot) usesWebhook() bool {
	return b.PollingMode == PollingModeWebhook || b.WebhookURL != ""
}

type Scheduler struct {
	PollInterval time.Duration
	Batch        int
	LockTTL      time.Duration
	MaxAttempts  int
}

type Limits struct {
	GroupPendingTTL      time.Duration
	GroupCreateDay       int
	GroupCreateWeek      int
	ClaimPerChatHour     int
	ClaimCodeTTL         time.Duration
	InviteDefaultTTLDays int
	SlugRegex            string
}

type loader struct {
	errs []error
}

func (l *loader) string(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func (l *loader) int(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		l.errs = append(l.errs, fmt.Errorf("%s: invalid integer %q", key, v))
		return def
	}
	return n
}

func (l *loader) duration(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		l.errs = append(l.errs, fmt.Errorf("%s: invalid duration %q", key, v))
		return def
	}
	return d
}

func (l *loader) hours(key string, def int) time.Duration {
	return time.Duration(l.int(key, def)) * time.Hour
}

func (l *loader) days(key string, def int) time.Duration {
	return time.Duration(l.int(key, def)) * 24 * time.Hour
}

func Load() (*Config, error) {
	l := &loader{}

	cfg := &Config{
		App: App{
			PublicURL:      os.Getenv("APP_PUBLIC_URL"),
			SessionTTLDays: l.int("SESSION_TTL_DAYS", 30),
			AuthDateMaxAge: l.hours("AUTH_DATE_MAX_AGE_HOURS", 24),
			DefaultTZ:      l.string("DEFAULT_TZ", "Europe/Moscow"),
			LogLevel:       l.string("LOG_LEVEL", "info"),
			LogFormat:      l.string("LOG_FORMAT", "json"),
		},
		DB: DB{
			URL:     os.Getenv("DATABASE_URL"),
			PoolMax: l.int("DB_POOL_MAX", 10),
		},
		Bot: Bot{
			Token:         os.Getenv("BOT_TOKEN"),
			APIBase:       os.Getenv("BOT_API_BASE"),
			PollingMode:   l.string("POLLING_MODE", "long_polling"),
			WebhookURL:    os.Getenv("WEBHOOK_URL"),
			WebhookSecret: os.Getenv("WEBHOOK_SECRET"),
			WebhookPort:   l.int("WEBHOOK_PORT", 0),
			RateGlobal:    l.int("TG_RATE_GLOBAL", 25),
			RatePerChat:   l.int("TG_RATE_PER_CHAT", 18),
		},
		Scheduler: Scheduler{
			PollInterval: l.duration("SCHED_POLL_INTERVAL", 10*time.Second),
			Batch:        l.int("SCHED_BATCH", 50),
			LockTTL:      l.duration("SCHED_LOCK_TTL", 2*time.Minute),
			MaxAttempts:  l.int("SCHED_MAX_ATTEMPTS", 5),
		},
		Limits: Limits{
			GroupPendingTTL:      l.days("GROUP_PENDING_TTL_DAYS", 14),
			GroupCreateDay:       l.int("LIMIT_GROUP_CREATE_DAY", 3),
			GroupCreateWeek:      l.int("LIMIT_GROUP_CREATE_WEEK", 5),
			ClaimPerChatHour:     l.int("LIMIT_CLAIM_PER_CHAT_HOUR", 3),
			ClaimCodeTTL:         l.duration("CLAIM_CODE_TTL", 10*time.Minute),
			InviteDefaultTTLDays: l.int("INVITE_DEFAULT_TTL_DAYS", 7),
			SlugRegex:            l.string("SLUG_REGEX", `^[А-ЯA-Z0-9]+(-[А-ЯA-Z0-9]+)*$`),
		},
	}

	if cfg.Bot.Token == "" {
		l.errs = append(l.errs, errors.New("missing required env var BOT_TOKEN"))
	}
	if cfg.DB.URL == "" {
		l.errs = append(l.errs, errors.New("missing required env var DATABASE_URL"))
	}
	// Fail-closed: webhook-режим без секрета принимает ЛЮБОЙ POST /webhook
	// (библиотека сверяет заголовок только при непустом секрете) — то есть
	// подделанные апдейты, включая /bind_group в чужом чате. Режим webhook
	// определяется POLLING_MODE=webhook либо непустым WEBHOOK_URL.
	if cfg.Bot.usesWebhook() && cfg.Bot.WebhookSecret == "" {
		l.errs = append(l.errs, errors.New(
			"missing required env var WEBHOOK_SECRET: webhook mode without a secret token accepts forged updates"))
	}

	if len(l.errs) > 0 {
		return nil, errors.Join(l.errs...)
	}
	return cfg, nil
}
