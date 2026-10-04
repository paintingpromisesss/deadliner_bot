package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sauron/deadliner/internal/config"
)

// exampleEnvKeys — все переменные, которые читает config.Load. Нужны, чтобы
// тест на .env.example был детерминированным: окружение прогона не должно
// подмешивать значения, которых в примере нет.
var exampleEnvKeys = []string{
	"BOT_TOKEN", "BOT_API_BASE", "BOT_USERNAME", "POLLING_MODE", "WEBHOOK_URL", "WEBHOOK_SECRET",
	"WEBHOOK_PORT", "TG_RATE_GLOBAL", "TG_RATE_PER_CHAT", "HTTP_ADDR", "DATABASE_URL",
	"DB_POOL_MAX", "APP_PUBLIC_URL", "SESSION_TTL_DAYS", "AUTH_DATE_MAX_AGE_HOURS",
	"DEFAULT_TZ", "LOG_LEVEL", "LOG_FORMAT", "SCHED_POLL_INTERVAL", "SCHED_BATCH",
	"SCHED_LOCK_TTL", "SCHED_MAX_ATTEMPTS", "CLEANUP_INTERVAL",
	"GROUP_PENDING_TTL_DAYS", "LIMIT_GROUP_CREATE_DAY", "LIMIT_GROUP_CREATE_WEEK",
	"INVITE_DEFAULT_TTL_DAYS",
	"SLUG_REGEX", "COUNTER_RETENTION",
}

// parseEnvExample разбирает .env.example так, как это делают парсеры env-файлов
// (docker compose env_file, docker run --env-file): строка «KEY=VALUE»,
// комментарием считается ТОЛЬКО строка, начинающаяся с «#». Всё, что стоит
// после «=», — значение целиком, включая возможный «# …» хвост.
func parseEnvExample(t *testing.T, path string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	out := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			t.Fatalf("%s: line is neither a comment nor KEY=VALUE: %q", path, line)
		}
		out[key] = value
	}
	return out
}

// Регрессия (C1): комментарии в .env.example обязаны быть ТОЛЬКО отдельными
// строками. Инлайновый комментарий после пустого значения — это НЕ комментарий
// для env-парсеров: строка «WEBHOOK_URL=    # https://…» даёт значение
// «# https://…», процесс уходит в webhook-режим с мусорным адресом и падает на
// setWebhook. Тест разбирает файл ровно как env-парсер и проверяет, что ни одно
// значение не начинается с «#» и что пример не включает webhook-режим.
func TestEnvExampleHasNoInlineComments(t *testing.T) {
	path := filepath.Join("..", "..", ".env.example")
	values := parseEnvExample(t, path)

	// 1. Ни одно значение не должно быть комментарием-хвостом.
	for key, value := range values {
		if strings.HasPrefix(strings.TrimSpace(value), "#") {
			t.Errorf("%s: value %q starts with '#' — env-парсеры не считают это комментарием "+
				"(инлайновые комментарии запрещены, комментарий должен быть отдельной строкой)", key, value)
		}
	}

	// 2. Все известные переменные присутствуют (пример должен быть полным).
	for _, key := range exampleEnvKeys {
		if _, ok := values[key]; !ok {
			t.Errorf(".env.example is missing %s", key)
		}
	}

	// 3. Скопированный пример не включает webhook-режим и не несёт мусора.
	//    Обязательные переменные в примере пустые (их заполняет оператор),
	//    поэтому для Load подставляем минимальные значения — важно, что
	//    ОСТАЛЬНЫЕ значения берутся из файла как есть.
	for _, key := range exampleEnvKeys {
		t.Setenv(key, values[key])
	}
	t.Setenv("BOT_TOKEN", "123456:FAKE")
	t.Setenv("DATABASE_URL", "postgres://deadliner:deadliner@localhost:5432/deadliner?sslmode=disable")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() with .env.example values = %v, want success", err)
	}
	if cfg.Bot.UsesWebhook() {
		t.Errorf("UsesWebhook() = true for .env.example: WEBHOOK_URL=%q POLLING_MODE=%q",
			cfg.Bot.WebhookURL, cfg.Bot.PollingMode)
	}
	if cfg.Bot.WebhookURL != "" {
		t.Errorf("WEBHOOK_URL from .env.example = %q, want empty", cfg.Bot.WebhookURL)
	}
	if cfg.Bot.WebhookSecret != "" {
		t.Errorf("WEBHOOK_SECRET from .env.example = %q, want empty", cfg.Bot.WebhookSecret)
	}
	if cfg.App.PublicURL != "" || cfg.Bot.APIBase != "" {
		t.Errorf("APP_PUBLIC_URL=%q BOT_API_BASE=%q, want both empty",
			cfg.App.PublicURL, cfg.Bot.APIBase)
	}
	if cfg.App.HTTPAddr != ":8080" || cfg.Bot.PollingMode != "long_polling" {
		t.Errorf("HTTPAddr=%q PollingMode=%q, want :8080 / long_polling",
			cfg.App.HTTPAddr, cfg.Bot.PollingMode)
	}
}

// Разбор примера не должен терять переменные: каждая строка файла — либо
// комментарий целиком, либо «KEY=VALUE». Отдельно от предыдущего теста, потому
// что это свойство формата, а не конкретных значений.
func TestEnvExampleLinesAreWellFormed(t *testing.T) {
	path := filepath.Join("..", "..", ".env.example")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	for i, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if !strings.Contains(line, "=") {
			t.Errorf("%s:%d: neither a comment nor KEY=VALUE: %q", path, i+1, line)
		}
	}
}
