package httpapi

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
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/sauron/deadliner/internal/app/auth"
	"github.com/sauron/deadliner/internal/domain"
	"github.com/sauron/deadliner/internal/i18n"
	"github.com/sauron/deadliner/internal/platform/db"
	"github.com/sauron/deadliner/internal/platform/repo"
)

// NOTE: контейнерная обвязка продублирована из internal/platform/repo/testutil_test.go —
// хелперы test-файлов не импортируются между пакетами, а выносить их в отдельный
// пакет ради одной задачи нецелесообразно.
// testPool доступен всем тестам пакета httpapi (promoteGroupAdmin и др.).
var testPool *pgxpool.Pool

const testBotToken = "TEST:TOKEN"

func TestMain(m *testing.M) {
	ctx := context.Background()

	ctr, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("deadliner_test"),
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

	code := m.Run()

	pool.Close()
	_ = ctr.Terminate(context.Background())
	os.Exit(code)
}

func newTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := testPool.Exec(ctx, `TRUNCATE
		users, groups, chat_bindings, group_memberships, deadlines, reminders,
		invites, user_action_counters, chat_action_counters, sessions,
		outbox_messages, audit_log CASCADE`)
	if err != nil {
		t.Fatalf("truncate: %v", err)
	}
	return testPool
}

func newTestRouter(t *testing.T) http.Handler {
	t.Helper()
	pool := newTestDB(t)
	users := repo.NewUsers(pool)
	sessions := repo.NewSessions(pool)
	ttl := 30 * 24 * time.Hour
	authSvc := auth.NewService(users, sessions, auth.Config{
		BotToken:       testBotToken,
		AuthDateMaxAge: 24 * time.Hour,
		SessionTTL:     ttl,
	}, domain.SystemClock{})
	i18n.MustLoad(i18n.Locales)
	r := New(Deps{
		Auth:       authSvc,
		Users:      users,
		Sessions:   sessions,
		Log:        slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		SessionTTL: ttl,
	})
	return r
}

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

func validInitData(t *testing.T, tgID int64) string {
	t.Helper()
	return signInitData(t, url.Values{
		"auth_date": {strconv.FormatInt(time.Now().Unix(), 10)},
		"user":      {`{"id":` + strconv.FormatInt(tgID, 10) + `,"username":"ivan","first_name":"Иван"}`},
	})
}

// login performs POST /api/v1/auth/telegram and returns the session token.
func login(t *testing.T, r http.Handler, tgID int64) (token string, body map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(map[string]string{"initData": validInitData(t, tgID)})
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, httptest.NewRequest(http.MethodPost, "/api/v1/auth/telegram", bytes.NewReader(raw)))
	if resp.Code != http.StatusOK {
		t.Fatalf("POST /auth/telegram = %d, want 200; body: %s", resp.Code, resp.Body)
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v (%s)", err, resp.Body)
	}
	token, _ = body["token"].(string)
	if token == "" {
		t.Fatalf("empty token in response: %s", resp.Body)
	}
	return token, body
}

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

func TestHealthz(t *testing.T) {
	r := newTestRouter(t)
	resp := doJSON(r, http.MethodGet, "/healthz", "", nil)
	if resp.Code != http.StatusOK || resp.Body.String() != "ok" {
		t.Fatalf("GET /healthz = %d %q, want 200 ok", resp.Code, resp.Body)
	}
}

func TestAuthTelegram_CreatesUserAndToken(t *testing.T) {
	r := newTestRouter(t)
	token, body := login(t, r, 42)
	if len(token) != 64 {
		t.Errorf("token len = %d, want 64 hex chars", len(token))
	}
	user, ok := body["user"].(map[string]any)
	if !ok {
		t.Fatalf("response has no user object: %v", body)
	}
	if user["telegram_id"] != float64(42) {
		t.Errorf("telegram_id = %v, want 42", user["telegram_id"])
	}
	if user["username"] != "ivan" {
		t.Errorf("username = %v, want ivan", user["username"])
	}
	if user["tz"] != "Europe/Moscow" {
		t.Errorf("tz = %v, want Europe/Moscow (schema default)", user["tz"])
	}
	for _, key := range []string{"id", "first_name", "dm_notify_default", "is_superadmin"} {
		if _, exists := user[key]; !exists {
			t.Errorf("user DTO missing key %q", key)
		}
	}
}

