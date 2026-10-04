// Package scheduler — воркер напоминаний (спека §7.2): цикл опроса,
// FetchDue с SKIP LOCKED, отправка через domain.Notifier, backoff при сбоях,
// fan-out дублей в ЛС (§7.3). Время/батчи/TTL — из конфига.
package scheduler

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/sauron/deadliner/internal/domain"
	"github.com/sauron/deadliner/internal/platform/repo"
)

// Config — параметры цикла из env (спека §8).
type Config struct {
	PollInterval time.Duration
	Batch        int
	LockTTL      time.Duration
	MaxAttempts  int
	WorkerID     string
	// Concurrency — семафор горутин на батч (по умолчанию 8).
	Concurrency int
	// DMNotifyBatch — максимум dm_dup-детей на один родительский success
	// (страховка от гигантских групп; 0 = без лимита).
	DMNotifyBatch int
	// FinalizeTimeout — бюджет на доведение уже начатой работы после отмены
	// ctx (см. process): фиксация sent/failed не должна теряться, иначе
	// перезапуск переотправит напоминание. Ноль (и меньше) → DefaultFinalizeTimeout.
	FinalizeTimeout time.Duration
}

// DefaultFinalizeTimeout — бюджет финализации по умолчанию: 75s покрывает
// внутреннюю выдержку 429 retry_after нотификатора (до 60с) плюс отправку и
// UPDATE, иначе graceful shutdown оставил бы сообщение с локом (дубль после
// рестарта). Экспортирован, чтобы serve не дублировал значение.
const DefaultFinalizeTimeout = 75 * time.Second

// Deps — зависимости воркера (только domain-порты + пул для транзакций).
type Deps struct {
	Pool        repo.TxBeginner
	Reminders   domain.ReminderRepo
	Deadlines   domain.DeadlineRepo
	Memberships domain.MembershipRepo
	Groups      domain.GroupRepo
	Bindings    domain.ChatBindingRepo
	Users       domain.UserRepo
	Notifier    domain.Notifier
	Clock       domain.Clock
	Log         *slog.Logger
}

// backoff — расписание ретраев (спека §7.2): 30s, 2m, 10m, 30m, 1h.
var backoff = []time.Duration{
	30 * time.Second,
	2 * time.Minute,
	10 * time.Minute,
	30 * time.Minute,
	time.Hour,
}

const (
	noBindingRetry = time.Hour          // нет привязки чата — дешёвый ретрай
	blockedRetry   = 7 * 24 * time.Hour // 403: карантин на неделю
)

type Worker struct {
	cfg  Config
	deps Deps
	log  *slog.Logger
	sem  chan struct{}
}

func New(deps Deps, cfg Config) *Worker {
	if cfg.Batch <= 0 {
		cfg.Batch = 50
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 5
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 8
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 10 * time.Second
	}
	if cfg.LockTTL <= 0 {
		cfg.LockTTL = 2 * time.Minute
	}
	if cfg.FinalizeTimeout <= 0 {
		cfg.FinalizeTimeout = DefaultFinalizeTimeout
	}
	log := deps.Log
	if log == nil {
		log = slog.Default()
	}
	return &Worker{
		cfg:  cfg,
		deps: deps,
		log:  log,
		sem:  make(chan struct{}, cfg.Concurrency),
	}
}

// FinalizeTimeout — фактический бюджет финализации воркера (после
// подстановки дефолта); для тестов сборки графа.
func (w *Worker) FinalizeTimeout() time.Duration { return w.cfg.FinalizeTimeout }

