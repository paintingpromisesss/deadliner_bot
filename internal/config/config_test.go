package config_test

import (
	"strings"
	"testing"
	"time"

	"github.com/sauron/deadliner/internal/config"
)

func setRequired(t *testing.T) {
	t.Helper()
	t.Setenv("BOT_TOKEN", "123456:ABC")
	t.Setenv("DATABASE_URL", "postgres://deadliner:deadliner@localhost:5432/deadliner?sslmode=disable")
}

func TestLoadDefaults(t *testing.T) {
	setRequired(t)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	if cfg.Bot.Token != "123456:ABC" {
		t.Errorf("Bot.Token = %q, want %q", cfg.Bot.Token, "123456:ABC")
	}
	if cfg.DB.URL != "postgres://deadliner:deadliner@localhost:5432/deadliner?sslmode=disable" {
		t.Errorf("DB.URL = %q, want %q", cfg.DB.URL, "postgres://deadliner:deadliner@localhost:5432/deadliner?sslmode=disable")
	}
	if cfg.DB.PoolMax != 10 {
		t.Errorf("DB.PoolMax = %d, want 10", cfg.DB.PoolMax)
	}
	if cfg.App.HTTPAddr != ":8080" {
		t.Errorf("App.HTTPAddr = %q, want %q", cfg.App.HTTPAddr, ":8080")
	}
	if cfg.Bot.PollingMode != "long_polling" {
		t.Errorf("Bot.PollingMode = %q, want %q", cfg.Bot.PollingMode, "long_polling")
	}
	if cfg.App.SessionTTLDays != 30 {
		t.Errorf("App.SessionTTLDays = %d, want 30", cfg.App.SessionTTLDays)
	}
	if cfg.App.AuthDateMaxAge != 24*time.Hour {
		t.Errorf("App.AuthDateMaxAge = %v, want %v", cfg.App.AuthDateMaxAge, 24*time.Hour)
	}
	if cfg.Scheduler.PollInterval != 10*time.Second {
		t.Errorf("Scheduler.PollInterval = %v, want %v", cfg.Scheduler.PollInterval, 10*time.Second)
	}
	if cfg.Scheduler.Batch != 50 {
		t.Errorf("Scheduler.Batch = %d, want 50", cfg.Scheduler.Batch)
	}
	if cfg.Scheduler.LockTTL != 2*time.Minute {
		t.Errorf("Scheduler.LockTTL = %v, want %v", cfg.Scheduler.LockTTL, 2*time.Minute)
	}
	if cfg.Scheduler.MaxAttempts != 5 {
		t.Errorf("Scheduler.MaxAttempts = %d, want 5", cfg.Scheduler.MaxAttempts)
	}
	if cfg.Cleanup.Interval != time.Hour {
		t.Errorf("Cleanup.Interval = %v, want %v", cfg.Cleanup.Interval, time.Hour)
	}
	// Ретенция счётчиков обязана быть больше недельного окна лимита (168ч):
	// иначе cleanup удалит живую строку LIMIT_GROUP_CREATE_WEEK.
	if cfg.Limits.CounterRetention != 192*time.Hour {
		t.Errorf("Limits.CounterRetention = %v, want 192h", cfg.Limits.CounterRetention)
	}
	if cfg.Limits.CounterRetention <= 168*time.Hour {
		t.Errorf("Limits.CounterRetention = %v, must exceed the 168h week window", cfg.Limits.CounterRetention)
	}
	if cfg.Bot.RateGlobal != 25 {
		t.Errorf("Bot.RateGlobal = %d, want 25", cfg.Bot.RateGlobal)
	}
	if cfg.Bot.RatePerChat != 18 {
		t.Errorf("Bot.RatePerChat = %d, want 18", cfg.Bot.RatePerChat)
	}
	if cfg.Limits.GroupPendingTTL != 14*24*time.Hour {
		t.Errorf("Limits.GroupPendingTTL = %v, want %v", cfg.Limits.GroupPendingTTL, 14*24*time.Hour)
	}
	if cfg.Limits.GroupCreateDay != 3 {
		t.Errorf("Limits.GroupCreateDay = %d, want 3", cfg.Limits.GroupCreateDay)
	}
	if cfg.Limits.GroupCreateWeek != 5 {
		t.Errorf("Limits.GroupCreateWeek = %d, want 5", cfg.Limits.GroupCreateWeek)
	}
	if cfg.Limits.ClaimPerChatHour != 3 {
		t.Errorf("Limits.ClaimPerChatHour = %d, want 3", cfg.Limits.ClaimPerChatHour)
	}
	if cfg.Limits.ClaimCodeTTL != 10*time.Minute {
		t.Errorf("Limits.ClaimCodeTTL = %v, want %v", cfg.Limits.ClaimCodeTTL, 10*time.Minute)
	}
	if cfg.Limits.InviteDefaultTTLDays != 7 {
		t.Errorf("Limits.InviteDefaultTTLDays = %d, want 7", cfg.Limits.InviteDefaultTTLDays)
	}
	if cfg.Limits.SlugRegex != `^[А-ЯA-Z0-9]+(-[А-ЯA-Z0-9]+)*$` {
		t.Errorf("Limits.SlugRegex = %q, want default", cfg.Limits.SlugRegex)
	}
	if cfg.App.DefaultTZ != "Europe/Moscow" {
		t.Errorf("App.DefaultTZ = %q, want %q", cfg.App.DefaultTZ, "Europe/Moscow")
	}
	if cfg.App.LogLevel != "info" {
		t.Errorf("App.LogLevel = %q, want %q", cfg.App.LogLevel, "info")
	}
	if cfg.App.LogFormat != "json" {
		t.Errorf("App.LogFormat = %q, want %q", cfg.App.LogFormat, "json")
	}
}