func TestAuthTelegram_InvalidInitData_403(t *testing.T) {
	r := newTestRouter(t)
	raw, _ := json.Marshal(map[string]string{"initData": "auth_date=1&hash=deadbeef"})
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, httptest.NewRequest(http.MethodPost, "/api/v1/auth/telegram", bytes.NewReader(raw)))
	if resp.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body: %s", resp.Code, resp.Body)
	}
	var e map[string]map[string]string
	if err := json.Unmarshal(resp.Body.Bytes(), &e); err != nil || e["error"]["code"] == "" {
		t.Fatalf("error envelope missing: %s", resp.Body)
	}
}

func TestAuthTelegram_BadJSON_400(t *testing.T) {
	r := newTestRouter(t)
	resp := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/telegram", bytes.NewReader([]byte(`{"nope":1}`)))
	r.ServeHTTP(resp, req)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", resp.Code, resp.Body)
	}
}

func TestGetMe_WithBearer(t *testing.T) {
	r := newTestRouter(t)
	token, _ := login(t, r, 42)
	resp := doJSON(r, http.MethodGet, "/api/v1/me", token, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("GET /me = %d, want 200; body: %s", resp.Code, resp.Body)
	}
	var user map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &user); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if user["telegram_id"] != float64(42) || user["username"] != "ivan" {
		t.Errorf("profile = %v", user)
	}
}

func TestGetMe_NoToken_401(t *testing.T) {
	r := newTestRouter(t)
	resp := doJSON(r, http.MethodGet, "/api/v1/me", "", nil)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("GET /me without token = %d, want 401; body: %s", resp.Code, resp.Body)
	}
}

func TestGetMe_BogusToken_401(t *testing.T) {
	r := newTestRouter(t)
	resp := doJSON(r, http.MethodGet, "/api/v1/me", strings.Repeat("ab", 32), nil)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("GET /me with bogus token = %d, want 401", resp.Code)
	}
}

func TestPatchMe_TZ_Persisted(t *testing.T) {
	r := newTestRouter(t)
	token, _ := login(t, r, 42)
	resp := doJSON(r, http.MethodPatch, "/api/v1/me", token, map[string]any{"tz": "Asia/Almaty"})
	if resp.Code != http.StatusOK {
		t.Fatalf("PATCH /me = %d, want 200; body: %s", resp.Code, resp.Body)
	}

	var tz string
	err := testPool.QueryRow(context.Background(),
		`SELECT tz FROM users WHERE telegram_id = 42`).Scan(&tz)
	if err != nil {
		t.Fatalf("read tz: %v", err)
	}
	if tz != "Asia/Almaty" {
		t.Errorf("tz in db = %q, want Asia/Almaty", tz)
	}
}

func TestPatchMe_DMNotifyDefault(t *testing.T) {
	r := newTestRouter(t)
	token, _ := login(t, r, 42)
	resp := doJSON(r, http.MethodPatch, "/api/v1/me", token, map[string]any{"dm_notify_default": true})
	if resp.Code != http.StatusOK {
		t.Fatalf("PATCH /me = %d, want 200; body: %s", resp.Code, resp.Body)
	}
	var dm bool
	if err := testPool.QueryRow(context.Background(),
		`SELECT dm_notify_default FROM users WHERE telegram_id = 42`).Scan(&dm); err != nil {
		t.Fatalf("read dm: %v", err)
	}
	if !dm {
		t.Error("dm_notify_default not persisted")
	}
}

func TestPatchMe_InvalidTZ_400(t *testing.T) {
	r := newTestRouter(t)
	token, _ := login(t, r, 42)
	resp := doJSON(r, http.MethodPatch, "/api/v1/me", token, map[string]any{"tz": "Mars/Olympus"})
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("PATCH /me invalid tz = %d, want 400; body: %s", resp.Code, resp.Body)
	}
}

func TestPatchMe_UnknownField_400(t *testing.T) {
	r := newTestRouter(t)
	token, _ := login(t, r, 42)
	resp := doJSON(r, http.MethodPatch, "/api/v1/me", token, map[string]any{"is_superadmin": true})
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("PATCH /me unknown field = %d, want 400; body: %s", resp.Code, resp.Body)
	}
}

