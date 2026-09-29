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
// ok-ом на любой метод и запоминает, какие методы вызывались.
type stubTelegram struct {
	mu      sync.Mutex
	methods []string
}

func (s *stubTelegram) start(t *testing.T) string {
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
