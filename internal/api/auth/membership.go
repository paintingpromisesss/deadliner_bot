package auth

import (
	"context"
	"sync"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

type membershipCacheKey struct {
	ChatID int64
	UserID int64
}

type membershipCacheValue struct {
	Status    models.ChatMemberType
	ExpiresAt time.Time
}

// MembershipAuthorizer provides access to Telegram chat member statuses with TTL caching.
type MembershipAuthorizer struct {
	bot *tgbot.Bot
	ttl time.Duration

	mu    sync.RWMutex
	cache map[membershipCacheKey]membershipCacheValue
}

func NewMembershipAuthorizer(botClient *tgbot.Bot, ttl time.Duration) *MembershipAuthorizer {
	if ttl <= 0 {
		ttl = 2 * time.Minute
	}
	return &MembershipAuthorizer{
		bot:   botClient,
		ttl:   ttl,
		cache: make(map[membershipCacheKey]membershipCacheValue),
	}
}

func (a *MembershipAuthorizer) GetChatMemberType(ctx context.Context, chatID, userID int64) (models.ChatMemberType, error) {
	key := membershipCacheKey{ChatID: chatID, UserID: userID}
	now := time.Now()

	a.mu.RLock()
	cached, ok := a.cache[key]
	a.mu.RUnlock()
	if ok && cached.ExpiresAt.After(now) {
		return cached.Status, nil
	}

	member, err := a.bot.GetChatMember(ctx, &tgbot.GetChatMemberParams{
		ChatID: chatID,
		UserID: userID,
	})
	if err != nil {
		return "", err
	}

	a.mu.Lock()
	a.cache[key] = membershipCacheValue{
		Status:    member.Type,
		ExpiresAt: now.Add(a.ttl),
	}
	a.mu.Unlock()

	return member.Type, nil
}

func IsMemberStatus(status models.ChatMemberType) bool {
	return status != models.ChatMemberTypeLeft && status != models.ChatMemberTypeBanned
}

func IsAdminStatus(status models.ChatMemberType) bool {
	return status == models.ChatMemberTypeAdministrator || status == models.ChatMemberTypeOwner
}
