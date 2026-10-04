package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sauron/deadliner/internal/app/groups"
	"github.com/sauron/deadliner/internal/domain"
	"github.com/sauron/deadliner/internal/i18n"
)

// stubTelegram — заглушка Telegram Bot API для смоук-теста Bot: отвечает
// ok-ом на любой метод, запоминает вызванные методы и разобранные поля запросов
// (библиотека шлёт все вызовы multipart-ом, а не JSON-ом).
type stubTelegram struct {
	mu      sync.Mutex
	methods []string
	fields  map[string][]map[string]string
}

func (s *stubTelegram) start(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]

		parsed := map[string]string{}
		if err := r.ParseMultipartForm(4 << 20); err == nil && r.MultipartForm != nil {
			for k, v := range r.MultipartForm.Value {
				if len(v) > 0 {
					parsed[k] = v[0]
				}
			}
		}

		s.mu.Lock()
		s.methods = append(s.methods, method)
		if s.fields == nil {
			s.fields = map[string][]map[string]string{}
		}
		s.fields[method] = append(s.fields[method], parsed)
		s.mu.Unlock()

		// Telegram отвергает menu button без типа ("MenuButton has unsupported
		// type"). Заглушка воспроизводит проверку, иначе кривой тип проходит
		// молча.
		if method == "setChatMenuButton" {
			var p struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal([]byte(parsed["menu_button"]), &p); err != nil || p.Type != "web_app" {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprint(w, `{"ok":false,"error_code":400,"description":"Bad Request: can't parse menu button: MenuButton has unsupported type"}`)
				return
			}
		}

		result := "true"
		switch method {
		case "getMe":
			result = `{"id":42,"is_bot":true,"first_name":"Deadliner","username":"deadliner_bot"}`
		case "sendMessage":
			result = `{"message_id":11,"chat":{"id":1,"type":"private"},"date":1}`
		case "getChatMember":
			result = `{"status":"administrator","user":{"id":42,"is_bot":true,"first_name":"D"},"can_be_edited":false,"is_anonymous":false,"can_manage_chat":true,"can_delete_messages":true,"can_manage_video_chats":true,"can_restrict_members":true,"can_promote_members":false,"can_change_info":true,"can_invite_users":true}`
		case "setMyCommands", "setChatMenuButton":
			result = "true"
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"ok":true,"result":%s}`, result)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// fieldOf возвращает поле N-го (0-based) вызова метода.
func (s *stubTelegram) fieldOf(method, field string, n int) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	calls := s.fields[method]
	if n >= len(calls) {
		return "", false
	}
	v, ok := calls[n][field]
	return v, ok
}

func (s *stubTelegram) methodCount(method string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, m := range s.methods {
		if m == method {
			n++
		}
	}
	return n
}

func (s *stubTelegram) called(method string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range s.methods {
		if m == method {
			return true
		}
	}
	return false
}

// stubUsers потокобезопасен: апдейты webhook-режима обрабатываются в воркерах.
type stubUsers struct {
	mu      sync.Mutex
	upserts int
}

func (u *stubUsers) count() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.upserts
}

func (u *stubUsers) GetByTelegramID(ctx context.Context, telegramID int64) (*domain.User, error) {
	return nil, domain.ErrNotFound
}
func (u *stubUsers) GetByID(ctx context.Context, id int64) (*domain.User, error) {
	return nil, domain.ErrNotFound
}
func (u *stubUsers) UpsertByTelegram(ctx context.Context, usr *domain.User) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.upserts++
	return nil
}
func (u *stubUsers) UpdateSettings(ctx context.Context, id int64, tz string, dmNotifyDefault bool) error {
	return nil
}
func (u *stubUsers) SetBanned(ctx context.Context, id int64, banned bool) error         { return nil }
func (u *stubUsers) SetSuperadmin(ctx context.Context, id int64, superadmin bool) error { return nil }
func (u *stubUsers) MarkBotBlocked(ctx context.Context, telegramID int64, blocked bool) error {
	return nil
}

// ListSuperadmins и UpdateProfile — части domain.UserRepo, не используемые
// ботом в этих тестах: заглушки-нули.
func (u *stubUsers) ListSuperadmins(ctx context.Context) ([]domain.User, error) {
	return nil, nil
}
func (u *stubUsers) UpdateProfile(ctx context.Context, id int64, firstName string) error {
	return nil
}

type stubBinder struct{ bound []int64 }

func (b *stubBinder) BindChat(ctx context.Context, actor *domain.User, chatID int64, threadID *int64, slug, chatTitle string) (*domain.Group, error) {
	b.bound = append(b.bound, chatID)
	return &domain.Group{ID: 1, Slug: domain.Normalize(slug), Title: chatTitle}, nil
}
func (b *stubBinder) UnbindChat(ctx context.Context, actor *domain.User, chatID int64, threadID *int64) (*domain.Group, error) {
	return &domain.Group{ID: 1, Slug: "ИКБО-33-21"}, nil
}
func (b *stubBinder) ListMine(ctx context.Context, actor *domain.User) ([]groups.MyGroup, error) {
	return nil, nil
}

// NewBot конструируется без сети (skipGetMe) и собирает живой BotSender:
// setMyCommands/setChatMenuButton уходят в заглушку API, а Handle доставляет
// апдейт хендлерам.
func TestNewBotSmoke(t *testing.T) {
	i18n.MustLoad(i18n.Locales)
	stub := &stubTelegram{}
	users := &stubUsers{}
	binder := &stubBinder{}

	b, err := NewBot(BotConfig{
		Token:        "42:TEST",
		APIBase:      stub.start(t),
		Mode:         ModePolling,
		AppPublicURL: "https://deadliner.example/app",
	}, Deps{Users: users, Binder: binder, BotUserID: 42},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewBot: %v", err)
	}
	if b.Sender() == nil || b.API() == nil {
		t.Fatal("Bot built without transport")
	}

	// setupUI — то, что Start вызывает на старте (проверяем напрямую, чтобы
	// не поднимать polling-цикл).
	b.setupUI(context.Background())
	if !stub.called("setMyCommands") {
		t.Errorf("setMyCommands not called; methods = %v", stub.methods)
	}
	if !stub.called("setChatMenuButton") {
		t.Errorf("setChatMenuButton not called; methods = %v", stub.methods)
	}
	// Тело menu button проверяем по существу: Telegram отвергает тип, отличный
	// от web_app (в проде это давало "MenuButton has unsupported type"),
	// и кнопка обязана вести на APP_PUBLIC_URL.
	var mb struct {
		Type   string `json:"type"`
		Text   string `json:"text"`
		WebApp struct {
			URL string `json:"url"`
		} `json:"web_app"`
	}
	raw, ok := stub.fieldOf("setChatMenuButton", "menu_button", 0)
	if !ok {
		t.Fatal("setChatMenuButton body has no menu_button field")
	}
	if err := json.Unmarshal([]byte(raw), &mb); err != nil {
		t.Fatalf("menu button %s: %v", raw, err)
	}
	if mb.Type != "web_app" {
		t.Errorf("menu button type = %q, want %q (body: %s)", mb.Type, "web_app", raw)
	}
	if mb.WebApp.URL != "https://deadliner.example/app" {
		t.Errorf("menu button url = %q, want %q", mb.WebApp.URL, "https://deadliner.example/app")
	}
	// Спека §6.1: команды регистрируются и для администраторов чатов — значит
	// setMyCommands вызывается дважды (default + all_chat_administrators).
	if n := stub.methodCount("setMyCommands"); n != 2 {
		t.Errorf("setMyCommands calls = %d, want 2 (default + all_chat_administrators)", n)
	}

	// Handle доставляет апдейт: /start в ЛС обновляет пользователя и отвечает.
	upd := update(900, "private", 7, "ivan", "/start")
	b.Handle(context.Background(), upd)
	if users.count() != 1 {
		t.Errorf("user upserts = %d, want 1", users.count())
	}
	if !stub.called("sendMessage") {
		t.Errorf("sendMessage not called; methods = %v", stub.methods)
	}
}

// Пустой токен — ошибка конструирования (fail fast на старте, а не при первом
// апдейте).
func TestNewBotEmptyToken(t *testing.T) {
	if _, err := NewBot(BotConfig{}, Deps{}, nil); err == nil {
		t.Fatal("NewBot(empty token) = nil, want error")
	}
}

// F-3: webhook-режим без секрета не конструируется. Иначе библиотека приняла бы
// любой POST /webhook (сверка заголовка включается только при непустом секрете),
// то есть подделанные апдейты — включая /bind_group в чужом чате.
func TestNewBotWebhookRequiresSecret(t *testing.T) {
	_, err := NewBot(BotConfig{
		Token: "42:TEST", Mode: ModeWebhook, AppPublicURL: "https://example/app",
	}, Deps{}, nil)
	if err == nil {
		t.Fatal("NewBot(webhook without secret) = nil, want error")
	}
	if !strings.Contains(err.Error(), "WebhookSecret") {
		t.Errorf("error = %v, want it to name WebhookSecret", err)
	}

	// С секретом — конструируется, и секрет попадает в опции библиотеки.
	b, err := NewBot(BotConfig{
		Token: "42:TEST", Mode: ModeWebhook, WebhookSecret: "s3cret",
	}, Deps{}, nil)
	if err != nil {
		t.Fatalf("NewBot(webhook with secret) = %v, want success", err)
	}
	if b == nil {
		t.Fatal("NewBot returned nil bot")
	}
}

// В polling-режиме секрет не требуется.
func TestNewBotPollingWithoutSecret(t *testing.T) {
	if _, err := NewBot(BotConfig{Token: "42:TEST", Mode: ModePolling}, Deps{}, nil); err != nil {
		t.Fatalf("NewBot(polling without secret) = %v, want success", err)
	}
}

// BotUserID выводится из токена офлайн (tgbot.Bot.ID разбирает "<id>:<secret>"),
// поэтому getMe не нужен; явный Deps.BotUserID имеет приоритет.
func TestNewBotDerivesBotUserIDFromToken(t *testing.T) {
	b, err := NewBot(BotConfig{Token: "424242:TEST", Mode: ModePolling}, Deps{}, nil)
	if err != nil {
		t.Fatalf("NewBot: %v", err)
	}
	if got := b.handl.botUserID; got != 424242 {
		t.Errorf("bot user id = %d, want 424242 (parsed from the token)", got)
	}

	explicit, err := NewBot(BotConfig{Token: "424242:TEST"}, Deps{BotUserID: 7}, nil)
	if err != nil {
		t.Fatalf("NewBot: %v", err)
	}
	if got := explicit.handl.botUserID; got != 7 {
		t.Errorf("bot user id = %d, want the explicit 7", got)
	}
}

// WebhookHandler отдаёт апдейт в канал и проверяет секрет: без правильного
// заголовка апдейт отбрасывается.
func TestWebhookHandlerSecret(t *testing.T) {
	i18n.MustLoad(i18n.Locales)
	stub := &stubTelegram{}
	users := &stubUsers{}
	b, err := NewBot(BotConfig{
		Token: "42:TEST", APIBase: stub.start(t), Mode: ModeWebhook,
		WebhookSecret: "s3cret", AppPublicURL: "https://deadliner.example/app",
	}, Deps{Users: users, Binder: &stubBinder{}}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewBot: %v", err)
	}

	// Воркеры webhook-режима разбирают канал; StartWebhook блокирующий, поэтому
	// запускаем его на отменяемом ctx.
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		b.StartWebhook(ctx)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})

	body, _ := json.Marshal(map[string]any{
		"update_id": 1,
		"message": map[string]any{
			"message_id": 5,
			"from":       map[string]any{"id": 7, "username": "ivan", "first_name": "Иван"},
			"chat":       map[string]any{"id": 900, "type": "private"},
			"text":       "/help",
		},
	})

	// Без секрета апдейт не должен дойти до хендлеров.
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(string(body)))
	b.WebhookHandler().ServeHTTP(httptest.NewRecorder(), req)
	waitFor(t, func() bool { return users.count() == 0 }, "update without secret must be dropped")

	req = httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(string(body)))
	req.Header.Set("X-Telegram-Bot-Api-Secret-Token", "s3cret")
	b.WebhookHandler().ServeHTTP(httptest.NewRecorder(), req)
	waitFor(t, func() bool { return users.count() == 1 }, "update with the secret must be processed")
}

// RegisterWebhook/UnregisterWebhook: webhook-режим публикует адрес и секрет,
// polling-режим не трогает вебхук вовсе (иначе Telegram слал бы апдейты в
// оба приёмника).
func TestRegisterAndUnregisterWebhook(t *testing.T) {
	i18n.MustLoad(i18n.Locales)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	t.Run("webhook mode registers and unregisters", func(t *testing.T) {
		stub := &stubTelegram{}
		b, err := NewBot(BotConfig{
			Token: "42:TEST", APIBase: stub.start(t), Mode: ModeWebhook,
			WebhookSecret: "s3cret",
		}, Deps{Users: &stubUsers{}}, log)
		if err != nil {
			t.Fatalf("NewBot: %v", err)
		}
		ctx := context.Background()
		if err := b.RegisterWebhook(ctx, "https://deadliner.example/webhook"); err != nil {
			t.Fatalf("RegisterWebhook: %v", err)
		}
		if !stub.called("setWebhook") {
			t.Error("setWebhook was not called")
		}
		if err := b.UnregisterWebhook(ctx); err != nil {
			t.Fatalf("UnregisterWebhook: %v", err)
		}
		if !stub.called("deleteWebhook") {
			t.Error("deleteWebhook was not called")
		}
	})

	t.Run("polling mode is a no-op", func(t *testing.T) {
		stub := &stubTelegram{}
		b, err := NewBot(BotConfig{
			Token: "42:TEST", APIBase: stub.start(t), Mode: ModePolling,
		}, Deps{Users: &stubUsers{}}, log)
		if err != nil {
			t.Fatalf("NewBot: %v", err)
		}
		ctx := context.Background()
		if err := b.RegisterWebhook(ctx, "https://deadliner.example/webhook"); err != nil {
			t.Fatalf("RegisterWebhook(polling): %v", err)
		}
		if err := b.UnregisterWebhook(ctx); err != nil {
			t.Fatalf("UnregisterWebhook(polling): %v", err)
		}
		if stub.called("setWebhook") || stub.called("deleteWebhook") {
			t.Errorf("polling mode must not touch the webhook; methods: %v", stub.methods)
		}
	})

	t.Run("empty url is refused", func(t *testing.T) {
		stub := &stubTelegram{}
		b, err := NewBot(BotConfig{
			Token: "42:TEST", APIBase: stub.start(t), Mode: ModeWebhook,
			WebhookSecret: "s3cret",
		}, Deps{Users: &stubUsers{}}, log)
		if err != nil {
			t.Fatalf("NewBot: %v", err)
		}
		if err := b.RegisterWebhook(context.Background(), ""); err == nil {
			t.Fatal("RegisterWebhook(\"\") = nil, want error naming WEBHOOK_URL")
		}
	})
}

// Двухшаговая сборка (serve): клиент и транспорт доступны ДО сервисов, а
// диспетчер — после них через SetDeps. Проверяем, что оба пути дают рабочие
// хендлеры с выведенным из токена id бота.
func TestNewBotAcceptsExistingClient(t *testing.T) {
	i18n.MustLoad(i18n.Locales)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	client, err := NewClient(BotConfig{Token: "555:TEST"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if client.Sender() == nil || client.API() == nil {
		t.Fatal("NewClient returned an incomplete client")
	}

	b, err := NewBot(BotConfig{Token: "555:TEST", Mode: ModePolling},
		Deps{Users: &stubUsers{}, API: client.API(), Sender: client.Sender()}, log)
	if err != nil {
		t.Fatalf("NewBot with existing client: %v", err)
	}
	if b.API() != client.API() {
		t.Error("NewBot created a different API client instead of reusing the provided one")
	}
	if got := b.handl.botUserID; got != 555 {
		t.Errorf("bot user id = %d, want 555 (parsed from the token)", got)
	}

	// SetDeps пересобирает диспетчер (порядок сборки serve).
	b.SetDeps(Deps{Users: &stubUsers{}, BotUserID: 9})
	if got := b.handler().botUserID; got != 9 {
		t.Errorf("bot user id after SetDeps = %d, want 9", got)
	}
}

// handleUpdate не паникует, если диспетчер ещё не собран (Bot до SetDeps).
func TestHandleWithoutHandlers(t *testing.T) {
	b := &Bot{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	b.Handle(context.Background(), nil) // nil-апдейт и nil-диспетчер — no-op
}

// waitFor ждёт выполнения условия (апдейты обрабатываются асинхронно).
func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal(msg)
}