// Run — главный цикл (спека §7.2): джиттер 0–500мс, ReleaseStale → FetchDue
// → батч параллельно (семафор) → MarkSent|MarkFailed. ctx.Done: ждём
// in-flight горутины (drain) и выходим.
func (w *Worker) Run(ctx context.Context) error {
	w.log.Info("scheduler: worker started",
		slog.String("worker_id", w.cfg.WorkerID),
		slog.Int("batch", w.cfg.Batch))
	// Первый тик сразу, дальше — PollInterval (тикер после джиттера).
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			w.log.Info("scheduler: worker stopped", slog.String("worker_id", w.cfg.WorkerID))
			return nil
		case <-timer.C:
		}

		if err := w.Tick(ctx); err != nil && ctx.Err() == nil {
			w.log.Error("scheduler: tick failed", slog.String("error", err.Error()))
		}

		// Джиттер 0..500мс + интервал.
		jitter := time.Duration(rand.Int64N(500 * int64(time.Millisecond)))
		timer.Reset(w.cfg.PollInterval + jitter)
	}
}

// Tick — один цикл воркера (публичный: Run зовёт его в цикле, тесты — напрямую);
// дожидается завершения батча.
//
// ИНВАРИАНТ МАСШТАБИРОВАНИЯ (runbook §2.1): между ReleaseStale и MarkSent
// напоминание защищено только локом в БД и условным UPDATE. При нескольких
// инстансах serve второй инстанс с now-LockTTL больше времени дренажа батча
// освободит живой лок и переотправит напоминание. Требование к конфигурации:
// SCHED_LOCK_TTL > худшего времени дренажа батча; штатный деплой — один
// инстанс serve.
func (w *Worker) Tick(ctx context.Context) error {
	now := w.deps.Clock.Now()

	// 1. ReleaseStale (§7.2 п.1): протухшие локи → pending.
	released, err := w.deps.Reminders.ReleaseStale(ctx, now.Add(-w.cfg.LockTTL))
	if err != nil {
		return err
	}
	if released > 0 {
		w.log.Info("scheduler: released stale locks", slog.Int64("count", released))
	}

	// 2. FetchDue в транзакции (SKIP LOCKED).
	tx, rollback, err := repo.BeginDomainTx(ctx, w.deps.Pool)
	if err != nil {
		return err
	}
	batch, err := w.deps.Reminders.FetchDue(ctx, tx, now, w.cfg.Batch, w.cfg.WorkerID)
	if err != nil {
		rollback()
		return err
	}
	// Локи записаны UPDATE'ом в той же транзакции — коммит фиксирует их.
	if err := repo.CommitDomainTx(ctx, tx); err != nil {
		rollback()
		return err
	}
	if len(batch) == 0 {
		return nil
	}

	// 3. Батч параллельно, ограниченно.
	var wg sync.WaitGroup
	wg.Add(len(batch))
	for i := range batch {
		rem := batch[i]
		w.sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-w.sem }()
			w.process(ctx, rem)
		}()
	}
	// Тик ждёт батч: 10-секундный интервал цикла доминирует, а завершённый
	// тик упрощает graceful shutdown (Run ждёт только текущий тик).
	wg.Wait()
	return nil
}

