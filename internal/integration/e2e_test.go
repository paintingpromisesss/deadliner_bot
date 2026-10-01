// Package integration — сквозной тест собранного приложения (спека §11):
// реальный PostgreSQL (testcontainers), реальные репозитории и сервисы,
// настоящий chi-роутер на httptest-сервере, фейковый транспорт Telegram.
//
// Проверяется ровно то, что не видно из юнит-тестов: стыки между слоями.
// Сценарий повторяет путь пользователя целиком — вход по initData → создание
// группы → привязка чата → claim (код в чат) → роль admin → групповой дедлайн
// с кастомным напоминанием → отправка напоминания воркером (чат + дубли в ЛС)
// → завершение дедлайна → cleanup не трогает живую группу.
package integration

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/sauron/deadliner/internal/app/auth"
	"github.com/sauron/deadliner/internal/app/claims"
	"github.com/sauron/deadliner/internal/app/deadlines"
	"github.com/sauron/deadliner/internal/app/groups"
	"github.com/sauron/deadliner/internal/app/moderation"
	"github.com/sauron/deadliner/internal/app/notifications"
	"github.com/sauron/deadliner/internal/domain"
	"github.com/sauron/deadliner/internal/i18n"
	"github.com/sauron/deadliner/internal/platform/db"
	"github.com/sauron/deadliner/internal/platform/httpapi"
	"github.com/sauron/deadliner/internal/platform/repo"
	"github.com/sauron/deadliner/internal/platform/scheduler"
	"github.com/sauron/deadliner/internal/platform/telegram"
)

// Параметры сценария: чат группы, telegram_id участников, слага.
const (
	testBotToken = "424242:TEST-TOKEN"
	testChatID   = int64(-1001234567890)
	testSlug     = "ИКБО-33-21"

	tgCreator = int64(1001) // создатель группы → админ через claim
	tgMember  = int64(1002) // участник с включённым дублем в ЛС
	tgQuiet   = int64(1003) // участник с выключенными уведомлениями

	// testBotUserID — telegram id бота (префикс токена): хендлер /bind_group
	// спрашивает у ChatAdminChecker именно про него.
	testBotUserID = int64(424242)
)

// t0 — неподвижная точка отсчёта: все сроки считаются от неё, часы двигает
// только тест, поэтому сценарий детерминирован.
var t0 = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

var (
	testPool *pgxpool.Pool
	testCtr  testcontainers.Container
)

func TestMain(m *testing.M) {
	ctx := context.Background()

	ctr, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("deadliner_e2e"),
		tcpostgres.WithUsername("deadliner"),
		tcpostgres.WithPassword("deadliner"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(90*time.Second),
		),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "start postgres container: %v\n", err)
		os.Exit(1)
	}
	testCtr = ctr

	url, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintf(os.Stderr, "connection string: %v\n", err)
		os.Exit(1)
	}
	if err := db.RunUp(ctx, url); err != nil {
		fmt.Fprintf(os.Stderr, "migrate up: %v\n", err)
		os.Exit(1)
	}
	pool, err := db.Connect(ctx, url, 8)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect: %v\n", err)
		os.Exit(1)
	}
	testPool = pool

	// Каталог строк нужен всем слоям: роутер локализует ошибки, сервисы —
	// сообщения, воркер — текст напоминаний.
	i18n.MustLoad(i18n.Locales)

	code := m.Run()

	pool.Close()
	_ = ctr.Terminate(context.Background())
	os.Exit(code)
}

// --- фейковые часы ---

// fakeClock — управляемое время процесса: его получают и сервисы, и воркер,
// поэтому сдвиг часов в тесте сдвигает весь сценарий разом.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// --- фейковый транспорт Telegram ---

// recorded — перехваченное сообщение: чат группы (chatID < 0) или ЛС (userID).
type recorded struct {
	chatID    int64
	threadID  *int64
	userID    int64
	text      string
	button    string
	buttonURL string
}

type recorder struct {
	mu     sync.Mutex
	msgs   []recorded
	nextID int64
}

func (r *recorder) addChat(chatID int64, threadID *int64, m recorded) int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	m.chatID, m.threadID = chatID, threadID
	r.msgs = append(r.msgs, m)
	return r.nextID
}