func TestLoadOverrides(t *testing.T) {
	setRequired(t)
	t.Setenv("BOT_API_BASE", "http://localhost:8081")
	t.Setenv("HTTP_ADDR", "127.0.0.1:9090")
	t.Setenv("WEBHOOK_URL", "https://example.com/webhook")
	t.Setenv("WEBHOOK_SECRET", "s3cret")
	t.Setenv("WEBHOOK_PORT", "8443")
	t.Setenv("POLLING_MODE", "webhook")
	t.Setenv("APP_PUBLIC_URL", "https://app.example.com")
	t.Setenv("DB_POOL_MAX", "25")
	t.Setenv("SESSION_TTL_DAYS", "7")
	t.Setenv("AUTH_DATE_MAX_AGE_HOURS", "12")
	t.Setenv("SCHED_POLL_INTERVAL", "30s")
	t.Setenv("SCHED_BATCH", "100")
	t.Setenv("SCHED_LOCK_TTL", "5m")
	t.Setenv("SCHED_MAX_ATTEMPTS", "3")
	t.Setenv("CLEANUP_INTERVAL", "15m")
	t.Setenv("COUNTER_RETENTION", "240h")
	t.Setenv("TG_RATE_GLOBAL", "20")
	t.Setenv("TG_RATE_PER_CHAT", "15")
	t.Setenv("GROUP_PENDING_TTL_DAYS", "30")
	t.Setenv("LIMIT_GROUP_CREATE_DAY", "1")
	t.Setenv("LIMIT_GROUP_CREATE_WEEK", "2")
	t.Setenv("LIMIT_CLAIM_PER_CHAT_HOUR", "9")
	t.Setenv("CLAIM_CODE_TTL", "1h")
	t.Setenv("INVITE_DEFAULT_TTL_DAYS", "3")
	t.Setenv("SLUG_REGEX", `^[A-Z]+$`)
	t.Setenv("DEFAULT_TZ", "UTC")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("LOG_FORMAT", "text")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	if cfg.Bot.APIBase != "http://localhost:8081" {
		t.Errorf("Bot.APIBase = %q", cfg.Bot.APIBase)
	}
	if cfg.Bot.WebhookURL != "https://example.com/webhook" {
		t.Errorf("Bot.WebhookURL = %q", cfg.Bot.WebhookURL)
	}
	if cfg.Bot.WebhookSecret != "s3cret" {
		t.Errorf("Bot.WebhookSecret = %q", cfg.Bot.WebhookSecret)
	}
	if cfg.Bot.WebhookPort != 8443 {
		t.Errorf("Bot.WebhookPort = %d", cfg.Bot.WebhookPort)
	}
	if cfg.Bot.PollingMode != "webhook" {
		t.Errorf("Bot.PollingMode = %q", cfg.Bot.PollingMode)
	}
	if cfg.Bot.RateGlobal != 20 || cfg.Bot.RatePerChat != 15 {
		t.Errorf("rates = %d/%d", cfg.Bot.RateGlobal, cfg.Bot.RatePerChat)
	}
	if cfg.App.PublicURL != "https://app.example.com" {
		t.Errorf("App.PublicURL = %q", cfg.App.PublicURL)
	}
	if cfg.App.HTTPAddr != "127.0.0.1:9090" {
		t.Errorf("App.HTTPAddr = %q, want 127.0.0.1:9090", cfg.App.HTTPAddr)
	}
	if cfg.DB.PoolMax != 25 {
		t.Errorf("DB.PoolMax = %d", cfg.DB.PoolMax)
	}
	if cfg.App.SessionTTLDays != 7 {
		t.Errorf("App.SessionTTLDays = %d", cfg.App.SessionTTLDays)
	}
	if cfg.App.AuthDateMaxAge != 12*time.Hour {
		t.Errorf("App.AuthDateMaxAge = %v", cfg.App.AuthDateMaxAge)
	}
	if cfg.Scheduler.PollInterval != 30*time.Second {
		t.Errorf("Scheduler.PollInterval = %v", cfg.Scheduler.PollInterval)
	}
	if cfg.Scheduler.Batch != 100 {
		t.Errorf("Scheduler.Batch = %d", cfg.Scheduler.Batch)
	}
	if cfg.Scheduler.LockTTL != 5*time.Minute {
		t.Errorf("Scheduler.LockTTL = %v", cfg.Scheduler.LockTTL)
	}
	if cfg.Scheduler.MaxAttempts != 3 {
		t.Errorf("Scheduler.MaxAttempts = %d", cfg.Scheduler.MaxAttempts)
	}
	if cfg.Cleanup.Interval != 15*time.Minute {
		t.Errorf("Cleanup.Interval = %v, want 15m", cfg.Cleanup.Interval)
	}
	if cfg.Limits.CounterRetention != 240*time.Hour {
		t.Errorf("Limits.CounterRetention = %v, want 240h", cfg.Limits.CounterRetention)
	}
	if cfg.Limits.GroupPendingTTL != 30*24*time.Hour {
		t.Errorf("Limits.GroupPendingTTL = %v", cfg.Limits.GroupPendingTTL)
	}
	if cfg.Limits.GroupCreateDay != 1 || cfg.Limits.GroupCreateWeek != 2 || cfg.Limits.ClaimPerChatHour != 9 {
		t.Errorf("limits = %d/%d/%d", cfg.Limits.GroupCreateDay, cfg.Limits.GroupCreateWeek, cfg.Limits.ClaimPerChatHour)
	}
	if cfg.Limits.ClaimCodeTTL != time.Hour {
		t.Errorf("Limits.ClaimCodeTTL = %v", cfg.Limits.ClaimCodeTTL)
	}
	if cfg.Limits.InviteDefaultTTLDays != 3 {
		t.Errorf("Limits.InviteDefaultTTLDays = %d", cfg.Limits.InviteDefaultTTLDays)
	}
	if cfg.Limits.SlugRegex != `^[A-Z]+$` {
		t.Errorf("Limits.SlugRegex = %q", cfg.Limits.SlugRegex)
	}
	if cfg.App.DefaultTZ != "UTC" {
		t.Errorf("App.DefaultTZ = %q", cfg.App.DefaultTZ)
	}
	if cfg.App.LogLevel != "debug" || cfg.App.LogFormat != "text" {
		t.Errorf("log = %q/%q", cfg.App.LogLevel, cfg.App.LogFormat)
	}
}

