package scheduler

import (
	"context"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Clock is the minimal clock surface the Limiter needs; domain.Clock fits.
// Tests inject a fake.
type Clock interface {
	Now() time.Time
}

// Limiter — двухуровневый rate limiter (спека §7.4): глобальный бакет
// (25/с, burst равен rate) и per-chat бакеты (18/мин, burst 18). Пенальти
// 429 (retry_after) держится как «запрет до» на конкретный chat: WaitChat
// честно ждёт окончания запрета перед выдачей токена.
type Limiter struct {
	global       *rate.Limiter
	perChatRate  rate.Limit
	perChatBurst int

	mu      sync.Mutex
	chats   map[int64]*rate.Limiter
	penalty map[int64]time.Time // chat → до какого момента запрещено
	nowFn   func() time.Time
	sleepFn func(ctx context.Context, d time.Duration) error
}

func NewLimiter(globalPerSec int, perChatPerMin int) *Limiter {
	g := globalPerSec
	if g <= 0 {
		g = 1
	}
	p := perChatPerMin
	if p <= 0 {
		p = 1
	}
	return &Limiter{
		global:       rate.NewLimiter(rate.Limit(g), g),
		perChatRate:  rate.Every(time.Minute / time.Duration(p)),
		perChatBurst: p,
		chats:        map[int64]*rate.Limiter{},
		penalty:      map[int64]time.Time{},
		nowFn:        time.Now,
		sleepFn:      ctxSleep,
	}
}

// WithClockAndSleep подменяет часы/сон (детерминированные тесты).
func (l *Limiter) WithClockAndSleep(nowFn func() time.Time, sleepFn func(ctx context.Context, d time.Duration) error) *Limiter {
	l.mu.Lock()
	l.nowFn = nowFn
	l.sleepFn = sleepFn
	l.mu.Unlock()
	return l
}

func ctxSleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Wait ждёт глобальный токен (§7.4: блокировка перед Send).
func (l *Limiter) Wait(ctx context.Context) error {
	return l.global.Wait(ctx)
}

// chatLimiter возвращает (создавая) бакет чата. Держит mu.
func (l *Limiter) chatLimiter(chatID int64) *rate.Limiter {
	lim, ok := l.chats[chatID]
	if !ok {
		lim = rate.NewLimiter(l.perChatRate, l.perChatBurst)
		l.chats[chatID] = lim
	}
	return lim
}

// WaitChat ждёт глобальный токен И токен чата (в этом порядке), а также
// выжидает активное пенальти чата. Пенальти не «съедает» токен чата — просто
// запрещает выдачу до момента времени.
func (l *Limiter) WaitChat(ctx context.Context, chatID int64) error {
	if err := l.global.Wait(ctx); err != nil {
		return err
	}
	l.mu.Lock()
	lim := l.chatLimiter(chatID)
	l.mu.Unlock()
	if err := lim.Wait(ctx); err != nil {
		return err
	}
	return l.waitPenalty(ctx, chatID)
}

// waitPenalty ждёт «запрет до» чата, если он активен.
func (l *Limiter) waitPenalty(ctx context.Context, chatID int64) error {
	l.mu.Lock()
	until, ok := l.penalty[chatID]
	if !ok {
		l.mu.Unlock()
		return nil
	}
	l.mu.Unlock()
	wait := until.Sub(l.nowFn())
	if wait <= 0 {
		l.mu.Lock()
		delete(l.penalty, chatID)
		l.mu.Unlock()
		return nil
	}
	return l.sleepFn(ctx, wait)
}

// Penalize запрещает выдачу токенов чату до момента until (429 retry_after,
// спека §7.4). Значение фиксируется в penalty-карте и проверяется в тестах
// без реального сна.
func (l *Limiter) Penalize(chatID int64, until time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if cur, ok := l.penalty[chatID]; !ok || until.After(cur) {
		l.penalty[chatID] = until
	}
}

// PenaltyUntil возвращает текущий «запрет до» чата (zero = нет запрета).
// Тестовое интроспективное API.
func (l *Limiter) PenaltyUntil(chatID int64) time.Time {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.penalty[chatID]
}
