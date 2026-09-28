package telegram

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sauron/deadliner/internal/domain"
	"github.com/sauron/deadliner/internal/platform/scheduler"
)

// fakeSender — записывает SendMessage-вызовы, возвращает запрограммированные
// ошибки по номеру вызова.
type fakeSender struct {
	calls []sendCall
	errs  []error // errs[i] — ошибка i-го вызова (nil — успех)
}

type sendCall struct {
	chatID     int64
	threadID   *int64
	text       string
	previewOff bool
}

func (s *fakeSender) SendMessage(ctx context.Context, chatID int64, threadID *int64, text string, linkPreviewOff bool) (int64, error) {
	i := len(s.calls)
	s.calls = append(s.calls, sendCall{chatID: chatID, threadID: threadID, text: text, previewOff: linkPreviewOff})
	if i < len(s.errs) {
		return 0, s.errs[i]
	}
	return int64(i + 1), nil
}

// Обычная отправка: лимиты не мешают (burst), link preview выключен.
func TestNotifierSendSuccess(t *testing.T) {
	sender := &fakeSender{}
	n := New(sender, 1000, 1000) // burst покрывает тест
	ctx := context.Background()

	if err := n.SendToChat(ctx, -100500, 0, "hi"); err != nil {
		t.Fatalf("SendToChat: %v", err)
	}
	if err := n.SendToUser(ctx, 42, "dm"); err != nil {
		t.Fatalf("SendToUser: %v", err)
	}
	if len(sender.calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(sender.calls))
	}
	if sender.calls[0].chatID != -100500 || sender.calls[0].threadID != nil {
		t.Errorf("chat call = %+v", sender.calls[0])
	}
	if sender.calls[1].chatID != 42 {
		t.Errorf("user call = %+v", sender.calls[1])
	}
	if !sender.calls[0].previewOff {
		t.Errorf("link preview not disabled")
	}
}

// 429 с retry_after ≤ 60с: нотификатор повторяет один раз, успех.
func TestNotifier429RetriesInternally(t *testing.T) {
	sender := &fakeSender{errs: []error{
		&domain.RateLimitError{RetryAfter: time.Second},
	}}
	n := New(sender, 1000, 1000)
	// Загоняем лимитеры в «нулевое ожидание»: заменяем сон фейшком.
	// Лимитер с penalty 1с: WaitChat подождёт — подменяем sleep.
	setFakeSleep(t, n)
	ctx := context.Background()

	if err := n.SendToChat(ctx, -1, 0, "hi"); err != nil {
		t.Fatalf("SendToChat after internal retry: %v", err)
	}
	if len(sender.calls) != 2 {
		t.Fatalf("calls = %d, want 2 (retry)", len(sender.calls))
	}
}

// 429, повтор тоже 429 → domain.RateLimitError наружу.
func TestNotifier429SecondFailureSurfaces(t *testing.T) {
	sender := &fakeSender{errs: []error{
		&domain.RateLimitError{RetryAfter: time.Second},
		&domain.RateLimitError{RetryAfter: 9 * time.Second},
	}}
	n := New(sender, 1000, 1000)
	setFakeSleep(t, n)
	ctx := context.Background()

	err := n.SendToChat(ctx, -1, 0, "hi")
	var rl *domain.RateLimitError
	if !errors.As(err, &rl) || rl.RetryAfter != 9*time.Second {
		t.Fatalf("err = %v, want RateLimitError(9s)", err)
	}
}

// 429 с retry_after > 60с — сразу наружу, без повтора.
func TestNotifier429LongRetryAfterSurfaces(t *testing.T) {
	sender := &fakeSender{errs: []error{
		&domain.RateLimitError{RetryAfter: 5 * time.Minute},
	}}
	n := New(sender, 1000, 1000)
	setFakeSleep(t, n)
	ctx := context.Background()

	err := n.SendToUser(ctx, 7, "hi")
	var rl *domain.RateLimitError
	if !errors.As(err, &rl) || rl.RetryAfter != 5*time.Minute {
		t.Fatalf("err = %v, want RateLimitError(5m)", err)
	}
	if len(sender.calls) != 1 {
		t.Errorf("calls = %d, want 1 (no retry)", len(sender.calls))
	}
}

// 403-блокировка → domain.BotBlockedError.
func TestNotifierBlockedError(t *testing.T) {
	sender := &fakeSender{errs: []error{&domain.BotBlockedError{UserID: 55}}}
	n := New(sender, 1000, 1000)
	setFakeSleep(t, n)
	ctx := context.Background()

	err := n.SendToUser(ctx, 55, "hi")
	var bb *domain.BotBlockedError
	if !errors.As(err, &bb) || bb.UserID != 55 {
		t.Fatalf("err = %v, want BotBlockedError{55}", err)
	}
	if !errors.Is(err, domain.ErrBotBlocked) {
		t.Errorf("err does not unwrap to ErrBotBlocked")
	}
}

// Обычная ошибка проксируется как есть.
func TestNotifierOtherErrorPassthrough(t *testing.T) {
	boom := errors.New("boom")
	sender := &fakeSender{errs: []error{boom}}
	n := New(sender, 1000, 1000)
	setFakeSleep(t, n)
	ctx := context.Background()

	if err := n.SendToUser(ctx, 1, "hi"); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
}

// setFakeSleep подменяет сон лимитера, чтобы тест не ждал пенальти.
func setFakeSleep(t *testing.T, n *Notifier) {
	t.Helper()
	n.lim = scheduler.NewLimiter(1000, 1000).WithClockAndSleep(
		time.Now, func(ctx context.Context, d time.Duration) error { return nil })
}
