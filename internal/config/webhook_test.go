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

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v, want success", err)
	}
	if cfg.Bot.WebhookSecret != "s3cret" {
		t.Errorf("webhook secret = %q", cfg.Bot.WebhookSecret)
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
	if cfg.Bot.usesWebhook() {
		t.Errorf("usesWebhook() = true for POLLING_MODE=%q", cfg.Bot.PollingMode)
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
		got := Bot{PollingMode: c.mode, WebhookURL: c.url}.usesWebhook()
		if got != c.want {
			t.Errorf("usesWebhook(mode=%q,url=%q) = %v, want %v", c.mode, c.url, got, c.want)
		}
	}
}
