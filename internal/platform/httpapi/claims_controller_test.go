package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sauron/deadliner/internal/app/auth"
	"github.com/sauron/deadliner/internal/app/claims"
	"github.com/sauron/deadliner/internal/app/groups"
	"github.com/sauron/deadliner/internal/domain"
	"github.com/sauron/deadliner/internal/i18n"
	"github.com/sauron/deadliner/internal/platform/repo"
)

// captureNotifier — фейковый claims.Notifier: запоминает опубликованный в чат
// код (перехватывая его из текста) и ЛС-уведомления, без сети.
type captureNotifier struct {
	chatSends []captureSend
	dms       []int64
	chatErr   error
}

type captureSend struct {
	chatID   int64
	threadID int64
	text     string
}

func (n *captureNotifier) SendToChat(ctx context.Context, chatID, threadID int64, text string) (int64, error) {
	if n.chatErr != nil {
		return 0, n.chatErr
	}
	n.chatSends = append(n.chatSends, captureSend{chatID: chatID, threadID: threadID, text: text})
	return int64(500 + len(n.chatSends)), nil
}

func (n *captureNotifier) SendToUser(ctx context.Context, userID int64, text string) error {
	n.dms = append(n.dms, userID)
	return nil
}

// code — последний опубликованный код (последние 6 цифр строки claim.code_message).
func (n *captureNotifier) code(t *testing.T) string {
	t.Helper()
	if len(n.chatSends) == 0 {
		t.Fatal("no claim code was posted to the chat")
	}
	text := n.chatSends[len(n.chatSends)-1].text
	// Формат: «...код подтверждения: 012345. Действует 10 минут...».
	const marker = "подтверждения: "
	i := strings.Index(text, marker)
	if i < 0 {
		t.Fatalf("cannot find the code marker in %q", text)
	}
	rest := text[i+len(marker):]
	if len(rest) < 6 {
		t.Fatalf("code shorter than 6 digits in %q", text)
	}
	return rest[:6]
}

// newTestClaimsRouter — роутер на реальных репозиториях (общий postgres-
// контейнер пакета) с сервисами групп и claim-флоу на подставном нотификаторе.
func newTestClaimsRouter(t *testing.T, nfy *captureNotifier, cfg claims.Config) http.Handler {
	t.Helper()
	pool := newTestDB(t)
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	i18n.MustLoad(i18n.Locales)
	users := repo.NewUsers(pool)
	members := repo.NewMemberships(pool)
	return New(Deps{
		Auth: auth.NewService(users, repo.NewSessions(pool), auth.Config{
			BotToken:       testBotToken,
			AuthDateMaxAge: 24 * time.Hour,
			SessionTTL:     sessionTTL,
		}, domain.SystemClock{}),
		Groups: groups.NewService(
			repo.NewGroups(pool), members, repo.NewInvites(pool),
			repo.NewCounters(pool), repo.NewAudit(pool), repo.NewBindings(pool),
			groups.Config{
				PendingTTL:       14 * 24 * time.Hour,
				CreateDayLimit:   3,
				CreateWeekLimit:  5,
				InviteDefaultTTL: 7 * 24 * time.Hour,
			}, domain.SystemClock{}, log),
		Claims: claims.NewService(
			repo.NewGroups(pool), members, repo.NewBindings(pool), repo.NewClaims(pool),
			repo.NewCounters(pool), users, repo.NewAudit(pool), nfy,
			cfg, domain.SystemClock{}, log),
		Users:      users,
		Sessions:   repo.NewSessions(pool),
		Log:        log,
		I18nLoaded: true,
		SessionTTL: sessionTTL,
	})
}

func claimsDefaultConfig() claims.Config {
	return claims.Config{
		CodeTTL:          10 * time.Minute,
		RequestHourLimit: 3,
		Cooldown:         time.Minute,
	}
}

// bindChat привязывает чат к группе напрямую в БД (бот-команда /bind_group
// проверяется в telegram-тестах; здесь нужен только результат).
func bindChat(t *testing.T, groupID, chatID int64, threadID *int64) {
	t.Helper()
	if _, err := testPool.Exec(t.Context(),
		`INSERT INTO chat_bindings (group_id, chat_id, message_thread_id, chat_title, bound_by)
		 VALUES ($1, $2, $3, 'Чат группы', (SELECT created_by FROM groups WHERE id = $1))`,
		groupID, chatID, threadID); err != nil {
		t.Fatalf("bind chat: %v", err)
	}
}