// process — один reminder: сообщение → MarkSent (+fan-out) | MarkFailed.
// ctx отвязывается от отмены: отмена — сигнал Run'у не брать новые батчи,
// начатая отправка и её фиксация доводятся под дедлайном FinalizeTimeout
// (иначе ReleaseStale вернул бы строку в очередь — дубль после рестарта).
func (w *Worker) process(ctx context.Context, rem domain.Reminder) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), w.cfg.FinalizeTimeout)
	defer cancel()

	now := w.deps.Clock.Now()

	dl, err := w.deps.Deadlines.GetByID(ctx, rem.DeadlineID)
	if errors.Is(err, domain.ErrNotFound) {
		// Дедлайн удалён/завершён — reminder потребляем молча: send не делаем,
		// строка не ретраится.
		w.log.Info("scheduler: skipped: deadline deleted",
			slog.Int64("reminder_id", rem.ID), slog.Int64("deadline_id", rem.DeadlineID))
		if _, err := w.deps.Reminders.MarkSent(ctx, rem.ID, w.cfg.WorkerID, now); err != nil {
			w.log.Error("scheduler: MarkSent after skip failed", slog.String("error", err.Error()))
		}
		return
	}
	if err != nil {
		// Транзиентная ошибка БД — не потребляем строку, обычный backoff.
		w.fail(ctx, rem, "deadline load: "+err.Error(), now.Add(w.nextBackoff(rem)))
		return
	}
	if dl.Status != domain.DeadlineStatusActive {
		// done/archived — потребляем молча.
		w.log.Info("scheduler: skipped: deadline not active",
			slog.String("status", string(dl.Status)),
			slog.Int64("reminder_id", rem.ID), slog.Int64("deadline_id", rem.DeadlineID))
		if _, err := w.deps.Reminders.MarkSent(ctx, rem.ID, w.cfg.WorkerID, now); err != nil {
			w.log.Error("scheduler: MarkSent after skip failed", slog.String("error", err.Error()))
		}
		return
	}

	switch {
	case rem.Kind == domain.KindDMDup:
		w.processDMDup(ctx, rem, *dl)
	case rem.Kind == domain.KindPreset || rem.Kind == domain.KindCustomOffset || rem.Kind == domain.KindCustomAt:
		if dl.GroupID != nil {
			w.processGroup(ctx, rem, *dl)
		} else if dl.OwnerUserID != nil {
			w.processPersonal(ctx, rem, *dl)
		} else {
			w.fail(ctx, rem, "deadline has neither group nor owner", now)
		}
	default:
		w.fail(ctx, rem, "unknown reminder kind: "+string(rem.Kind), now)
	}
}

// processGroup — напоминание в чат группы + fan-out dm_dup (§7.3).
func (w *Worker) processGroup(ctx context.Context, rem domain.Reminder, dl domain.Deadline) {
	now := w.deps.Clock.Now()

	binding, err := w.deps.Bindings.GetByGroup(ctx, *dl.GroupID)
	if errors.Is(err, domain.ErrNotFound) {
		w.log.Error("scheduler: no chat binding", slog.Int64("group_id", *dl.GroupID))
		w.fail(ctx, rem, "no chat binding", now.Add(noBindingRetry))
		return
	}
	if err != nil {
		// Не «привязки нет», а сбой БД: обычный backoff.
		w.fail(ctx, rem, "binding load: "+err.Error(), now.Add(w.nextBackoff(rem)))
		return
	}

	group, err := w.deps.Groups.GetByID(ctx, *dl.GroupID)
	if err != nil {
		w.fail(ctx, rem, "group not found", now.Add(noBindingRetry))
		return
	}

	var threadID int64
	if binding.MessageThreadID != nil {
		threadID = *binding.MessageThreadID
	}
	text := groupMessage(rem, dl, group.Slug)
	if err := w.deps.Notifier.SendToChat(ctx, binding.ChatID, threadID, text); err != nil {
		// Чат группы: 403 здесь не означает «пользователь заблокировал бота»,
		// поэтому dm-цель не передаём.
		w.handleSendError(ctx, rem, err, nil)
		return
	}

	// Fan-out (§7.3): те же цели, что и у родителя — в одной транзакции с
	// MarkSent, дети kind='dm_dup', fire_at=now.
	targets, err := w.deps.Memberships.ListDMTargets(ctx, *dl.GroupID)
	if err != nil {
		w.log.Error("scheduler: ListDMTargets failed", slog.String("error", err.Error()))
		// Чат отправлен — родитель должен стать sent: MarkSent без fan-out,
		// ошибка детей не отменяет родителя (§7.3).
		w.finalizeWithoutFanout(ctx, rem.ID, now, "list-dm-targets-failed")
		return
	}
	if w.cfg.DMNotifyBatch > 0 && len(targets) > w.cfg.DMNotifyBatch {
		targets = targets[:w.cfg.DMNotifyBatch]
	}
	children := make([]domain.Reminder, len(targets))
	for i, uid := range targets {
		u := uid
		children[i] = domain.Reminder{
			DeadlineID:   rem.DeadlineID,
			Kind:         domain.KindDMDup,
			TargetUserID: &u,
			FireAt:       now,
			Status:       domain.ReminderStatusPending,
		}
	}

	tx, rollback, err := repo.BeginDomainTx(ctx, w.deps.Pool)
	if err != nil {
		w.log.Error("scheduler: fanout tx begin failed", slog.String("error", err.Error()))
		w.finalizeWithoutFanout(ctx, rem.ID, now, "fanout-tx-begin-failed")
		return
	}
	ok, err := w.deps.Reminders.MarkSentWithFanout(ctx, tx, rem.ID, w.cfg.WorkerID, now, children)
	if err != nil {
		rollback()
		w.log.Error("scheduler: MarkSentWithFanout failed", slog.String("error", err.Error()))
		// Отправка в чат уже была: ретраить нельзя, фиксируем sent отдельным
		// MarkSent (только если строка ещё pending и locked_by=нас).
		w.finalizeWithoutFanout(ctx, rem.ID, now, "mark-sent-with-fanout-failed")
		return
	}
	if err := repo.CommitDomainTx(ctx, tx); err != nil {
		rollback()
		w.log.Error("scheduler: fanout commit failed", slog.String("error", err.Error()))
		// После отката строка снова pending и locked_by=нас: фиксируем sent
		// плейн-MarkSent; дети потеряны, повторной отправки в чат нет.
		w.finalizeWithoutFanout(ctx, rem.ID, now, "fanout-commit-failed")
		return
	}
	if !ok {
		w.log.Info("scheduler: lost lock race, fan-out suppressed", slog.Int64("reminder_id", rem.ID))
	}
}

