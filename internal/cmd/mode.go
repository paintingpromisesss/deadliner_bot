package cmd

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"runtime/debug"
	"strconv"
	"time"

	"github.com/sauron/deadliner/internal/config"
	"github.com/sauron/deadliner/internal/platform/telegram"
)

// botMode — человекочитаемый режим приёма апдейтов для баннера и логов.
func botMode(cfg *config.Config) string {
	if cfg.Bot.UsesWebhook() {
		return "webhook"
	}
	return "polling"
}

// telegramMode — режим для telegram.BotConfig: webhook определяется как
// POLLING_MODE=webhook, так и непустым WEBHOOK_URL (определение — одно на весь
// процесс, см. config.Bot.UsesWebhook).
func telegramMode(cfg *config.Config) telegram.Mode {
	if cfg.Bot.UsesWebhook() {
		return telegram.ModeWebhook
	}
	return telegram.ModePolling
}

// webhookHandler — обработчик POST /webhook в webhook-режиме; nil в
// polling-режиме (маршрут не монтируется, и POST /webhook честно даёт 404 от
// статики, а не принимает апдейты в никуда). Секрет проверяет библиотека.
func webhookHandler(cfg *config.Config, bot *telegram.Bot) http.Handler {
	if !cfg.Bot.UsesWebhook() || bot == nil {
		return nil
	}
	return bot.WebhookHandler()
}

// workerID — идентификатор воркера для локирования строк reminders
// (reminders.locked_by). Формат «host/pod-pid-rand» облегчает разбор логов:
// видно, какой инстанс держал лок. Планировщик координируется через
// FOR UPDATE SKIP LOCKED, поэтому уникальности достаточно, а глобальной
// синхронизации не требуется.
func workerID() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown"
	}
	var buf [4]byte
	b := make([]byte, 0, len(buf)*2)
	if _, err := rand.Read(buf[:]); err == nil {
		b = []byte(hex.EncodeToString(buf[:]))
	} else {
		b = []byte(strconv.FormatInt(time.Now().UnixNano(), 16))
	}
	return fmt.Sprintf("%s-%d-%s", host, os.Getpid(), b)
}

// buildVersion — версия сборки для баннера: коммит и флаг модификации из
// ReadBuildInfo. Сборка без VCS (вне модуля) даёт "unknown" — это нормально,
// баннер не должен требовать ldflags.
func buildVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	var rev string
	var dirty bool
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if rev == "" {
		return "dev"
	}
	if len(rev) > 12 {
		rev = rev[:12]
	}
	if dirty {
		return rev + "-dirty"
	}
	return rev
}