func (r *recorder) addUser(userID int64, m recorded) int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	m.userID = userID
	r.msgs = append(r.msgs, m)
	return r.nextID
}

func (r *recorder) snapshot() []recorded {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]recorded(nil), r.msgs...)
}

// inChat — сообщения, ушедшие в конкретный чат группы.
func (r *recorder) inChat(chatID int64) []recorded {
	out := []recorded{}
	for _, m := range r.snapshot() {
		if m.chatID == chatID {
			out = append(out, m)
		}
	}
	return out
}

// inDM — сообщения, ушедшие в ЛС пользователя.
func (r *recorder) inDM(userID int64) []recorded {
	out := []recorded{}
	for _, m := range r.snapshot() {
		if m.userID == userID {
			out = append(out, m)
		}
	}
	return out
}

// fakeNotifier — подмена domain.Notifier для воркера: записывает отправки и не
// ходит в сеть (лимитер и ретраи Telegram здесь не проверяются — это предмет
// юнит-тестов notifier/scheduler).
type fakeNotifier struct {
	rec *recorder
}

func (n *fakeNotifier) SendToChat(_ context.Context, chatID, threadID int64, text string) error {
	n.rec.addChat(chatID, threadPtr(threadID), recorded{text: text})
	return nil
}

func (n *fakeNotifier) SendToUser(_ context.Context, userID int64, text string) error {
	n.rec.addUser(userID, recorded{text: text})
	return nil
}

// fakeClaimsNotifier — подмена claims.Notifier: код публикуется в чат и его
// message_id возвращается вызывающему (в проде — telegram.ClaimsSender).
type fakeClaimsNotifier struct {
	rec *recorder
}

func (n *fakeClaimsNotifier) SendToChat(_ context.Context, chatID, threadID int64, text string) (int64, error) {
	return n.rec.addChat(chatID, threadPtr(threadID), recorded{text: text, button: "chat"}), nil
}

func (n *fakeClaimsNotifier) SendToUser(_ context.Context, userID int64, text string) error {
	n.rec.addUser(userID, recorded{text: text})
	return nil
}

func threadPtr(id int64) *int64 {
	if id == 0 {
		return nil
	}
	return &id
}

// fakeMsgSender — транспорт хендлеров бота (telegram.MessageSender): ответы
// команд пишутся в общий recorder, кнопки сохраняются для проверок.
type fakeMsgSender struct {
	rec *recorder
}

func (s *fakeMsgSender) Send(_ context.Context, m telegram.OutMessage) (int64, error) {
	return s.rec.addChat(m.ChatID, m.ThreadID, recorded{
		text: m.Text, button: m.ButtonText, buttonURL: m.ButtonURL,
	}), nil
}

// fakeChatAdmin — проверка «бот — админ чата» (telegram.ChatAdminChecker).
// Сценарий использует её через настоящий хендлер /bind_group: привязка чата
// существует ТОЛЬКО в боте (в REST API такого эндпоинта нет), поэтому сквозной
// тест обязан пройти именно через диспетчер апдейтов.
type fakeChatAdmin struct {
	admin bool
	// calls — запрошенные (chatID, botUserID): проверка, что handler запрашивает
	// админство именно для бота в целевом чате, а не «просто true».
	calls []string
}

func (c *fakeChatAdmin) IsChatAdmin(_ context.Context, chatID, userID int64) (bool, error) {
	c.calls = append(c.calls, fmt.Sprintf("%d/%d", chatID, userID))
	return c.admin, nil
}

// --- сборка приложения ---

// env — собранное приложение сценария: реальные репозитории и сервисы поверх
// контейнерной БД, реальный роутер и фейковый транспорт Telegram.
type env struct {
	pool  *pgxpool.Pool
	clock *fakeClock
	rec   *recorder
	log   *slog.Logger

	users         domain.UserRepo
	groups        *groups.Service
	claims        *claims.Service
	deadlines     *deadlines.Service
	notifications *notifications.Service
	moderation    *moderation.Service

	worker       *scheduler.Worker
	router       http.Handler
	handlers     *telegram.Handlers
	adminChecker *fakeChatAdmin
}