// finalizeWithoutFanout — сообщение в чат уже ушло, а fan-out не состоялся:
// помечаем родителя sent вне транзакции (MarkSent проходит только если строка
// ещё pending и locked_by=нас). Дочерние dm_dup теряются: приоритет —
// отсутствие дубля в чате.
func (w *Worker) finalizeWithoutFanout(ctx context.Context, remID int64, now time.Time, reason string) {
	if _, err := w.deps.Reminders.MarkSent(ctx, remID, w.cfg.WorkerID, now); err != nil {
		w.log.Error("scheduler: MarkSent fallback failed",
			slog.String("reason", reason), slog.String("error", err.Error()))
	}
}

// processPersonal — ЛС владельцу персонального дедлайна.
func (w *Worker) processPersonal(ctx context.Context, rem domain.Reminder, dl domain.Deadline) {
	now := w.deps.Clock.Now()
	user, err := w.deps.Users.GetByID(ctx, *dl.OwnerUserID)
	if err != nil {
		w.fail(ctx, rem, "owner not found", now.Add(noBindingRetry))
		return
	}
	if user.BotBlocked || user.IsBanned {
		w.quarantine(ctx, rem, "user blocked bot", now)
		return
	}
	text := personalMessage(rem, dl)
	if err := w.deps.Notifier.SendToUser(ctx, user.TelegramID, text); err != nil {
		w.handleSendError(ctx, rem, err, &user.TelegramID)
		return
	}
	if _, err := w.deps.Reminders.MarkSent(ctx, rem.ID, w.cfg.WorkerID, now); err != nil {
		w.log.Error("scheduler: MarkSent failed", slog.String("error", err.Error()))
	}
}

// processDMDup — дубли в ЛС (ребёнок fan-out).
func (w *Worker) processDMDup(ctx context.Context, rem domain.Reminder, dl domain.Deadline) {
	now := w.deps.Clock.Now()
	if rem.TargetUserID == nil {
		w.fail(ctx, rem, "dm_dup without target_user_id", now)
		return
	}
	user, err := w.deps.Users.GetByID(ctx, *rem.TargetUserID)
	if err != nil {
		w.fail(ctx, rem, "target user not found", now.Add(noBindingRetry))
		return
	}
	if user.BotBlocked || user.IsBanned {
		w.quarantine(ctx, rem, "user blocked bot", now)
		return
	}
	group, err := w.deps.Groups.GetByID(ctx, *dl.GroupID)
	if err != nil {
		w.fail(ctx, rem, "group not found", now.Add(noBindingRetry))
		return
	}
	text := dmDupMessage(rem, dl, group.Slug)
	if err := w.deps.Notifier.SendToUser(ctx, user.TelegramID, text); err != nil {
		w.handleSendError(ctx, rem, err, &user.TelegramID)
		return
	}
	if _, err := w.deps.Reminders.MarkSent(ctx, rem.ID, w.cfg.WorkerID, now); err != nil {
		w.log.Error("scheduler: MarkSent failed", slog.String("error", err.Error()))
	}
}

