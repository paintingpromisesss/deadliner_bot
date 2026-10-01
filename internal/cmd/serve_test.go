package cmd

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sauron/deadliner/internal/config"
	"github.com/sauron/deadliner/internal/platform/scheduler"
	"github.com/sauron/deadliner/internal/platform/telegram"
)

// stubBotAPI — заглушка Telegram Bot API для тестов serve: отвечает ok-ом на
// все методы и запоминает, какие вызывались. Конфиг указывает на неё через
// BOT_API_BASE, поэтому сборка графа не ходит в сеть.
type stubBotAPI struct {
	mu      sync.Mutex
	methods []string
	// failSetWebhook — заглушка отвечает 401 на setWebhook (неверный токен):
	// так проверяется путь отказа регистрации вебхука.
	failSetWebhook bool
}

func (s *stubBotAPI) start(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		s.mu.Lock()
		s.methods = append(s.methods, method)
		s.mu.Unlock()

		if method == "setWebhook" && s.failSetWebhook {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"ok":false,"error_code":401,"description":"Unauthorized"}`)
			return
		}

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
		// Serve начинается с миграций и подключения: URL контейнера из TestMain
		// (без него тесты падали бы на «DATABASE_URL is not set»).
		DB:  config.DB{URL: testDatabaseURL, PoolMax: 4},
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

// Воркер из графа serve обязан работать под дефолтным FinalizeTimeout: serve не
// задаёт значение сам (иначе копия константы могла бы разойтись с пакетом
// scheduler и тихо вернуть баг с недоотправленным напоминанием). Проверяем
// поведением, а не полем: воркер, у которого бюджет меньше выдержки 429 в
// нотификаторе, не смог бы зафиксировать отправку.
func TestBuildGraphWorkerUsesDefaultFinalizeTimeout(t *testing.T) {
	cfg := cfgForServe("http://127.0.0.1:1", false)

	g, err := buildGraph(cfg, nil, testLogger())
	if err != nil {
		t.Fatalf("buildGraph: %v", err)
	}
	got := g.worker.FinalizeTimeout()
	if got != scheduler.DefaultFinalizeTimeout {
		t.Errorf("serve's worker FinalizeTimeout = %v, want %v (= scheduler default)",
			got, scheduler.DefaultFinalizeTimeout)
	}
	if got <= 60*time.Second {
		t.Errorf("serve's worker FinalizeTimeout = %v, must exceed the notifier's 60s retry_after wait", got)
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

// Путь ошибки net.Listen обязан снять вебхук: к моменту падения он уже
// зарегистрирован (startBot идёт раньше прослушивания), и оставленный адрес
// указывал бы на мёртвый инстанс. Тест занимает порт заранее, гоняет Serve и
// проверяет, что после setWebhook последовал deleteWebhook.
//
// Полноценный Serve запускается с заглушкой Bot API; до реального дренажа дело
// не доходит — Serve падает на прослушивании сразу после регистрации вебхука.
func TestServeListenFailureUnregistersWebhook(t *testing.T) {
	stub := &stubBotAPI{}
	cfg := cfgForServe(stub.start(t), true)

	// Занимаем порт: тот же адрес, что возьмёт Serve.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	defer ln.Close()
	cfg.App.HTTPAddr = ln.Addr().String()

	err = Serve(context.Background(), cfg, testLogger())
	if err == nil {
		t.Fatal("Serve with an occupied port = nil, want a listen error")
	}
	if !strings.Contains(err.Error(), "listen") {
		t.Errorf("Serve error = %v, want it to name the listen failure", err)
	}

	if !stub.called("setWebhook") {
		t.Error("setWebhook was not called: the webhook must be registered before listening")
	}
	if !stub.called("deleteWebhook") {
		t.Error("deleteWebhook was not called on the listen-failure path: " +
			"the webhook would keep pointing at a dead instance")
	}
}

// Отказ setWebhook не должен сопровождаться снятием вебхука: он не
// регистрировался, а deleteWebhook в этом пути только маскировал бы причину.
func TestServeSetWebhookFailureSkipsUnregister(t *testing.T) {
	stub := &stubBotAPI{failSetWebhook: true}
	cfg := cfgForServe(stub.start(t), true)

	err := Serve(context.Background(), cfg, testLogger())
	if err == nil {
		t.Fatal("Serve with a failing setWebhook = nil, want an error")
	}
	if !strings.Contains(err.Error(), "setWebhook") {
		t.Errorf("Serve error = %v, want it to name setWebhook", err)
	}
	if stub.called("deleteWebhook") {
		t.Error("deleteWebhook was called although the webhook was never registered")
	}
}