func TestLoadMissingRequired(t *testing.T) {
	t.Setenv("BOT_TOKEN", "")
	t.Setenv("DATABASE_URL", "")

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load() with missing required vars returned nil error")
	}
	if !strings.Contains(err.Error(), "BOT_TOKEN") {
		t.Errorf("error %q does not mention BOT_TOKEN", err)
	}
	if !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Errorf("error %q does not mention DATABASE_URL", err)
	}
}

func TestLoadMissingBotTokenOnly(t *testing.T) {
	t.Setenv("BOT_TOKEN", "")
	t.Setenv("DATABASE_URL", "postgres://localhost/deadliner")

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load() with missing BOT_TOKEN returned nil error")
	}
	if !strings.Contains(err.Error(), "BOT_TOKEN") {
		t.Errorf("error %q does not mention BOT_TOKEN", err)
	}
	if strings.Contains(err.Error(), "DATABASE_URL") {
		t.Errorf("error %q mentions DATABASE_URL which is set", err)
	}
}

func TestLoadInvalidDuration(t *testing.T) {
	setRequired(t)
	t.Setenv("SCHED_POLL_INTERVAL", "ten seconds")

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load() with invalid duration returned nil error")
	}
	if !strings.Contains(err.Error(), "SCHED_POLL_INTERVAL") {
		t.Errorf("error %q does not mention SCHED_POLL_INTERVAL", err)
	}
}

func TestLoadInvalidInt(t *testing.T) {
	setRequired(t)
	t.Setenv("DB_POOL_MAX", "many")

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load() with invalid int returned nil error")
	}
	if !strings.Contains(err.Error(), "DB_POOL_MAX") {
		t.Errorf("error %q does not mention DB_POOL_MAX", err)
	}
}

// Регрессия C-1 (конфиг): retention ≤ недельного окна — ошибка загрузки, а не
// молчаливая потеря живого недельного счётчика rate-limit.
func TestLoadCounterRetentionMustExceedWeekWindow(t *testing.T) {
	setRequired(t)
	t.Setenv("COUNTER_RETENTION", "24h")

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load() with COUNTER_RETENTION=24h returned nil error, want failure")
	}
	if !strings.Contains(err.Error(), "COUNTER_RETENTION") {
		t.Errorf("error %q does not mention COUNTER_RETENTION", err)
	}

	// Ровно 168ч — тоже отказ: окно floor-ится, возраст живой строки может
	// быть равен длине окна.
	for _, v := range []string{"168h", "1h", "0s"} {
		t.Setenv("COUNTER_RETENTION", v)
		if _, err := config.Load(); err == nil {
			t.Errorf("Load() with COUNTER_RETENTION=%s returned nil error, want failure", v)
		}
	}

	t.Setenv("COUNTER_RETENTION", "169h")
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() with COUNTER_RETENTION=169h: %v", err)
	}
	if cfg.Limits.CounterRetention != 169*time.Hour {
		t.Errorf("CounterRetention = %v, want 169h", cfg.Limits.CounterRetention)
	}
}