// handleSendError — классификация ошибок отправки (§7.2/§7.3/§7.4).
// dmTarget — telegram_id получателя ЛС (nil для отправки в чат группы):
// только для ЛС 403 означает «пользователь заблокировал бота» и его можно
// фиксировать в users.bot_blocked.
func (w *Worker) handleSendError(ctx context.Context, rem domain.Reminder, err error, dmTarget *int64) {
	now := w.deps.Clock.Now()
	var rl *domain.RateLimitError
	if errors.As(err, &rl) && rl.RetryAfter > 0 {
		// 429: уважаем retry_after (§7.4), attempt не сгорает зря — MarkFailed
		// с retryAt=now+retry_after.
		w.fail(ctx, rem, "rate limited", now.Add(rl.RetryAfter))
		return
	}
	var bb *domain.BotBlockedError
	if errors.As(err, &bb) || errors.Is(err, domain.ErrBotBlocked) {
		// 403 от Telegram на ЛС: фиксируем флаг в users, чтобы следующий
		// fan-out (ListDMTargets: NOT u.bot_blocked) участника уже не выбирал.
		if dmTarget != nil {
			w.markBotBlocked(ctx, *dmTarget)
		}
		w.quarantine(ctx, rem, "user blocked bot", now)
		return
	}
	w.fail(ctx, rem, err.Error(), now.Add(w.nextBackoff(rem)))
}

// markBotBlocked — best-effort запись users.bot_blocked (спека §7.3): сбой
// записи не должен ломать обработку reminder, поэтому только лог.
func (w *Worker) markBotBlocked(ctx context.Context, telegramID int64) {
	if err := w.deps.Users.MarkBotBlocked(ctx, telegramID, true); err != nil {
		w.log.Error("scheduler: MarkBotBlocked failed",
			slog.Int64("telegram_id", telegramID), slog.String("error", err.Error()))
		return
	}
	w.log.Info("scheduler: user marked bot_blocked", slog.Int64("telegram_id", telegramID))
}

// nextBackoff — шаг ретрая по attempts (спека §7.2: 30s,2m,10m,30m,1h).
func (w *Worker) nextBackoff(rem domain.Reminder) time.Duration {
	if rem.Attempts >= len(backoff) {
		return backoff[len(backoff)-1]
	}
	return backoff[rem.Attempts]
}

// fail — MarkFailed с retryAt; failed=true (терминал) — лог ошибки.
func (w *Worker) fail(ctx context.Context, rem domain.Reminder, errText string, retryAt time.Time) {
	failed, err := w.deps.Reminders.MarkFailed(ctx, rem.ID, w.cfg.WorkerID, errText, retryAt, w.cfg.MaxAttempts)
	if err != nil {
		w.log.Error("scheduler: MarkFailed repo error", slog.String("error", err.Error()))
		return
	}
	if failed {
		w.log.Error("scheduler: reminder failed permanently",
			slog.Int64("reminder_id", rem.ID),
			slog.Int("attempts", rem.Attempts+1),
			slog.String("error", errText))
	}
}

// quarantine — BlockedError: MarkFailed с ретраем через неделю.
func (w *Worker) quarantine(ctx context.Context, rem domain.Reminder, errText string, now time.Time) {
	w.log.Info("scheduler: user blocked bot, quarantine reminder",
		slog.Int64("reminder_id", rem.ID), slog.String("error", errText))
	w.fail(ctx, rem, errText, now.Add(blockedRetry))
}