// groupStatus читает статус группы из БД.
func groupStatus(t *testing.T, groupID int64) string {
	t.Helper()
	var status string
	if err := testPool.QueryRow(t.Context(),
		`SELECT status FROM groups WHERE id = $1`, groupID).Scan(&status); err != nil {
		t.Fatalf("read group status: %v", err)
	}
	return status
}

// roleOf читает роль пользователя (telegram_id) в группе.
func roleOf(t *testing.T, groupID, telegramID int64) string {
	t.Helper()
	var role string
	err := testPool.QueryRow(t.Context(),
		`SELECT m.role FROM group_memberships m JOIN users u ON u.id = m.user_id
		 WHERE m.group_id = $1 AND u.telegram_id = $2`, groupID, telegramID).Scan(&role)
	if err != nil {
		t.Fatalf("read role: %v", err)
	}
	return role
}

func TestClaimEndToEnd(t *testing.T) {
	nfy := &captureNotifier{}
	r := newTestClaimsRouter(t, nfy, claimsDefaultConfig())
	tok, _ := login(t, r, 6001)

	// Группа + привязка чата (payload claim'а требует чат).
	resp := doJSON(r, http.MethodPost, "/api/v1/groups", tok,
		map[string]any{"slug": "икбо-33-21", "title": "Моя группа"})
	if resp.Code != http.StatusCreated {
		t.Fatalf("POST /groups = %d; body: %s", resp.Code, resp.Body)
	}
	var created struct {
		Group struct {
			ID int64 `json:"id"`
		} `json:"group"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	gid := created.Group.ID
	thread := int64(9)
	bindChat(t, gid, -100500, &thread)

	// POST /claim/start → 200 {expires_at}: код уходит в чат (в топик).
	resp = doJSON(r, http.MethodPost, fmt.Sprintf("/api/v1/groups/%d/claim/start", gid), tok, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("POST claim/start = %d; body: %s", resp.Code, resp.Body)
	}
	var started struct {
		ExpiresAt time.Time `json:"expires_at"`
		ChatID    int64     `json:"chat_id"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &started); err != nil {
		t.Fatal(err)
	}
	if started.ChatID != -100500 || started.ExpiresAt.Before(time.Now()) {
		t.Errorf("start response = %+v", started)
	}
	if len(nfy.chatSends) != 1 || nfy.chatSends[0].threadID != 9 {
		t.Fatalf("chat sends = %+v, want one in thread 9", nfy.chatSends)
	}
	code := nfy.code(t)

	// POST /claim/confirm с верным кодом → 200 {role: admin}, группа active.
	resp = doJSON(r, http.MethodPost, fmt.Sprintf("/api/v1/groups/%d/claim/confirm", gid), tok,
		map[string]any{"code": code})
	if resp.Code != http.StatusOK {
		t.Fatalf("POST claim/confirm = %d; body: %s", resp.Code, resp.Body)
	}
	var confirmed struct {
		Role   string `json:"role"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &confirmed); err != nil {
		t.Fatal(err)
	}
	if confirmed.Role != "admin" || confirmed.Status != "active" {
		t.Errorf("confirm response = %+v, want admin/active", confirmed)
	}
	if got := groupStatus(t, gid); got != "active" {
		t.Errorf("group status in DB = %q, want active", got)
	}
	if got := roleOf(t, gid, 6001); got != "admin" {
		t.Errorf("role in DB = %q, want admin", got)
	}

	// Повторный confirm того же кода → 404 (код сгорел).
	resp = doJSON(r, http.MethodPost, fmt.Sprintf("/api/v1/groups/%d/claim/confirm", gid), tok,
		map[string]any{"code": code})
	if resp.Code != http.StatusNotFound {
		t.Errorf("second confirm = %d, want 404; body: %s", resp.Code, resp.Body)
	}
}

// Нет привязки чата → 409 с подсказкой про /bind_group.
func TestClaimStartWithoutBinding(t *testing.T) {
	nfy := &captureNotifier{}
	r := newTestClaimsRouter(t, nfy, claimsDefaultConfig())
	tok, _ := login(t, r, 6101)

	resp := doJSON(r, http.MethodPost, "/api/v1/groups", tok,
		map[string]any{"slug": "икбо-33-21", "title": "T"})
	if resp.Code != http.StatusCreated {
		t.Fatalf("POST /groups = %d", resp.Code)
	}
	var created struct {
		Group struct {
			ID int64 `json:"id"`
		} `json:"group"`
	}
	_ = json.Unmarshal(resp.Body.Bytes(), &created)

	resp = doJSON(r, http.MethodPost, fmt.Sprintf("/api/v1/groups/%d/claim/start", created.Group.ID), tok, nil)
	if resp.Code != http.StatusConflict {
		t.Fatalf("claim/start without binding = %d, want 409; body: %s", resp.Code, resp.Body)
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != "no_chat_binding" || body.Error.Message == "" {
		t.Errorf("error body = %+v, want code no_chat_binding with a message", body.Error)
	}
}

// 4-й запрос кода за час → 429 с Retry-After (cooldown снимается реальным
// временем: окно cooldown — 1 минута, поэтому здесь лимит проверяется с
// нулевым cooldown).
func TestClaimStartRateLimit(t *testing.T) {
	nfy := &captureNotifier{}
	r := newTestClaimsRouter(t, nfy, claims.Config{
		CodeTTL: 10 * time.Minute, RequestHourLimit: 3, Cooldown: time.Nanosecond,
	})
	tok, _ := login(t, r, 6201)

	resp := doJSON(r, http.MethodPost, "/api/v1/groups", tok, map[string]any{"slug": "икбо-33-21", "title": "T"})
	if resp.Code != http.StatusCreated {
		t.Fatalf("POST /groups = %d", resp.Code)
	}
	var created struct {
		Group struct {
			ID int64 `json:"id"`
		} `json:"group"`
	}
	_ = json.Unmarshal(resp.Body.Bytes(), &created)
	gid := created.Group.ID
	bindChat(t, gid, -100500, nil)

	path := fmt.Sprintf("/api/v1/groups/%d/claim/start", gid)
	for i := 1; i <= 3; i++ {
		if resp := doJSON(r, http.MethodPost, path, tok, nil); resp.Code != http.StatusOK {
			t.Fatalf("claim/start #%d = %d; body: %s", i, resp.Code, resp.Body)
		}
	}
	resp = doJSON(r, http.MethodPost, path, tok, nil)
	if resp.Code != http.StatusTooManyRequests {
		t.Fatalf("4th claim/start = %d, want 429; body: %s", resp.Code, resp.Body)
	}
	if resp.Header().Get("Retry-After") == "" {
		t.Errorf("429 response has no Retry-After header")
	}
}

// Неверный код → 403 без раскрытия; мусор в поле → 400.
func TestClaimConfirmWrongAndMalformedCode(t *testing.T) {
	nfy := &captureNotifier{}
	r := newTestClaimsRouter(t, nfy, claimsDefaultConfig())
	tok, _ := login(t, r, 6301)

	resp := doJSON(r, http.MethodPost, "/api/v1/groups", tok, map[string]any{"slug": "икбо-33-21", "title": "T"})
	if resp.Code != http.StatusCreated {
		t.Fatalf("POST /groups = %d", resp.Code)
	}
	var created struct {
		Group struct {
			ID int64 `json:"id"`
		} `json:"group"`
	}
	_ = json.Unmarshal(resp.Body.Bytes(), &created)
	gid := created.Group.ID
	bindChat(t, gid, -100500, nil)

	confirm := fmt.Sprintf("/api/v1/groups/%d/claim/confirm", gid)
	// Кода ещё нет → 404.
	if resp := doJSON(r, http.MethodPost, confirm, tok, map[string]any{"code": "000000"}); resp.Code != http.StatusNotFound {
		t.Errorf("confirm without active code = %d, want 404", resp.Code)
	}

	started := doJSON(r, http.MethodPost, fmt.Sprintf("/api/v1/groups/%d/claim/start", gid), tok, nil)
	if started.Code != http.StatusOK {
		t.Fatalf("claim/start = %d; body: %s", started.Code, started.Body)
	}
	code := nfy.code(t)
	wrong := "999999"
	if code == wrong {
		wrong = "000000"
	}
	if resp := doJSON(r, http.MethodPost, confirm, tok, map[string]any{"code": wrong}); resp.Code != http.StatusForbidden {
		t.Errorf("confirm(wrong) = %d, want 403; body: %s", resp.Code, resp.Body)
	}
	// Верный код после неверной попытки всё ещё работает.
	if resp := doJSON(r, http.MethodPost, confirm, tok, map[string]any{"code": code}); resp.Code != http.StatusOK {
		t.Errorf("confirm(correct after wrong) = %d; body: %s", resp.Code, resp.Body)
	}

	// Мусор и неверная длина — 400 (до обращения к сервису).
	for _, bad := range []string{"12345", "1234567", "abcdef", ""} {
		if resp := doJSON(r, http.MethodPost, confirm, tok, map[string]any{"code": bad}); resp.Code != http.StatusBadRequest {
			t.Errorf("confirm(code=%q) = %d, want 400", bad, resp.Code)
		}
	}
}

// Revoke: не-админ → 403, админ → 204, после отзыва код не работает.
// Cooldown в конфиге обнулён: тест делает два запроса кода одним пользователем.
func TestClaimRevoke(t *testing.T) {
	nfy := &captureNotifier{}
	r := newTestClaimsRouter(t, nfy, claims.Config{
		CodeTTL: 10 * time.Minute, RequestHourLimit: 3, Cooldown: time.Nanosecond,
	})
	adminTok, _ := login(t, r, 6401)
	memberTok, _ := login(t, r, 6402)

	resp := doJSON(r, http.MethodPost, "/api/v1/groups", adminTok, map[string]any{"slug": "икбо-33-21", "title": "T"})
	if resp.Code != http.StatusCreated {
		t.Fatalf("POST /groups = %d", resp.Code)
	}
	var created struct {
		Group struct {
			ID int64 `json:"id"`
		} `json:"group"`
	}
	_ = json.Unmarshal(resp.Body.Bytes(), &created)
	gid := created.Group.ID
	bindChat(t, gid, -100500, nil)

	// Создатель группы — member (спека §3.1), но не админ: отзывать не может.
	// Второй пользователь вообще не участник группы — тоже 403.
	for _, tok := range []string{adminTok, memberTok} {
		if resp := doJSON(r, http.MethodPost, fmt.Sprintf("/api/v1/groups/%d/claim/revoke", gid), tok, nil); resp.Code != http.StatusForbidden {
			t.Fatalf("revoke without admin role = %d, want 403; body: %s", resp.Code, resp.Body)
		}
	}

	// Claim создателем: код выпущен, введён и создатель становится админом.
	if resp := doJSON(r, http.MethodPost, fmt.Sprintf("/api/v1/groups/%d/claim/start", gid), adminTok, nil); resp.Code != http.StatusOK {
		t.Fatalf("claim/start = %d; body: %s", resp.Code, resp.Body)
	}
	code := nfy.code(t)
	if resp := doJSON(r, http.MethodPost, fmt.Sprintf("/api/v1/groups/%d/claim/confirm", gid), adminTok,
		map[string]any{"code": code}); resp.Code != http.StatusOK {
		t.Fatalf("claim/confirm = %d; body: %s", resp.Code, resp.Body)
	}

	// Теперь админ выпускает второй код и отзывает его → 204.
	if resp := doJSON(r, http.MethodPost, fmt.Sprintf("/api/v1/groups/%d/claim/start", gid), adminTok, nil); resp.Code != http.StatusOK {
		t.Fatalf("second claim/start = %d; body: %s", resp.Code, resp.Body)
	}
	revokedCode := nfy.code(t)
	resp = doJSON(r, http.MethodPost, fmt.Sprintf("/api/v1/groups/%d/claim/revoke", gid), adminTok, nil)
	if resp.Code != http.StatusNoContent {
		t.Fatalf("revoke as admin = %d, want 204; body: %s", resp.Code, resp.Body)
	}
	if resp := doJSON(r, http.MethodPost, fmt.Sprintf("/api/v1/groups/%d/claim/confirm", gid), adminTok,
		map[string]any{"code": revokedCode}); resp.Code != http.StatusNotFound {
		t.Errorf("confirm(revoked code) = %d, want 404", resp.Code)
	}
	// Админ может отозвать и код, выпущенный другим участником группы.
	addMember(t, gid, 6402)
	if resp := doJSON(r, http.MethodPost, fmt.Sprintf("/api/v1/groups/%d/claim/start", gid), memberTok, nil); resp.Code != http.StatusOK {
		t.Fatalf("member claim/start = %d; body: %s", resp.Code, resp.Body)
	}
	if resp := doJSON(r, http.MethodPost, fmt.Sprintf("/api/v1/groups/%d/claim/revoke", gid), adminTok, nil); resp.Code != http.StatusNoContent {
		t.Errorf("revoke of another member's code = %d, want 204", resp.Code)
	}
	// Нечего отзывать → 404.
	if resp := doJSON(r, http.MethodPost, fmt.Sprintf("/api/v1/groups/%d/claim/revoke", gid), adminTok, nil); resp.Code != http.StatusNotFound {
		t.Errorf("revoke without active code = %d, want 404", resp.Code)
	}
}

// addMember добавляет пользователя (telegram_id) в группу напрямую в БД:
// вступление через инвайт проверяется в groups-тестах, здесь нужен только факт
// участия (claim требует членства).
func addMember(t *testing.T, groupID, telegramID int64) {
	t.Helper()
	if _, err := testPool.Exec(t.Context(),
		`INSERT INTO group_memberships (group_id, user_id)
		 SELECT $1, id FROM users WHERE telegram_id = $2
		 ON CONFLICT (group_id, user_id) DO NOTHING`, groupID, telegramID); err != nil {
		t.Fatalf("add member: %v", err)
	}
}

// Не-участник группы → 403: код в чужой чат не публикуется.
func TestClaimStartNonMemberForbidden(t *testing.T) {
	nfy := &captureNotifier{}
	r := newTestClaimsRouter(t, nfy, claimsDefaultConfig())
	ownerTok, _ := login(t, r, 6801)
	outsiderTok, _ := login(t, r, 6802)

	resp := doJSON(r, http.MethodPost, "/api/v1/groups", ownerTok, map[string]any{"slug": "икбо-33-21", "title": "T"})
	if resp.Code != http.StatusCreated {
		t.Fatalf("POST /groups = %d", resp.Code)
	}
	var created struct {
		Group struct {
			ID int64 `json:"id"`
		} `json:"group"`
	}
	_ = json.Unmarshal(resp.Body.Bytes(), &created)
	gid := created.Group.ID
	bindChat(t, gid, -100500, nil)

	resp = doJSON(r, http.MethodPost, fmt.Sprintf("/api/v1/groups/%d/claim/start", gid), outsiderTok, nil)
	if resp.Code != http.StatusForbidden {
		t.Fatalf("claim/start as non-member = %d, want 403; body: %s", resp.Code, resp.Body)
	}
	if len(nfy.chatSends) != 0 {
		t.Errorf("no code must be posted for a non-member, got %+v", nfy.chatSends)
	}
}

// Эндпоинты требуют авторизации; неизвестная группа → 404.
func TestClaimAuthAndUnknownGroup(t *testing.T) {
	nfy := &captureNotifier{}
	r := newTestClaimsRouter(t, nfy, claimsDefaultConfig())
	tok, _ := login(t, r, 6501)

	for _, path := range []string{"/claim/start", "/claim/confirm", "/claim/revoke"} {
		p := fmt.Sprintf("/api/v1/groups/1%s", path)
		if resp := doJSON(r, http.MethodPost, p, "", map[string]any{"code": "000000"}); resp.Code != http.StatusUnauthorized {
			t.Errorf("POST %s without token = %d, want 401", p, resp.Code)
		}
	}
	if resp := doJSON(r, http.MethodPost, "/api/v1/groups/999999/claim/start", tok, nil); resp.Code != http.StatusNotFound {
		t.Errorf("claim/start of unknown group = %d, want 404", resp.Code)
	}
	if resp := doJSON(r, http.MethodPost, "/api/v1/groups/abc/claim/start", tok, nil); resp.Code != http.StatusBadRequest {
		t.Errorf("claim/start with non-numeric id = %d, want 400", resp.Code)
	}
}

// Смена старосты: существующий админ уведомляется в ЛС при запросе кода
// (спека §3.1), claim при этом не блокируется.
func TestClaimStartNotifiesExistingAdmins(t *testing.T) {
	nfy := &captureNotifier{}
	r := newTestClaimsRouter(t, nfy, claimsDefaultConfig())
	adminTok, _ := login(t, r, 6601)
	otherTok, _ := login(t, r, 6602)

	resp := doJSON(r, http.MethodPost, "/api/v1/groups", adminTok, map[string]any{"slug": "икбо-33-21", "title": "T"})
	if resp.Code != http.StatusCreated {
		t.Fatalf("POST /groups = %d", resp.Code)
	}
	var created struct {
		Group struct {
			ID int64 `json:"id"`
		} `json:"group"`
	}
	_ = json.Unmarshal(resp.Body.Bytes(), &created)
	gid := created.Group.ID
	bindChat(t, gid, -100500, nil)

	// Первый claim: создатель становится админом.
	if resp := doJSON(r, http.MethodPost, fmt.Sprintf("/api/v1/groups/%d/claim/start", gid), adminTok, nil); resp.Code != http.StatusOK {
		t.Fatalf("claim/start = %d", resp.Code)
	}
	if resp := doJSON(r, http.MethodPost, fmt.Sprintf("/api/v1/groups/%d/claim/confirm", gid), adminTok,
		map[string]any{"code": nfy.code(t)}); resp.Code != http.StatusOK {
		t.Fatalf("claim/confirm = %d; body: %s", resp.Code, resp.Body)
	}

	// Второй пользователь (участник группы) запрашивает код: действующий админ
	// получает ЛС-уведомление о смене старосты.
	addMember(t, gid, 6602)
	nfy.dms = nil
	if resp := doJSON(r, http.MethodPost, fmt.Sprintf("/api/v1/groups/%d/claim/start", gid), otherTok, nil); resp.Code != http.StatusOK {
		t.Fatalf("second claim/start = %d; body: %s", resp.Code, resp.Body)
	}
	if len(nfy.dms) != 1 || nfy.dms[0] != 6601 {
		t.Errorf("admin DMs = %v, want [6601]", nfy.dms)
	}
}

// Сбой публикации кода в чат → 409 и код не сохраняется (следующий запрос
// снова проходит, а confirm невозможен).
func TestClaimStartSendFailure(t *testing.T) {
	nfy := &captureNotifier{chatErr: fmt.Errorf("bot was kicked from the chat")}
	r := newTestClaimsRouter(t, nfy, claimsDefaultConfig())
	tok, _ := login(t, r, 6701)

	resp := doJSON(r, http.MethodPost, "/api/v1/groups", tok, map[string]any{"slug": "икбо-33-21", "title": "T"})
	if resp.Code != http.StatusCreated {
		t.Fatalf("POST /groups = %d", resp.Code)
	}
	var created struct {
		Group struct {
			ID int64 `json:"id"`
		} `json:"group"`
	}
	_ = json.Unmarshal(resp.Body.Bytes(), &created)
	gid := created.Group.ID
	bindChat(t, gid, -100500, nil)

	resp = doJSON(r, http.MethodPost, fmt.Sprintf("/api/v1/groups/%d/claim/start", gid), tok, nil)
	if resp.Code != http.StatusConflict {
		t.Fatalf("claim/start with failing send = %d, want 409; body: %s", resp.Code, resp.Body)
	}
	var count int
	if err := testPool.QueryRow(t.Context(),
		`SELECT count(*) FROM claim_codes WHERE group_id = $1`, gid).Scan(&count); err != nil {
		t.Fatalf("count claim codes: %v", err)
	}
	if count != 0 {
		t.Errorf("claim_codes rows = %d, want 0 after a failed send", count)
	}
}