func newEnv(t *testing.T) *env {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if _, err := testPool.Exec(ctx, `TRUNCATE
		users, groups, chat_bindings, group_memberships, deadlines, reminders,
		invites, claim_codes, user_action_counters, chat_action_counters, sessions,
		outbox_messages, audit_log CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	clock := &fakeClock{now: t0}
	rec := &recorder{}

	users := repo.NewUsers(testPool)
	sessions := repo.NewSessions(testPool)
	groupsRepo := repo.NewGroups(testPool)
	members := repo.NewMemberships(testPool)
	deadlinesRepo := repo.NewDeadlines(testPool)
	reminders := repo.NewReminders(testPool)
	claimsRepo := repo.NewClaims(testPool)
	invites := repo.NewInvites(testPool)
	counters := repo.NewCounters(testPool)
	chatCounters := repo.NewChatCounters(testPool)
	bindings := repo.NewBindings(testPool)
	audit := repo.NewAudit(testPool)
	maintenance := repo.NewMaintenance(testPool)

	groupsSvc := groups.NewService(groupsRepo, members, invites, counters, audit, bindings,
		groups.Config{
			PendingTTL:       14 * 24 * time.Hour,
			CreateDayLimit:   3,
			CreateWeekLimit:  5,
			InviteDefaultTTL: 7 * 24 * time.Hour,
		}, clock, log)

	claimsSvc := claims.NewService(groupsRepo, members, bindings, claimsRepo,
		counters, chatCounters, users, audit, &fakeClaimsNotifier{rec: rec},
		claims.Config{
			CodeTTL:          10 * time.Minute,
			RequestHourLimit: 3,
			Cooldown:         time.Minute,
			ConfirmFailLimit: 10,
		}, clock, log)

	deadlinesSvc := deadlines.NewService(deadlinesRepo, reminders, groupsRepo, members, audit, clock, log)
	notificationsSvc := notifications.NewService(users, members, groupsRepo)

	moderationSvc := moderation.NewService(moderation.Deps{
		Groups: groupsRepo, Deadlines: deadlinesRepo, Reminders: reminders,
		Memberships: members, Bindings: bindings, Users: users, Sessions: sessions,
		Maintenance: maintenance, Audit: audit, Clock: clock, Log: log,
		Config: moderation.Config{
			PendingTTL:       14 * 24 * time.Hour,
			CounterRetention: 192 * time.Hour,
		},
	})

	authSvc := auth.NewService(users, sessions, auth.Config{
		BotToken:       testBotToken,
		AuthDateMaxAge: 24 * time.Hour,
		SessionTTL:     30 * 24 * time.Hour,
	}, clock)

	router := httpapi.New(httpapi.Deps{
		Auth:          authSvc,
		Groups:        groupsSvc,
		Deadlines:     deadlinesSvc,
		Claims:        claimsSvc,
		Notifications: notificationsSvc,
		Users:         users,
		Sessions:      sessions,
		Log:           log,
		I18nLoaded:    true,
		SessionTTL:    30 * 24 * time.Hour,
	})

	// Тот же воркер, что собирает serve: реальный пул, реальные репозитории,
	// фейковый транспорт вместо Telegram.
	worker := scheduler.New(scheduler.Deps{
		Pool:        testPool,
		Reminders:   reminders,
		Deadlines:   deadlinesRepo,
		Memberships: members,
		Groups:      groupsRepo,
		Bindings:    bindings,
		Users:       users,
		Notifier:    &fakeNotifier{rec: rec},
		Clock:       clock,
		Log:         log,
	}, scheduler.Config{
		PollInterval: time.Minute,
		Batch:        50,
		LockTTL:      2 * time.Minute,
		MaxAttempts:  5,
		WorkerID:     "e2e-worker",
		// Ноль — дефолт пакета (scheduler.DefaultFinalizeTimeout), как в serve:
		// тест не должен закреплять собственную копию бюджета.
	})

	// Хендлеры бота (диспетчер апдейтов) собираются на тех же сервисах, что и
	// REST API — иначе сценарий проверял бы не тот код, что работает в проде.
	// Транспорт — фейковый recorder; проверка админства — управляемый чекер.
	adminChecker := &fakeChatAdmin{admin: true}
	handlers := telegram.NewHandlers(telegram.HandlersDeps{
		Users:        users,
		Binder:       groupsSvc,
		Superadmin:   moderationSvc,
		Sender:       &fakeMsgSender{rec: rec},
		AdminChecker: adminChecker,
		BotUserID:    testBotUserID,
		AppPublicURL: "https://deadliner.example/app",
	}, log)

	return &env{
		pool: testPool, clock: clock, rec: rec, log: log,
		users: users, groups: groupsSvc, claims: claimsSvc,
		deadlines: deadlinesSvc, notifications: notificationsSvc,
		moderation: moderationSvc, worker: worker, router: router,
		handlers: handlers, adminChecker: adminChecker,
	}
}

// --- HTTP-хелперы ---

// signInitData подписывает поля initData по официальному алгоритму Telegram
// (тот же, что проверяет webapp.ValidateInitData и как это делают
// httpapi-тесты): secret = HMAC("WebAppData", botToken), подпись — по
// отсортированным парам без hash.
func signInitData(t *testing.T, fields url.Values) string {
	t.Helper()
	secret := hmac.New(sha256.New, []byte("WebAppData"))
	secret.Write([]byte(testBotToken))

	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var check string
	for i, k := range keys {
		if i > 0 {
			check += "\n"
		}
		check += k + "=" + fields.Get(k)
	}
	mac := hmac.New(sha256.New, secret.Sum(nil))
	mac.Write([]byte(check))
	fields.Set("hash", hex.EncodeToString(mac.Sum(nil)))
	return fields.Encode()
}

// initDataFor — валидный initData для telegram_id с заданным именем.
func initDataFor(t *testing.T, tgID int64, username, firstName string) string {
	t.Helper()
	user := fmt.Sprintf(`{"id":%d,"username":%q,"first_name":%q}`, tgID, username, firstName)
	return signInitData(t, url.Values{
		"auth_date": {strconv.FormatInt(t0.Unix(), 10)},
		"user":      {user},
	})
}

// doJSON выполняет запрос к реальному роутеру через httptest (без сети).
func doJSON(r http.Handler, method, path, bearer string, payload any) *httptest.ResponseRecorder {
	var body *bytes.Buffer
	if payload != nil {
		b, _ := json.Marshal(payload)
		body = bytes.NewBuffer(b)
	} else {
		body = bytes.NewBuffer(nil)
	}
	req := httptest.NewRequest(method, path, body)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)
	return resp
}

// login выполняет вход по initData и возвращает токен сессии.
func login(t *testing.T, e *env, tgID int64, username, firstName string) string {
	t.Helper()
	raw, _ := json.Marshal(map[string]string{"initData": initDataFor(t, tgID, username, firstName)})
	resp := httptest.NewRecorder()
	e.router.ServeHTTP(resp, httptest.NewRequest(http.MethodPost,
		"/api/v1/auth/telegram", bytes.NewReader(raw)))
	if resp.Code != http.StatusOK {
		t.Fatalf("POST /auth/telegram (tg=%d) = %d, want 200; body: %s", tgID, resp.Code, resp.Body)
	}
	var body struct {
		Token string `json:"token"`
		User  struct {
			ID int64 `json:"id"`
		} `json:"user"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode login: %v (%s)", err, resp.Body)
	}
	if body.Token == "" || body.User.ID == 0 {
		t.Fatalf("login response incomplete: %s", resp.Body)
	}
	return body.Token
}

