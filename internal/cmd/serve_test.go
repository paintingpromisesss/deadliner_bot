package cmd

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sauron/deadliner/internal/config"
	"github.com/sauron/deadliner/internal/platform/telegram"
)

// stubBotAPI — заглушка Telegram Bot API для тестов serve: отвечает ok-ом на
// все методы и запоминает, какие вызывались. Конфиг указывает на неё через
// BOT_API_BASE, поэтому сборка графа не ходит в сеть.
type stubBotAPI struct {
	mu      sync.Mutex
	methods []string
}

func (s *stubBotAPI) start(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		s.mu.Lock()
		s.methods = append(s.methods, method)
		s.mu.Unlock()

		result := "true"
		switch method {
		case "getMe":
			result = `{"id":42,"is_bot":true,"first_name":"Deadliner","username":"deadliner_bot"}`
		case "getUpdates":
			result = "[]"
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"ok":true,"result":%s}`, result)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func (s *stubBotAPI) called(method string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range s.methods {
		if m == method {
			return true
		}
	}
	return false
}

// cfgForServe — минимальный конфиг serve: APIBase заглушки, локальный порт.
func cfgForServe(apiBase string, webhook bool) *config.Config {
	cfg := &config.Config{
		App: config.App{
			HTTPAddr: "127.0.0.1:0", PublicURL: "https://deadliner.example/app",
			DefaultTZ: "Europe/Moscow", LogLevel: "error", LogFormat: "text",
			AuthDateMaxAge: 24 * time.Hour, SessionTTLDays: 30,
		},
		DB:  config.DB{URL: "postgres://localhost/deadliner", PoolMax: 4},
		Bot: config.Bot{Token: "42:TEST", APIBase: apiBase, RateGlobal: 25, RatePerChat: 18},
		Scheduler: config.Scheduler{
			PollInterval: time.Minute, Batch: 10, LockTTL: time.Minute, MaxAttempts: 5,
		},
		Cleanup: config.Cleanup{Interval: time.Hour},
		Limits: config.Limits{
			GroupPendingTTL: 14 * 24 * time.Hour, GroupCreateDay: 3, GroupCreateWeek: 5,
			ClaimPerChatHour: 3, ClaimCodeTTL: 10 * time.Minute, InviteDefaultTTLDays: 7,
			SlugRegex: `^[А-ЯA-Z0-9]+(-[А-ЯA-Z0-9]+)*$`, CounterRetention: 192 * time.Hour,
		},
	}
	if webhook {
		cfg.Bot.PollingMode = config.PollingModeWebhook
		cfg.Bot.WebhookURL = "https://deadliner.example/webhook"
		cfg.Bot.WebhookSecret = "s3cret"
	} else {
		cfg.Bot.PollingMode = "long_polling"
	}
	return cfg
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// Режим приёма апдейтов определяется одинаково для баннера и для клиента:
// расхождение означало бы «лог говорит polling, а процесс слушает webhook».
func TestBotModeMapping(t *testing.T) {
	polling := cfgForServe("", false)
	if got := botMode(polling); got != "polling" {
		t.Errorf("botMode(polling) = %q, want polling", got)
	}
	if got := telegramMode(polling); got != telegram.ModePolling {
		t.Errorf("telegramMode(polling) = %q, want %q", got, telegram.ModePolling)
	}

	webhook := cfgForServe("", true)
	if got := botMode(webhook); got != "webhook" {
		t.Errorf("botMode(webhook) = %q, want webhook", got)
	}
	if got := telegramMode(webhook); got != telegram.ModeWebhook {
		t.Errorf("telegramMode(webhook) = %q, want %q", got, telegram.ModeWebhook)
	}
}

// webhookHandler монтируется только в webhook-режиме: в polling-режиме POST
// /webhook обязан отвечать 404, а не принимать апдейты, которые никто не
// обрабатывает.
func TestWebhookHandlerMounting(t *testing.T) {
	if h := webhookHandler(cfgForServe("", false), nil); h != nil {
		t.Error("webhookHandler(polling) = non-nil, want nil")
	}
	if h := webhookHandler(cfgForServe("", true), nil); h != nil {
		t.Error("webhookHandler(webhook, nil bot) = non-nil, want nil")
	}
}

// workerID должен быть уникальным между вызовами и содержать хост: воркеры
// координируются через locked_by, и одинаковый id означал бы «второй воркер
// считает чужой лок своим».
func TestWorkerID(t *testing.T) {
	a, b := workerID(), workerID()
	if a == b {
		t.Errorf("workerID() returned the same value twice: %q", a)
	}
	if !strings.Contains(a, "-") {
		t.Errorf("workerID() = %q, want host-pid-rand", a)
	}
}

func TestBuildVersion(t *testing.T) {
	if got := buildVersion(); got == "" {
		t.Error("buildVersion() = empty, want a non-empty string")
	}
}

// Сборка графа в webhook-режиме без секрета обязана падать (fail-closed):
// клиент создаётся до сервисов, и без секрета библиотека принимала бы любой
// POST /webhook, то есть подделанные апдейты.
func TestBuildGraphWebhookRequiresSecret(t *testing.T) {
	cfg := cfgForServe("http://127.0.0.1:1", true)
	cfg.Bot.WebhookSecret = ""

	_, err := buildGraph(cfg, nil, testLogger())
	if err == nil {
		t.Fatal("buildGraph(webhook without secret) = nil, want error")
	}
	if !strings.Contains(err.Error(), "WebhookSecret") {
		t.Errorf("error = %v, want it to name WebhookSecret", err)
	}
}

// buildGraph не ходит в сеть: клиент создаётся с WithSkipGetMe, id бота
// берётся из токена. Проверяем на заведомо недоступном APIBase — любой
// запрос на сборке провалил бы тест.
func TestBuildGraphBuildsOffline(t *testing.T) {
	cfg := cfgForServe("http://127.0.0.1:1", false)

	g, err := buildGraph(cfg, nil, testLogger())
	if err != nil {
		t.Fatalf("buildGraph: %v", err)
	}
	if g.bot == nil || g.worker == nil || g.router == nil || g.moder == nil {
		t.Fatalf("graph incomplete: %+v", g)
	}
}

// Заглушка Bot API подтверждает, что полный цикл сборки не делает ни одного
// запроса: getMe не вызывается (иначе каждый старт зависел бы от сети).
func TestBuildGraphDoesNotCallGetMe(t *testing.T) {
	stub := &stubBotAPI{}
	cfg := cfgForServe(stub.start(t), false)

	if _, err := buildGraph(cfg, nil, testLogger()); err != nil {
		t.Fatalf("buildGraph: %v", err)
	}
	if stub.called("getMe") {
		t.Error("buildGraph called getMe: the bot must be built offline")
	}
}
