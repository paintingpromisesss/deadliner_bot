package config

import (
	"strings"
	"testing"
)

func setRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("BOT_TOKEN", "42:TEST")
	t.Setenv("DATABASE_URL", "postgres://localhost/deadliner")
}

func TestLoadWebhookWithoutSecretFailsClosed(t *testing.T) {
	// Fail-closed (спека §6.1): без секрета библиотека принимает любой
	// POST /webhook, то есть подделанные апдейты (включая /bind_group).
	cases := []struct {
		name string
		env  map[string]string
	}{
		{"POLLING_MODE=webhook", map[string]string{"POLLING_MODE": "webhook"}},
		{"WEBHOOK_URL set", map[string]string{"WEBHOOK_URL": "https://example/webhook"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			setRequiredEnv(t)
			for k, v := range c.env {
				t.Setenv(k, v)
			}
			_, err := Load()
			if err == nil {
				t.Fatal("Load() = nil, want an error mentioning WEBHOOK_SECRET")
			}
			if !strings.Contains(err.Error(), "WEBHOOK_SECRET") {
				t.Errorf("error = %v, want it to name WEBHOOK_SECRET", err)
			}
		})
	}
}

func TestLoadWebhookWithSecretOK(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("POLLING_MODE", "webhook")
	t.Setenv("WEBHOOK_SECRET", "s3cret")
	// URL обязателен в webhook-режиме (I2): без него процесс стартовал бы и
	// молча не принимал апдейты.
	t.Setenv("WEBHOOK_URL", "https://example/webhook")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v, want success", err)
	}
	if cfg.Bot.WebhookSecret != "s3cret" {
		t.Errorf("webhook secret = %q", cfg.Bot.WebhookSecret)
	}
	if cfg.Bot.WebhookURL != "https://example/webhook" {
		t.Errorf("webhook url = %q", cfg.Bot.WebhookURL)
	}
}

// Polling-режим секрета не требует: WEBHOOK_SECRET без webhook-режима — норма.
func TestLoadPollingWithoutSecretOK(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("POLLING_MODE", "long_polling")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v, want success", err)
	}
	if cfg.Bot.UsesWebhook() {
		t.Errorf("usesWebhook() = true for POLLING_MODE=%q", cfg.Bot.PollingMode)
	}
}

// После I2 (webhook без URL — ошибка загрузки) клиент в webhook-режиме обязан
// получить непустой URL: проверка здесь защищает от рассинхронизации, если
// валидация конфига когда-нибудь изменится, — молча не принимающий апдейты
// бот хуже отказа старта.
func TestLoadWebhookWithoutURLFailsClosed(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("POLLING_MODE", "webhook")
	t.Setenv("WEBHOOK_SECRET", "s3cret")

	_, err := Load()
	if err == nil {
		t.Fatal("Load(webhook without WEBHOOK_URL) = nil, want an error naming WEBHOOK_URL")
	}
	if !strings.Contains(err.Error(), "WEBHOOK_URL") {
		t.Errorf("error = %v, want it to name WEBHOOK_URL", err)
	}

	// С URL — загрузка проходит.
	t.Setenv("WEBHOOK_URL", "https://example/webhook")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load(webhook with URL) = %v, want success", err)
	}
	if !cfg.Bot.UsesWebhook() || cfg.Bot.WebhookURL == "" {
		t.Errorf("UsesWebhook()=%v WebhookURL=%q, want true / non-empty",
			cfg.Bot.UsesWebhook(), cfg.Bot.WebhookURL)
	}

	// Обратный случай: URL задан, а POLLING_MODE не трогаем — режим определяется
	// URL, и пустой PollingMode не должен падать на этой проверке.
	t.Setenv("POLLING_MODE", "")
	if _, err := Load(); err != nil {
		t.Errorf("Load(WEBHOOK_URL set, POLLING_MODE empty) = %v, want success", err)
	}
}

func TestUsesWebhook(t *testing.T) {
	cases := []struct {
		mode, url string
		want      bool
	}{
		{"long_polling", "", false},
		{"webhook", "", true},
		{"long_polling", "https://example/webhook", true},
		{"", "https://example/webhook", true},
		{"", "", false},
	}
	for _, c := range cases {
		got := Bot{PollingMode: c.mode, WebhookURL: c.url}.UsesWebhook()
		if got != c.want {
			t.Errorf("UsesWebhook(mode=%q,url=%q) = %v, want %v", c.mode, c.url, got, c.want)
		}
	}
}

// Опечатка в POLLING_MODE (например "polling" вместо "long_polling") при
// непустом WEBHOOK_URL не ошибка: webhook определяется по URL. Но без URL
// такой режим неотличим от опечатки — процесс молча не принимал бы апдейты,
// поэтому Load обязан отказать.
func TestLoadUnknownPollingModeFails(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("POLLING_MODE", "polling")

	_, err := Load()
	if err == nil {
		t.Fatal("Load(POLLING_MODE=polling) = nil, want an error naming POLLING_MODE")
	}
	if !strings.Contains(err.Error(), "POLLING_MODE") {
		t.Errorf("error = %v, want it to name POLLING_MODE", err)
	}

	// С WEBHOOK_URL тот же режим валиден: приём апдейтов однозначен.
	t.Setenv("WEBHOOK_URL", "https://example/webhook")
	t.Setenv("WEBHOOK_SECRET", "s3cret")
	if _, err := Load(); err != nil {
		t.Errorf("Load(POLLING_MODE=polling + WEBHOOK_URL) = %v, want success", err)
	}
}