// userIDByTelegram — users.id по telegram_id (для проверок dm_dup-целей).
func userIDByTelegram(t *testing.T, e *env, tgID int64) int64 {
	t.Helper()
	u, err := e.users.GetByTelegramID(context.Background(), tgID)
	if err != nil {
		t.Fatalf("users.GetByTelegramID(%d): %v", tgID, err)
	}
	return u.ID
}

// reminderStatuses — статусы reminders дедлайна в порядке kind (проверка
// фактического состояния очереди после прогонов воркера).
func reminderStatuses(t *testing.T, e *env, deadlineID int64) map[string][]string {
	t.Helper()
	rows, err := e.pool.Query(context.Background(),
		`SELECT kind, status FROM reminders WHERE deadline_id = $1 ORDER BY kind, id`, deadlineID)
	if err != nil {
		t.Fatalf("select reminders: %v", err)
	}
	defer rows.Close()

	out := map[string][]string{}
	for rows.Next() {
		var kind, status string
		if err := rows.Scan(&kind, &status); err != nil {
			t.Fatalf("scan reminder: %v", err)
		}
		out[kind] = append(out[kind], status)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate reminders: %v", err)
	}
	return out
}

// claimCodeRe — одноразовый код claim'а: ровно 6 цифр отдельным словом (в
// тексте каталога других чисел с такой длиной нет).
var claimCodeRe = regexp.MustCompile(`\b\d{6}\b`)

