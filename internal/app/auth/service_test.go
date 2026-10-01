package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/sauron/deadliner/internal/domain"
)

const testBotToken = "TEST:TOKEN"

// --- fakes ---

type fakeClock struct{ now time.Time }

func (c fakeClock) Now() time.Time { return c.now }

type fakeUserRepo struct {
	users        map[int64]*domain.User // by telegram id
	upsertCalls  int
	upsertErr    error
	hydrateCalls int
}

func newFakeUserRepo() *fakeUserRepo {
	return &fakeUserRepo{users: map[int64]*domain.User{}}
}

func (r *fakeUserRepo) UpsertByTelegram(ctx context.Context, u *domain.User) error {
	r.upsertCalls++
	if r.upsertErr != nil {
		return r.upsertErr
	}
	existing, ok := r.users[u.TelegramID]
	if !ok {
		existing = &domain.User{
			ID:              int64(len(r.users) + 1),
			TelegramID:      u.TelegramID,
			TZ:              "Europe/Moscow",
			DMNotifyDefault: true,
			CreatedAt:       time.Now(),
		}
		r.users[u.TelegramID] = existing
	}
	existing.Username = u.Username
	existing.FirstName = u.FirstName
	// Mirror the Task 4 repo limitation: only ID and CreatedAt are filled.
	u.ID = existing.ID
	u.CreatedAt = existing.CreatedAt
	return nil
}

func (r *fakeUserRepo) hydrate(telegramID int64) (*domain.User, bool) {
	u, ok := r.users[telegramID]
	if !ok {
		return nil, false
	}
	cp := *u
	return &cp, true
}

func (r *fakeUserRepo) GetByTelegramID(ctx context.Context, telegramID int64) (*domain.User, error) {
	r.hydrateCalls++
	u, ok := r.hydrate(telegramID)
	if !ok {
		return nil, domain.ErrNotFound
	}
	return u, nil
}

func (r *fakeUserRepo) GetByID(ctx context.Context, id int64) (*domain.User, error) {
	for _, u := range r.users {
		if u.ID == id {
			cp := *u
			return &cp, nil
		}
	}
	return nil, domain.ErrNotFound
}

func (r *fakeUserRepo) UpdateSettings(ctx context.Context, id int64, tz string, dm bool) error {
	return errors.New("not used")
}
func (r *fakeUserRepo) SetBanned(ctx context.Context, id int64, banned bool) error {
	return errors.New("not used")
}
func (r *fakeUserRepo) SetSuperadmin(ctx context.Context, id int64, sa bool) error {
	return errors.New("not used")
}
func (r *fakeUserRepo) MarkBotBlocked(ctx context.Context, tgID int64, b bool) error {
	return errors.New("not used")
}

// ListSuperadmins и UpdateProfile — части domain.UserRepo, не используемые
// сервисом auth: заглушки-нули, чтобы фейк продолжал удовлетворять порту.
func (r *fakeUserRepo) ListSuperadmins(ctx context.Context) ([]domain.User, error) {
	return nil, nil
}
func (r *fakeUserRepo) UpdateProfile(ctx context.Context, id int64, firstName string) error {
	return nil
}

type fakeSessionRepo struct {
	sessions     map[string]*domain.Session
	createCalls  int
	revokeCalls  int
	createErr    error
	lastTokenHas string
}

func newFakeSessionRepo() *fakeSessionRepo {
	return &fakeSessionRepo{sessions: map[string]*domain.Session{}}
}

func (r *fakeSessionRepo) Create(ctx context.Context, s *domain.Session) error {
	r.createCalls++
	if r.createErr != nil {
		return r.createErr
	}
	cp := *s
	r.sessions[s.TokenHash] = &cp
	r.lastTokenHas = s.TokenHash
	return nil
}

func (r *fakeSessionRepo) GetActive(ctx context.Context, tokenHash string, now time.Time) (*domain.Session, error) {
	s, ok := r.sessions[tokenHash]
	if !ok || !s.ExpiresAt.After(now) {
		return nil, domain.ErrNotFound
	}
	cp := *s
	return &cp, nil
}

func (r *fakeSessionRepo) Touch(ctx context.Context, tokenHash string, lastSeen, expiresAt time.Time) error {
	s, ok := r.sessions[tokenHash]
	if !ok {
		return domain.ErrNotFound
	}
	s.LastSeen = lastSeen
	s.ExpiresAt = expiresAt
	return nil
}

func (r *fakeSessionRepo) Revoke(ctx context.Context, tokenHash string) error {
	r.revokeCalls++
	if _, ok := r.sessions[tokenHash]; !ok {
		return domain.ErrNotFound
	}
	delete(r.sessions, tokenHash)
	return nil
}

func (r *fakeSessionRepo) RevokeAllForUser(ctx context.Context, userID int64) error {
	for h, s := range r.sessions {
		if s.UserID == userID {
			delete(r.sessions, h)
		}
	}
	return nil
}

// --- helpers ---

