package telegram

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/sauron/deadliner/internal/domain"
	"github.com/sauron/deadliner/internal/i18n"
	"github.com/sauron/deadliner/internal/platform/scheduler"
)

// Sender — минимальная поверхность Telegram Bot API, нужная нотификатору.
// chatID: для чата группы — -100…, для ЛС — user telegram_id. linkPreviewOff
// отключает превью ссылок. Возвращает message_id отправленного сообщения.
type Sender interface {
	SendMessage(ctx context.Context, chatID int64, threadID *int64, text string, linkPreviewOff bool) (messageID int64, err error)
}

// maxInternalRetryAfter: retry_after до 60с нотификатор честно отрабатывает
// сам (блокируя горутину; батч ограничен), дольше — отдаёт ошибку воркеру.
const maxInternalRetryAfter = 60 * time.Second

// Notifier реализует domain.Notifier: глобальный (25/с) и per-chat (18/мин)
// лимиты через scheduler.Limiter, внутренняя выдержка 429 retry_after.
//
// Если нижележащий sender реализует MessageSender (BotSender это делает),
// сообщения уходят с inline-кнопкой web_app (спека §6.2); иначе — обычным
// SendMessage: нотификатор остаётся работоспособен на минимальном Sender.
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
	_, err := n.SendToChatID(ctx, chatID, threadID, text)
	return err
}

// SendToChatID — как SendToChat, но возвращает message_id опубликованного
// сообщения (claim-флоу пишет его в claim_codes.message_id).
func (n *Notifier) SendToChatID(ctx context.Context, chatID, threadID int64, text string) (int64, error) {
	var tid *int64
	if threadID != 0 {
		tid = &threadID
	}
	return n.send(ctx, chatID, tid, text)
}

// SendToUser — сообщение в ЛС (chat_id = telegram_id пользователя).
func (n *Notifier) SendToUser(ctx context.Context, userID int64, text string) error {
	_, err := n.send(ctx, userID, nil, text)
	return err
}

// send: лимит → SendMessage → 429: выдержка до 60с и повтор ОДИН раз, дольше
// (или повторный 429) — domain.RateLimitError воркеру; 403-blocked —
// domain.BotBlockedError.
func (n *Notifier) send(ctx context.Context, chatID int64, threadID *int64, text string) (int64, error) {
	return n.dispatch(ctx, chatID, func(ctx context.Context) (int64, error) {
		return n.sender.SendMessage(ctx, chatID, threadID, text, true)
	})
}

// dispatch выполняет одну отправку под общим лимитером с обработкой 429/403.
// op вызывается от одного до двух раз (внутренний повтор после короткого
// retry_after).
func (n *Notifier) dispatch(ctx context.Context, chatID int64, op func(context.Context) (int64, error)) (int64, error) {
	if err := n.lim.WaitChat(ctx, chatID); err != nil {
		return 0, fmt.Errorf("telegram: wait limiter: %w", err)
	}
	id, err := op(ctx)
	if err == nil {
		return id, nil
	}

	var rl *domain.RateLimitError
	if errors.As(err, &rl) && rl.RetryAfter > 0 {
		if rl.RetryAfter <= maxInternalRetryAfter {
			n.lim.Penalize(chatID, time.Now().Add(rl.RetryAfter))
			if werr := n.lim.WaitChat(ctx, chatID); werr != nil {
				return 0, fmt.Errorf("telegram: wait retry_after: %w", werr)
			}
			if id2, err2 := op(ctx); err2 == nil {
				return id2, nil
			} else if errors.As(err2, &rl) {
				return 0, &domain.RateLimitError{RetryAfter: rl.RetryAfter}
			} else if isBlocked(err2) {
				return 0, blockedUser(chatID)
			} else {
				return 0, err2
			}
		}
		return 0, &domain.RateLimitError{RetryAfter: rl.RetryAfter}
	}
	if isBlocked(err) {
		return 0, blockedUser(chatID)
	}
	return 0, err
}

// isBlocked — ошибка отправки из-за блокировки бота пользователем.
func isBlocked(err error) bool {
	var bb *domain.BotBlockedError
	return errors.As(err, &bb) || errors.Is(err, domain.ErrBotBlocked)
}

func blockedUser(id int64) error {
	return &domain.BotBlockedError{UserID: id}
}

// SendToChatWithButton — сообщение с inline-кнопкой (спека §6.2: «Открыть в
// Deadliner»). Если транспорт реализует MessageSender, кнопка уходит; иначе
// деградирует до обычного SendMessage — нотификатору не нужен более богатый
// контракт, чем Sender.
func (n *Notifier) SendToChatWithButton(ctx context.Context, chatID, threadID int64, text, buttonText, buttonURL string) (int64, error) {
	ms, ok := n.sender.(MessageSender)
	if !ok || buttonURL == "" {
		return n.SendToChatID(ctx, chatID, threadID, text)
	}
	var tid *int64
	if threadID != 0 {
		tid = &threadID
	}
	msg := OutMessage{
		ChatID: chatID, ThreadID: tid, Text: text,
		LinkPreviewOff: true, ButtonText: buttonText, ButtonURL: buttonURL,
	}
	// chat_id < 0 — группа: BotSender сам выберет url-кнопку вместо web_app
	// (Telegram отклоняет web_app в группах).
	return n.dispatch(ctx, chatID, func(ctx context.Context) (int64, error) {
		return ms.Send(ctx, msg)
	})
}

// InvitePublisher — публикация инвайта в чат группы: призыв «Присоединяйтесь»
// и Main App-кнопка (startapp = plaintext-код инвайта). Реализует интерфейс
// httpapi.InvitePublisher.
type InvitePublisher struct {
	notifier *Notifier
	appURL   string
}

func NewInvitePublisher(notifier *Notifier, appURL string) *InvitePublisher {
	return &InvitePublisher{notifier: notifier, appURL: appURL}
}

// PublishInvite отправляет инвайт-сообщение в чат группы. Кнопка открывает
// Mini App как Main App: startapp-параметр несёт plaintext-код инвайта
// (ссылок во внешний браузер нет — приложение открывается нативно в Telegram).
func (p *InvitePublisher) PublishInvite(ctx context.Context, chatID, threadID int64, text string) error {
	_, err := p.notifier.SendToChatWithButton(ctx, chatID, threadID, text,
		i18n.T("bot.button.join_group"), p.appURL)
	return err
}
