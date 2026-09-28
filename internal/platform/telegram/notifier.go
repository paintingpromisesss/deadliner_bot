// Package telegram — адаптер Notifier поверх Telegram Bot API. Конкретный
// HTTP-клиент (go-telegram/bot) подключается задачей 10 через интерфейс
// Sender; этот пакет знает только лимиты, ретраи 429 и ошибки 403.
package telegram

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/sauron/deadliner/internal/domain"
	"github.com/sauron/deadliner/internal/platform/scheduler"
)

// Sender — минимальная поверхность Telegram Bot API, нужная нотификатору
// (задача 10 адаптирует к ней go-telegram/bot). chatID: для чата группы —
// -100…, для ЛС — user telegram_id. linkPreviewOff отключает превью ссылок.
type Sender interface {
	SendMessage(ctx context.Context, chatID int64, threadID *int64, text string, linkPreviewOff bool) (messageID int64, err error)
}

// maxInternalRetryAfter: retry_after до 60с нотификатор честно отрабатывает
// сам (блокируя горутину; батч ограничен), дольше — отдаёт ошибку воркеру.
const maxInternalRetryAfter = 60 * time.Second

// Notifier реализует domain.Notifier: глобальный (25/с) и per-chat (18/мин)
// лимиты через scheduler.Limiter, внутренняя выдержка 429 retry_after.
type Notifier struct {
	sender Sender
	lim    *scheduler.Limiter
	log    logger
}

type logger interface {
	Info(msg string, args ...any)
}

var _ domain.Notifier = (*Notifier)(nil)

func New(sender Sender, globalPerSec, perChatPerMin int) *Notifier {
	return &Notifier{
		sender: sender,
		lim:    scheduler.NewLimiter(globalPerSec, perChatPerMin),
	}
}

// WithLogger добавляет slog-логгер (опционально).
func (n *Notifier) WithLogger(log logger) *Notifier {
	n.log = log
	return n
}

// SendToChat — сообщение в чат группы (возможно, в топик threadID).
func (n *Notifier) SendToChat(ctx context.Context, chatID, threadID int64, text string) error {
	var tid *int64
	if threadID != 0 {
		tid = &threadID
	}
	return n.send(ctx, chatID, tid, text)
}

// SendToUser — сообщение в ЛС (chat_id = telegram_id пользователя).
func (n *Notifier) SendToUser(ctx context.Context, userID int64, text string) error {
	return n.send(ctx, userID, nil, text)
}

// send: лимит → SendMessage → 429: выдержка до 60с и повтор ОДИН раз, дольше
// (или повторный 429) — domain.RateLimitError воркеру; 403-blocked —
// domain.BotBlockedError.
func (n *Notifier) send(ctx context.Context, chatID int64, threadID *int64, text string) error {
	if err := n.lim.WaitChat(ctx, chatID); err != nil {
		return fmt.Errorf("telegram: wait limiter: %w", err)
	}
	_, err := n.sender.SendMessage(ctx, chatID, threadID, text, true)
	if err == nil {
		return nil
	}

	var rl *domain.RateLimitError
	if errors.As(err, &rl) && rl.RetryAfter > 0 {
		if rl.RetryAfter <= maxInternalRetryAfter {
			n.lim.Penalize(chatID, time.Now().Add(rl.RetryAfter))
			if werr := n.lim.WaitChat(ctx, chatID); werr != nil {
				return fmt.Errorf("telegram: wait retry_after: %w", werr)
			}
			if _, err2 := n.sender.SendMessage(ctx, chatID, threadID, text, true); err2 == nil {
				return nil
			} else if errors.As(err2, &rl) {
				return &domain.RateLimitError{RetryAfter: rl.RetryAfter}
			} else if isBlocked(err2) {
				return blockedUser(chatID)
			} else {
				return err2
			}
		}
		return &domain.RateLimitError{RetryAfter: rl.RetryAfter}
	}
	if isBlocked(err) {
		return blockedUser(chatID)
	}
	return err
}

// isBlocked — ошибка отправки из-за блокировки бота пользователем.
func isBlocked(err error) bool {
	var bb *domain.BotBlockedError
	return errors.As(err, &bb) || errors.Is(err, domain.ErrBotBlocked)
}

func blockedUser(id int64) error {
	return &domain.BotBlockedError{UserID: id}
}
