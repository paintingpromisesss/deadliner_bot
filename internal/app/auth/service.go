// Package auth — use case входа через Telegram initData и управления
// сессиями (спека §5.1): opaque-токен 32 байта hex, в БД — SHA-256 токена,
// TTL 30 дней sliding.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/sauron/deadliner/internal/domain"
	"github.com/sauron/deadliner/internal/webapp"
)

// Config — параметры авторизации из env (спека §8).
type Config struct {
	BotToken       string
	AuthDateMaxAge time.Duration
	SessionTTL     time.Duration
}

type Service struct {
	users    domain.UserRepo
	sessions domain.SessionRepo
	cfg      Config
	clock    domain.Clock
}

// NewService создаёт сервис авторизации. Валидация initData выполняется
// webapp.ValidateInitData (чистая крипто-функция без I/O).
func NewService(users domain.UserRepo, sessions domain.SessionRepo, cfg Config, clock domain.Clock) *Service {
	return &Service{users: users, sessions: sessions, cfg: cfg, clock: clock}
}

// HashToken возвращает SHA-256 hex токена — ключ хранения в sessions.
func HashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// GenerateToken создаёт opaque-токен сессии: 32 случайных байта в hex.
func GenerateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("auth: generate token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// Login валидирует initData, создаёт/обновляет пользователя и выдаёт токен
// сессии. Забаненным пользователям сессия не выдаётся (domain.ErrForbidden).
func (s *Service) Login(ctx context.Context, initData string) (*domain.User, string, error) {
	now := s.clock.Now()
	data, err := webapp.ValidateInitData(initData, s.cfg.BotToken, now, s.cfg.AuthDateMaxAge)
	if err != nil {
		return nil, "", err
	}

	u := &domain.User{
		TelegramID: data.User.TelegramID,
		Username:   data.User.Username,
		FirstName:  data.User.FirstName,
	}
	if err := s.users.UpsertByTelegram(ctx, u); err != nil {
		return nil, "", fmt.Errorf("auth: upsert user: %w", err)
	}
	// Upsert заполняет только ID/CreatedAt — полная гидратация отдельным чтением.
	full, err := s.users.GetByTelegramID(ctx, data.User.TelegramID)
	if err != nil {
		return nil, "", fmt.Errorf("auth: hydrate user: %w", err)
	}
	if full.IsBanned {
		return nil, "", fmt.Errorf("%w: user id=%d is banned", domain.ErrForbidden, full.ID)
	}

	token, err := GenerateToken()
	if err != nil {
		return nil, "", err
	}
	sess := &domain.Session{
		TokenHash: HashToken(token),
		UserID:    full.ID,
		ExpiresAt: now.Add(s.cfg.SessionTTL),
		CreatedAt: now,
		LastSeen:  now,
	}
	if err := s.sessions.Create(ctx, sess); err != nil {
		return nil, "", fmt.Errorf("auth: create session: %w", err)
	}
	return full, token, nil
}

// Logout отзывает сессию по токену.
func (s *Service) Logout(ctx context.Context, token string) error {
	if err := s.sessions.Revoke(ctx, HashToken(token)); err != nil {
		return fmt.Errorf("auth: revoke session: %w", err)
	}
	return nil
}