// firstChatText — текст первого сообщения, ушедшего в чат.
func firstChatText(t *testing.T, recs []recorded) string {
	t.Helper()
	if len(recs) == 0 {
		t.Fatal("no messages were sent to the chat")
	}
	return recs[0].text
}

// lastChatText — текст последнего сообщения, ушедшего в чат (ответ на команду).
func lastChatText(t *testing.T, recs []recorded) string {
	t.Helper()
	if len(recs) == 0 {
		t.Fatal("no messages were sent to the chat")
	}
	return recs[len(recs)-1].text
}

// sendBotCommand прогоняет апдейт через настоящий диспетчер хендлеров
// (telegram.Handlers.Handle) — тот же путь, которым идёт апдейт от Telegram
// в polling/webhook-режиме. chatID < 0 — группа, > 0 — ЛС.
func (e *env) sendBotCommand(t *testing.T, chatID int64, threadID *int64, fromID int64, firstName, text string) {
	t.Helper()
	chatType := models.ChatTypeGroup
	if chatID > 0 {
		chatType = models.ChatTypePrivate
	}
	msg := &models.Message{
		ID:   1,
		From: &models.User{ID: fromID, FirstName: firstName, Username: fmt.Sprintf("u%d", fromID)},
		Chat: models.Chat{ID: chatID, Type: chatType, Title: "Тестовый чат"},
		Text: text,
	}
	if threadID != nil {
		msg.MessageThreadID = int(*threadID)
	}
	e.handlers.Handle(context.Background(), &models.Update{ID: 1, Message: msg})
}

// --- сценарий ---

