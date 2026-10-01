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
	Cleanup   Cleanup
}

type App struct {
	// HTTPAddr — HTTP_ADDR: адрес прослушивания HTTP-сервера serve (API, TMA,
	// webhook и /healthz живут на одном порту). Дефолт ":8080" — он же порт
	// контейнера в docker-compose и проброс наружу.
	HTTPAddr       string
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

// UsesWebhook — режим приёма апдейтов: POLLING_MODE=webhook или заданный
// WEBHOOK_URL. Используется валидацией секрета (fail-closed) и serve.
func (b Bot) UsesWebhook() bool {
	return b.PollingMode == PollingModeWebhook || b.WebhookURL != ""
}

type Scheduler struct {
	PollInterval time.Duration
	Batch        int
	LockTTL      time.Duration
	MaxAttempts  int
}

// Cleanup — параметры cleanup-джобы pending-групп (спека §3.3, §8).
type Cleanup struct {
	// Interval — CLEANUP_INTERVAL: период прогона автоудаления протухших
	// pending-групп и служебной уборки (дефолт 1h — спека §12 п.7).
	Interval time.Duration
}

// longestLimitWindow — самое длинное окно rate-limit-счётчика
// (group_create_week в groups.Service: 168 часов). Retention уборки обязан
// быть строго больше него, иначе cleanup удалит ЖИВУЮ строку недельного
// лимита и LIMIT_GROUP_CREATE_WEEK молча перестанет срабатывать.
const longestLimitWindow = 168 * time.Hour

type Limits struct {
	GroupPendingTTL      time.Duration
	GroupCreateDay       int
	GroupCreateWeek      int
	ClaimPerChatHour     int
	ClaimCodeTTL         time.Duration
	InviteDefaultTTLDays int
	SlugRegex            string
	// CounterRetention — COUNTER_RETENTION: сколько живёт окно rate-limit-
	// счётчика после его начала (дефолт 192ч). Должен быть больше самого
	// длинного окна (longestLimitWindow) — валидируется в Load, потому что
	// оператор, повысивший окно, обязан повысить и retention.
	CounterRetention time.Duration
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
			HTTPAddr:       l.string("HTTP_ADDR", ":8080"),
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
		Cleanup: Cleanup{
			Interval: l.duration("CLEANUP_INTERVAL", time.Hour),
		},
		Limits: Limits{
			GroupPendingTTL:      l.days("GROUP_PENDING_TTL_DAYS", 14),
			GroupCreateDay:       l.int("LIMIT_GROUP_CREATE_DAY", 3),
			GroupCreateWeek:      l.int("LIMIT_GROUP_CREATE_WEEK", 5),
			ClaimPerChatHour:     l.int("LIMIT_CLAIM_PER_CHAT_HOUR", 3),
			ClaimCodeTTL:         l.duration("CLAIM_CODE_TTL", 10*time.Minute),
			InviteDefaultTTLDays: l.int("INVITE_DEFAULT_TTL_DAYS", 7),
			SlugRegex:            l.string("SLUG_REGEX", `^[А-ЯA-Z0-9]+(-[А-ЯA-Z0-9]+)*$`),
			CounterRetention:     l.duration("COUNTER_RETENTION", 8*24*time.Hour),
		},
	}

	if cfg.Bot.Token == "" {
		l.errs = append(l.errs, errors.New("missing required env var BOT_TOKEN"))
	}
	if cfg.DB.URL == "" {
		l.errs = append(l.errs, errors.New("missing required env var DATABASE_URL"))
	}
	if cfg.Bot.PollingMode != "long_polling" && !cfg.Bot.UsesWebhook() {
		l.errs = append(l.errs, fmt.Errorf(
			"POLLING_MODE=%q is not a known mode (use %q or %q)",
			cfg.Bot.PollingMode, "long_polling", PollingModeWebhook))
	}
	// Fail-closed: webhook-режим без секрета принимает ЛЮБОЙ POST /webhook
	// (библиотека сверяет заголовок только при непустом секрете) — то есть
	// подделанные апдейты, включая /bind_group в чужом чате. Режим webhook
	// определяется POLLING_MODE=webhook либо непустым WEBHOOK_URL.
	if cfg.Bot.UsesWebhook() && cfg.Bot.WebhookSecret == "" {
		l.errs = append(l.errs, errors.New(
			"missing required env var WEBHOOK_SECRET: webhook mode without a secret token accepts forged updates"))
	}
	// Fail-closed: POLLING_MODE=webhook без WEBHOOK_URL — процесс, который
	// стартует и молча не принимает апдейты: регистрировать вебхук некуда,
	// getUpdates тоже не запускается. Ошибка на старте громче и дешевле, чем
	// «бот жив, но молчит» в проде. Пустой PollingMode сюда не попадает:
	// webhook-режим тогда определяет сам WEBHOOK_URL, а он непуст по условию.
	if cfg.Bot.PollingMode == PollingModeWebhook && cfg.Bot.WebhookURL == "" {
		l.errs = append(l.errs, errors.New(
			"missing required env var WEBHOOK_URL: POLLING_MODE=webhook without a webhook URL "+
				"starts a process that receives no updates"))
	}
	// Retention уборки счётчиков обязан быть больше самого длинного окна
	// лимита (168ч = неделя): окно floor-ится на своё начало, поэтому живая
	// строка недельного счётчика может быть почти 168 часов от роду, и
	// retention ≤ 168ч удалял бы её — лимит «5 групп в неделю» молча
	// переставал бы срабатывать.
	if cfg.Limits.CounterRetention <= longestLimitWindow {
		l.errs = append(l.errs, fmt.Errorf(
			"COUNTER_RETENTION=%s must exceed the longest rate-limit window (%s, week limit): "+
				"a shorter retention purges the live weekly counter row",
			cfg.Limits.CounterRetention, longestLimitWindow))
	}

	if len(l.errs) > 0 {
		return nil, errors.Join(l.errs...)
	}
	return cfg, nil
}