func signInitData(t *testing.T, fields url.Values, token string) string {
	t.Helper()
	secret := hmac.New(sha256.New, []byte("WebAppData"))
	secret.Write([]byte(token))
	keys := make([]string, 0, len(fields))
	for k := range fields {
		if k != "hash" {
			keys = append(keys, k)
		}
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

func newTestService(t *testing.T, now time.Time) (*Service, *fakeUserRepo, *fakeSessionRepo) {
	t.Helper()
	users := newFakeUserRepo()
	sessions := newFakeSessionRepo()
	svc := NewService(users, sessions, Config{
		BotToken:       testBotToken,
		AuthDateMaxAge: 24 * time.Hour,
		SessionTTL:     30 * 24 * time.Hour,
	}, fakeClock{now: now})
	return svc, users, sessions
}

func validInitData(t *testing.T, now time.Time, tgID int64) string {
	t.Helper()
	return signInitData(t, url.Values{
		"auth_date": {strconv.FormatInt(now.Unix(), 10)},
		"user":      {`{"id":` + strconv.FormatInt(tgID, 10) + `,"username":"ivan","first_name":"Иван"}`},
	}, testBotToken)
}

// --- tests ---

func TestLogin_HappyPath(t *testing.T) {
	now := time.Now()
	svc, users, sessions := newTestService(t, now)
	initData := validInitData(t, now, 42)

	user, token, err := svc.Login(context.Background(), initData)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if users.upsertCalls != 1 {
		t.Errorf("upsertCalls = %d, want 1", users.upsertCalls)
	}
	if users.hydrateCalls != 1 {
		t.Errorf("hydrateCalls = %d, want 1 (full hydration after upsert)", users.hydrateCalls)
	}
	if user == nil || user.TelegramID != 42 || user.ID == 0 {
		t.Fatalf("user = %+v, want hydrated user with telegram id 42", user)
	}
	if len(token) != 64 {
		t.Errorf("token = %q (len %d), want 32 bytes hex (len 64)", token, len(token))
	}
	if sessions.createCalls != 1 {
		t.Fatalf("createCalls = %d, want 1", sessions.createCalls)
	}
	// Stored token must be the SHA-256 hash, not the token itself.
	wantHash := sha256.Sum256([]byte(token))
	if got := hex.EncodeToString(wantHash[:]); got != sessions.lastTokenHas {
		t.Errorf("stored token_hash = %q, want sha256(token) = %q", sessions.lastTokenHas, got)
	}
	s := sessions.sessions[sessions.lastTokenHas]
	if !s.ExpiresAt.Equal(now.Add(30 * 24 * time.Hour)) {
		t.Errorf("ExpiresAt = %v, want %v", s.ExpiresAt, now.Add(30*24*time.Hour))
	}
	if s.UserID != user.ID {
		t.Errorf("session UserID = %d, want %d", s.UserID, user.ID)
	}
}

func TestLogin_BannedUser_NoSession(t *testing.T) {
	now := time.Now()
	svc, users, sessions := newTestService(t, now)
	initData := validInitData(t, now, 42)

	// Pre-create a banned user.
	users.users[42] = &domain.User{ID: 7, TelegramID: 42, Username: "ivan", FirstName: "Иван", IsBanned: true}

	_, _, err := svc.Login(context.Background(), initData)
	if !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("Login err = %v, want ErrForbidden", err)
	}
	if sessions.createCalls != 0 {
		t.Errorf("createCalls = %d, want 0 for banned user", sessions.createCalls)
	}
}

func TestLogin_InvalidInitData_NoUpsert(t *testing.T) {
	now := time.Now()
	svc, users, sessions := newTestService(t, now)

	_, _, err := svc.Login(context.Background(), "auth_date=1&hash=deadbeef")
	if !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("Login err = %v, want ErrForbidden", err)
	}
	if users.upsertCalls != 0 {
		t.Errorf("upsertCalls = %d, want 0", users.upsertCalls)
	}
	if sessions.createCalls != 0 {
		t.Errorf("createCalls = %d, want 0", sessions.createCalls)
	}
}

func TestLogin_WrongBotToken(t *testing.T) {
	now := time.Now()
	svc, _, sessions := newTestService(t, now)
	initData := signInitData(t, url.Values{
		"auth_date": {strconv.FormatInt(now.Unix(), 10)},
		"user":      {`{"id":42,"username":"ivan","first_name":"Иван"}`},
	}, "OTHER:TOKEN")

	if _, _, err := svc.Login(context.Background(), initData); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("Login err = %v, want ErrForbidden", err)
	}
	if sessions.createCalls != 0 {
		t.Errorf("createCalls = %d, want 0", sessions.createCalls)
	}
}

func TestLogout_RevokesSession(t *testing.T) {
	now := time.Now()
	svc, _, sessions := newTestService(t, now)
	initData := validInitData(t, now, 42)

	_, token, err := svc.Login(context.Background(), initData)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if err := svc.Logout(context.Background(), token); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if sessions.revokeCalls != 1 {
		t.Errorf("revokeCalls = %d, want 1", sessions.revokeCalls)
	}
	if len(sessions.sessions) != 0 {
		t.Errorf("sessions remain after logout: %d", len(sessions.sessions))
	}
}

func TestLogout_UnknownToken_NotFound(t *testing.T) {
	now := time.Now()
	svc, _, _ := newTestService(t, now)
	err := svc.Logout(context.Background(), "unknown-token")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Logout err = %v, want ErrNotFound", err)
	}
}