// Спека §5.2: PATCH /me принимает first_name. Проверяем round-trip —
// ответ содержит новое имя и оно же лежит в БД (а не только в ответе).
func TestPatchMe_FirstNamePersisted(t *testing.T) {
	r := newTestRouter(t)
	token, _ := login(t, r, 42)

	resp := doJSON(r, http.MethodPatch, "/api/v1/me", token, map[string]any{"first_name": "  Пётр  "})
	if resp.Code != http.StatusOK {
		t.Fatalf("PATCH /me first_name = %d, want 200; body: %s", resp.Code, resp.Body)
	}
	var body struct {
		FirstName string `json:"first_name"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.FirstName != "Пётр" {
		t.Errorf("response first_name = %q, want trimmed Пётр", body.FirstName)
	}

	var dbName string
	if err := testPool.QueryRow(context.Background(),
		`SELECT first_name FROM users WHERE telegram_id = 42`).Scan(&dbName); err != nil {
		t.Fatalf("read first_name: %v", err)
	}
	if dbName != "Пётр" {
		t.Errorf("first_name in db = %q, want Пётр", dbName)
	}

	// Имя видно и на следующем GET /me — имя не «одноразовое» на ответ.
	resp = doJSON(r, http.MethodGet, "/api/v1/me", token, nil)
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.FirstName != "Пётр" {
		t.Errorf("GET /me first_name = %q, want Пётр", body.FirstName)
	}

	// Имя меняется вместе с настройками одним запросом (tz не откатывается).
	resp = doJSON(r, http.MethodPatch, "/api/v1/me", token,
		map[string]any{"first_name": "Иван", "tz": "Asia/Almaty"})
	if resp.Code != http.StatusOK {
		t.Fatalf("combined PATCH = %d, want 200; body: %s", resp.Code, resp.Body)
	}
	var tz string
	if err := testPool.QueryRow(context.Background(),
		`SELECT tz, first_name FROM users WHERE telegram_id = 42`).Scan(&tz, &dbName); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if tz != "Asia/Almaty" || dbName != "Иван" {
		t.Errorf("tz/first_name = %q/%q, want Asia/Almaty/Иван", tz, dbName)
	}
}

// Пустое (или пробельное) имя — 400: сброс оставил бы пользователя безымянным
// в списках участников.
func TestPatchMe_EmptyFirstName_400(t *testing.T) {
	r := newTestRouter(t)
	token, _ := login(t, r, 42)

	for _, v := range []string{"", "   "} {
		resp := doJSON(r, http.MethodPatch, "/api/v1/me", token, map[string]any{"first_name": v})
		if resp.Code != http.StatusBadRequest {
			t.Errorf("PATCH first_name=%q = %d, want 400; body: %s", v, resp.Code, resp.Body)
		}
	}

	// Имя не изменилось: валидация отклоняет ДО записи.
	var name string
	if err := testPool.QueryRow(context.Background(),
		`SELECT first_name FROM users WHERE telegram_id = 42`).Scan(&name); err != nil {
		t.Fatalf("read first_name: %v", err)
	}
	if name != "Иван" {
		t.Errorf("first_name = %q, want the original Иван (no partial write)", name)
	}
}

// Слишком длинное имя — 400 (граница 64 символа).
func TestPatchMe_LongFirstName_400(t *testing.T) {
	r := newTestRouter(t)
	token, _ := login(t, r, 42)

	resp := doJSON(r, http.MethodPatch, "/api/v1/me", token,
		map[string]any{"first_name": strings.Repeat("я", 65)})
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("PATCH /me long first_name = %d, want 400; body: %s", resp.Code, resp.Body)
	}
	// Ровно 64 — принимается.
	resp = doJSON(r, http.MethodPatch, "/api/v1/me", token,
		map[string]any{"first_name": strings.Repeat("я", 64)})
	if resp.Code != http.StatusOK {
		t.Fatalf("PATCH /me 64-char first_name = %d, want 200; body: %s", resp.Code, resp.Body)
	}
}

func TestLogout_KillsToken(t *testing.T) {
	r := newTestRouter(t)
	token, _ := login(t, r, 42)

	resp := doJSON(r, http.MethodPost, "/api/v1/me/logout", token, nil)
	if resp.Code != http.StatusNoContent {
		t.Fatalf("POST /me/logout = %d, want 204; body: %s", resp.Code, resp.Body)
	}

	resp = doJSON(r, http.MethodGet, "/api/v1/me", token, nil)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("GET /me after logout = %d, want 401", resp.Code)
	}
}