// TestE2E_DeadlineLifecycle — сквозной путь от входа до отправленного
// напоминания и уборки. Шаги пронумерованы и повторяют §3/§6/§7 спеки.
func TestE2E_DeadlineLifecycle(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	const step = "шаг"

	t.Log(step, "1: вход по initData (создатель группы)")
	creatorToken := login(t, e, tgCreator, "creator", "Иван")

	// --- группа ---
	t.Log(step, "2: создание группы → pending")
	resp := doJSON(e.router, http.MethodPost, "/api/v1/groups", creatorToken,
		map[string]string{"slug": testSlug, "title": "ИКБО-33-21"})
	if resp.Code != http.StatusCreated {
		t.Fatalf("POST /groups = %d, want 201; body: %s", resp.Code, resp.Body)
	}
	var created struct {
		Group struct {
			ID     int64  `json:"id"`
			Status string `json:"status"`
		} `json:"group"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode group: %v (%s)", err, resp.Body)
	}
	groupID := created.Group.ID
	if groupID == 0 || created.Group.Status != string(domain.GroupStatusPending) {
		t.Fatalf("group = %+v, want pending group with id", created.Group)
	}

	t.Log(step, "3: привязка чата — через настоящий /bind_group в чате группы")
	// Привязка идёт ровно тем путём, что и в проде: апдейт от Telegram →
	// хендлер → ChatAdminChecker → groups.BindChat. В REST API такого
	// эндпоинта нет, поэтому «привязать напрямую через сервис» проверяло бы
	// не тот код, который работает у пользователя.
	e.sendBotCommand(t, testChatID, nil, tgCreator, "Иван", "/bind_group "+testSlug)
	bindReply := lastChatText(t, e.rec.inChat(testChatID))
	if !strings.Contains(bindReply, i18n.EscapeHTML("ИКБО-33-21")) {
		t.Fatalf("/bind_group reply = %q, want it to name the bound group", bindReply)
	}
	if len(e.adminChecker.calls) == 0 {
		t.Fatal("/bind_group did not consult the chat-admin checker")
	}
	if got := e.adminChecker.calls[len(e.adminChecker.calls)-1]; got != fmt.Sprintf("%d/%d", testChatID, testBotUserID) {
		t.Errorf("admin check = %q, want %q (bot id in the target chat)",
			got, fmt.Sprintf("%d/%d", testChatID, testBotUserID))
	}

	// --- claim: код публикуется в чат ---
	// Считаем сообщения ПОСЛЕ привязки: ответ /bind_group в чате уже есть, и
	// привязка к его количеству сделала бы тест хрупким.
	afterBind := len(e.rec.inChat(testChatID))

	t.Log(step, "4: claim start — код уходит в чат")
	resp = doJSON(e.router, http.MethodPost,
		fmt.Sprintf("/api/v1/groups/%d/claim/start", groupID), creatorToken, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("POST claim/start = %d, want 200; body: %s", resp.Code, resp.Body)
	}
	chatMsgs := e.rec.inChat(testChatID)
	if len(chatMsgs) != afterBind+1 {
		t.Fatalf("chat messages after claim start = %d, want %d (only the code message added)",
			len(chatMsgs), afterBind+1)
	}
	codeMsg := chatMsgs[len(chatMsgs)-1]
	code := claimCodeRe.FindString(codeMsg.text)
	if code == "" {
		t.Fatalf("no 6-digit claim code in chat message: %q", codeMsg.text)
	}

	t.Log(step, "5: claim confirm — роль admin, группа active")
	resp = doJSON(e.router, http.MethodPost,
		fmt.Sprintf("/api/v1/groups/%d/claim/confirm", groupID), creatorToken,
		map[string]string{"code": code})
	if resp.Code != http.StatusOK {
		t.Fatalf("POST claim/confirm = %d, want 200; body: %s", resp.Code, resp.Body)
	}
	var confirmed struct {
		Role   string `json:"role"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &confirmed); err != nil {
		t.Fatalf("decode confirm: %v (%s)", err, resp.Body)
	}
	if confirmed.Role != string(domain.RoleAdmin) || confirmed.Status != string(domain.GroupStatusActive) {
		t.Fatalf("confirm = %+v, want role=admin status=active", confirmed)
	}

	// --- участники через инвайт ---
	t.Log(step, "6: инвайт и двое участников (дубль в ЛС — только у одного)")
	resp = doJSON(e.router, http.MethodPost,
		fmt.Sprintf("/api/v1/groups/%d/invites", groupID), creatorToken,
		map[string]any{"role": "member"})
	if resp.Code != http.StatusCreated {
		t.Fatalf("POST invites = %d, want 201; body: %s", resp.Code, resp.Body)
	}
	var invite struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &invite); err != nil {
		t.Fatalf("decode invite: %v (%s)", err, resp.Body)
	}

	memberToken := login(t, e, tgMember, "member", "Пётр")
	resp = doJSON(e.router, http.MethodPost, "/api/v1/invites/redeem", memberToken,
		map[string]string{"code": invite.Code})
	if resp.Code != http.StatusOK {
		t.Fatalf("POST invites/redeem (member) = %d, want 200; body: %s", resp.Code, resp.Body)
	}
	// Дубль в ЛС этому участнику включён явно (переопределение для группы).
	resp = doJSON(e.router, http.MethodPatch, "/api/v1/notifications/settings", memberToken,
		map[string]any{"group_id": groupID, "dm_notify": true})
	if resp.Code != http.StatusOK {
		t.Fatalf("PATCH notifications (member) = %d, want 200; body: %s", resp.Code, resp.Body)
	}

	quietToken := login(t, e, tgQuiet, "quiet", "Анна")
	resp = doJSON(e.router, http.MethodPost, "/api/v1/invites/redeem", quietToken,
		map[string]string{"code": invite.Code})
	if resp.Code != http.StatusOK {
		t.Fatalf("POST invites/redeem (quiet) = %d, want 200; body: %s", resp.Code, resp.Body)
	}

	// --- дедлайн с кастомным напоминанием ---
	t.Log(step, "7: групповой дедлайн due +30m, напоминание через +5m")
	const dueAfter, remindAfter = 30 * time.Minute, 5 * time.Minute
	// Заголовок с HTML — проверяем экранирование на пути воркера.
	const rawTitle = "<b>Сдать</b> лабу"
	resp = doJSON(e.router, http.MethodPost, "/api/v1/deadlines", creatorToken, map[string]any{
		"group_id":    groupID,
		"title":       rawTitle,
		"description": "до конца недели",
		"due_at":      e.clock.Now().Add(dueAfter).Format(time.RFC3339),
		"tz":          "Europe/Moscow",
		"reminders": []map[string]any{
			{"kind": "custom_offset", "offset_minutes": int(remindAfter / time.Minute)},
		},
	})
	if resp.Code != http.StatusCreated {
		t.Fatalf("POST /deadlines = %d, want 201; body: %s", resp.Code, resp.Body)
	}
	var deadlined struct {
		Deadline struct {
			ID     int64  `json:"id"`
			Status string `json:"status"`
		} `json:"deadline"`
		Reminders []struct {
			Kind          string    `json:"kind"`
			FireAt        time.Time `json:"fire_at"`
			Status        string    `json:"status"`
			OffsetMinutes *int      `json:"offset_minutes"`
		} `json:"reminders"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &deadlined); err != nil {
		t.Fatalf("decode deadline: %v (%s)", err, resp.Body)
	}
	deadlineID := deadlined.Deadline.ID
	if len(deadlined.Reminders) != 1 || deadlined.Reminders[0].Kind != string(domain.KindCustomOffset) {
		t.Fatalf("reminders = %+v, want one custom_offset", deadlined.Reminders)
	}
	wantFireAt := t0.Add(remindAfter)
	if !deadlined.Reminders[0].FireAt.Equal(wantFireAt) {
		t.Errorf("reminder fire_at = %s, want %s", deadlined.Reminders[0].FireAt, wantFireAt)
	}

	// --- воркер: чат + fan-out ---
	t.Log(step, "8: часы +5m, прогон воркера — напоминание в чат и дубли в ЛС")
	e.clock.advance(remindAfter)
	if err := e.worker.Tick(ctx); err != nil {
		t.Fatalf("worker.Tick #1: %v", err)
	}

	chatMsgs = e.rec.inChat(testChatID)
	if len(chatMsgs) != afterBind+2 { // ответ /bind_group + код claim'а + напоминание
		t.Fatalf("chat messages = %d, want %d (bind reply + claim code + reminder)",
			len(chatMsgs), afterBind+2)
	}
	text := lastChatText(t, chatMsgs)
	// Текст собирается из i18n-шаблона: проверяем и разметку, и экранирование.
	if !strings.Contains(text, "25 минут") {
		t.Errorf("reminder text %q does not carry the humanized interval (%s - %s)", text, dueAfter, remindAfter)
	}
	if !strings.Contains(text, i18n.EscapeHTML(testSlug)) {
		t.Errorf("reminder text %q does not carry the group slug", text)
	}
	if !strings.Contains(text, "&lt;b&gt;Сдать&lt;/b&gt; лабу") {
		t.Errorf("reminder text %q: title is not HTML-escaped", text)
	}
	if strings.Contains(text, "<b>Сдать") {
		t.Errorf("reminder text %q contains the raw title markup", text)
	}
	if !strings.Contains(text, "01.10.2026") {
		t.Errorf("reminder text %q does not carry the local due date", text)
	}

	// Родитель зафиксирован как sent, дети dm_dup созданы — но ещё не отправлены.
	statuses := reminderStatuses(t, e, deadlineID)
	if got := statuses[string(domain.KindCustomOffset)]; len(got) != 1 || got[0] != string(domain.ReminderStatusSent) {
		t.Fatalf("custom_offset statuses = %v, want [sent]", got)
	}
	if got := statuses[string(domain.KindDMDup)]; len(got) != 1 || got[0] != string(domain.ReminderStatusPending) {
		t.Fatalf("dm_dup statuses after fan-out = %v, want one [pending]", got)
	}

	t.Log(step, "9: второй прогон — дубль уходит только подписчику")
	if err := e.worker.Tick(ctx); err != nil {
		t.Fatalf("worker.Tick #2: %v", err)
	}
	memberDMs := e.rec.inDM(tgMember)
	if len(memberDMs) != 1 {
		t.Fatalf("DMs to tg=%d = %d, want 1 (dm_dup)", tgMember, len(memberDMs))
	}
	if !strings.Contains(memberDMs[0].text, i18n.EscapeHTML(testSlug)) {
		t.Errorf("dm_dup text %q does not name the group", memberDMs[0].text)
	}
	if quietDMs := e.rec.inDM(tgQuiet); len(quietDMs) != 0 {
		t.Errorf("DMs to tg=%d = %d, want 0 (dm_notify is off)", tgQuiet, len(quietDMs))
	}
	statuses = reminderStatuses(t, e, deadlineID)
	if got := statuses[string(domain.KindDMDup)]; len(got) != 1 || got[0] != string(domain.ReminderStatusSent) {
		t.Fatalf("dm_dup statuses after delivery = %v, want [sent]", got)
	}

	// --- завершение дедлайна ---
	t.Log(step, "10: complete — статус done")
	resp = doJSON(e.router, http.MethodPost,
		fmt.Sprintf("/api/v1/deadlines/%d/complete", deadlineID), creatorToken, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("POST complete = %d, want 200; body: %s", resp.Code, resp.Body)
	}
	var completed struct {
		Deadline struct {
			Status string `json:"status"`
		} `json:"deadline"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &completed); err != nil {
		t.Fatalf("decode complete: %v (%s)", err, resp.Body)
	}
	if completed.Deadline.Status != string(domain.DeadlineStatusDone) {
		t.Fatalf("deadline status = %q, want done", completed.Deadline.Status)
	}

	// --- cleanup ---
	t.Log(step, "11: cleanup удаляет протухшую pending-группу и не трогает живую")
	resp = doJSON(e.router, http.MethodPost, "/api/v1/groups", creatorToken,
		map[string]string{"slug": "М8О-401Б-23", "title": "Брошенная группа"})
	if resp.Code != http.StatusCreated {
		t.Fatalf("POST /groups (stale) = %d, want 201; body: %s", resp.Code, resp.Body)
	}
	var stale struct {
		Group struct {
			ID int64 `json:"id"`
		} `json:"group"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &stale); err != nil {
		t.Fatalf("decode stale group: %v (%s)", err, resp.Body)
	}

	// TTL pending-группы — 14 дней; живая группа защищена привязкой чата и
	// ролью admin, поэтому TTL к ней не применяется, хотя она тоже не pending.
	e.clock.advance(15 * 24 * time.Hour)
	report, err := e.moderation.CleanupExpiredPending(ctx)
	if err != nil {
		t.Fatalf("CleanupExpiredPending: %v", err)
	}
	if report.Groups != 1 {
		t.Errorf("cleanup removed %d groups, want 1 (the stale pending one)", report.Groups)
	}
	// Проверка «протухшая группа исчезла, живая осталась» идёт через доменные
	// вызовы: Get живого вызывающего (создателя) — та же видимость, что у TMA.
	creator, err := e.users.GetByTelegramID(ctx, tgCreator)
	if err != nil {
		t.Fatalf("users.GetByTelegramID(%d): %v", tgCreator, err)
	}
	if _, err := e.groups.Get(ctx, creator, stale.Group.ID); err == nil {
		t.Error("stale pending group is still visible after cleanup")
	} else if !isNotFound(err) {
		t.Errorf("stale group lookup error = %v, want ErrNotFound", err)
	}
	resp = doJSON(e.router, http.MethodGet, fmt.Sprintf("/api/v1/groups/%d", groupID), creatorToken, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("GET live group after cleanup = %d, want 200; body: %s", resp.Code, resp.Body)
	}
}

// isNotFound — доменный ErrNotFound в цепочке ошибок сервиса.
func isNotFound(err error) bool {
	type unwrapper interface{ Unwrap() error }
	for err != nil {
		if err == domain.ErrNotFound {
			return true
		}
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
