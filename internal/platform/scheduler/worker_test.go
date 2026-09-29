package scheduler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/sauron/deadliner/internal/domain"
	"github.com/sauron/deadliner/internal/i18n"
	"github.com/sauron/deadliner/internal/platform/db"
	"github.com/sauron/deadliner/internal/platform/repo"
)

// Интеграционные тесты воркера: реальные repo на testcontainers-пуле,
// фейковые Clock и Notifier. Тик воркера дергается напрямую (tick), цикл
// Run/Drain покрывается TestWorkerGracefulShutdown.

var schedPool *pgxpool.Pool

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
	schedPool = pool
	if err := i18n.Load(i18n.Locales); err != nil {
		fmt.Fprintf(os.Stderr, "i18n: %v\n", err)
		os.Exit(1)
	}
	code := m.Run()
	pool.Close()
	_ = ctr.Terminate(context.Background())
	os.Exit(code)
}

// truncate — чистые таблицы для каждого теста.
func truncate(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := schedPool.Exec(ctx, `TRUNCATE
		users, groups, chat_bindings, group_memberships, deadlines, reminders,
		invites, claim_codes, user_action_counters, sessions, outbox_messages,
		audit_log CASCADE`)
	if err != nil {
		t.Fatalf("truncate: %v", err)
	}
	return schedPool
}

// fakeClock — управляемые часы.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

// fakeNotifier — записывает вызовы; errFn возвращает ошибку по вызову.
type fakeNotifier struct {
	mu    sync.Mutex
	calls []notifyCall
	errFn func(call notifyCall) error
}

type notifyCall struct {
	kind     string // "chat" | "user"
	chatID   int64
	threadID int64
	userID   int64
	text     string
}

func (n *fakeNotifier) SendToChat(ctx context.Context, chatID, threadID int64, text string) error {
	return n.record(notifyCall{kind: "chat", chatID: chatID, threadID: threadID, text: text})
}

func (n *fakeNotifier) SendToUser(ctx context.Context, userID int64, text string) error {
	return n.record(notifyCall{kind: "user", userID: userID, text: text})
}

func (n *fakeNotifier) record(c notifyCall) error {
	n.mu.Lock()
	n.calls = append(n.calls, c)
	fn := n.errFn
	n.mu.Unlock()
	if fn != nil {
		return fn(c)
	}
	return nil
}

func (n *fakeNotifier) Count() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.calls)
}

func (n *fakeNotifier) Calls() []notifyCall {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]notifyCall{}, n.calls...)
}

var discardLogger = slog.New(slog.NewTextHandler(io.Discard, nil))

// recordingUserRepo оборачивает реальный UserRepo и запоминает вызовы
// MarkBotBlocked (I-1: воркер обязан фиксировать 403 в users.bot_blocked).
type recordingUserRepo struct {
	domain.UserRepo
	mu      sync.Mutex
	blocked []blockedCall
	err     error // если задан — MarkBotBlocked возвращает его (best-effort путь)
}

type blockedCall struct {
	telegramID int64
	blocked    bool
}

func (r *recordingUserRepo) MarkBotBlocked(ctx context.Context, telegramID int64, blocked bool) error {
	r.mu.Lock()
	r.blocked = append(r.blocked, blockedCall{telegramID: telegramID, blocked: blocked})
	err := r.err
	r.mu.Unlock()
	if err != nil {
		return err
	}
	return r.UserRepo.MarkBotBlocked(ctx, telegramID, blocked)
}

func (r *recordingUserRepo) MarkedBlocked() []blockedCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]blockedCall{}, r.blocked...)
}

// failingCommitTx — pgx.Tx, чей Commit падает на заданном по счёту коммите
// (I-4: сбой коммита fan-out не должен приводить к повторной отправке в чат).
// Тик делает два коммита: FetchDue (должен пройти) и fan-out (ломаем второй).
type failingCommitTx struct {
	pgx.Tx
	state *commitState
}

func (f *failingCommitTx) Commit(ctx context.Context) error {
	f.state.mu.Lock()
	f.state.commits++
	fail := f.state.failAt > 0 && f.state.commits == f.state.failAt
	f.state.mu.Unlock()
	if fail {
		return errors.New("commit boom")
	}
	return f.Tx.Commit(ctx)
}

// commitState — общий счётчик коммитов пула.
type commitState struct {
	mu      sync.Mutex
	commits int
	failAt  int // 0 = не ломать
}

// failingCommitPool — Begin отдаёт транзакции со счётчиком коммитов.
type failingCommitPool struct {
	repo.TxBeginner
	state commitState
}

// FailCommitAt — ломать коммит с указанным номером (1-based; 0 — выключить).
func (p *failingCommitPool) FailCommitAt(n int) {
	p.state.mu.Lock()
	p.state.failAt = n
	p.state.commits = 0
	p.state.mu.Unlock()
}

func (p *failingCommitPool) Begin(ctx context.Context) (pgx.Tx, error) {
	tx, err := p.TxBeginner.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return &failingCommitTx{Tx: tx, state: &p.state}, nil
}

type testEnv struct {
	worker   *Worker
	clock    *fakeClock
	notifier *fakeNotifier
	pool     *pgxpool.Pool
	users    *recordingUserRepo
}

func newEnv(t *testing.T) *testEnv {
	t.Helper()
	return newEnvOn(t, truncate(t))
}

// newEnvOn — воркер поверх уже подготовленной БД (без truncate): нужен для
// тестов с несколькими воркерами на одном наборе данных.
func newEnvOn(t *testing.T, pool *pgxpool.Pool) *testEnv {
	t.Helper()
	return newEnvWith(t, pool, nil)
}

// newEnvWith — воркер с опциональной подменой Deps/Config (сбойный commit,
// другой WorkerID и т.п.).
func newEnvWith(t *testing.T, pool *pgxpool.Pool, tweak func(*Deps, *Config)) *testEnv {
	t.Helper()
	clock := &fakeClock{now: time.Now().UTC().Truncate(time.Microsecond)}
	notifier := &fakeNotifier{}
	users := &recordingUserRepo{UserRepo: repo.NewUsers(pool)}
	deps := Deps{
		Pool:        pool,
		Reminders:   repo.NewReminders(pool),
		Deadlines:   repo.NewDeadlines(pool),
		Memberships: repo.NewMemberships(pool),
		Groups:      repo.NewGroups(pool),
		Bindings:    repo.NewBindings(pool),
		Users:       users,
		Notifier:    notifier,
		Clock:       clock,
		Log:         discardLogger,
	}
	cfg := Config{
		PollInterval: time.Hour, // тики вручную
		Batch:        50,
		LockTTL:      2 * time.Minute,
		MaxAttempts:  5,
		WorkerID:     "w-test",
	}
	if tweak != nil {
		tweak(&deps, &cfg)
	}
	w := New(deps, cfg)
	return &testEnv{worker: w, clock: clock, notifier: notifier, pool: pool, users: users}
}

// fixture-хелперы --------------------------------------------------------

func insertUser(t *testing.T, pool *pgxpool.Pool, telegramID int64) int64 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var id int64
	err := pool.QueryRow(ctx,
		`INSERT INTO users (telegram_id, username, first_name)
		 VALUES ($1, $2, 'User') RETURNING id`,
		telegramID, fmt.Sprintf("user_%d", telegramID)).Scan(&id)
	if err != nil {
		t.Fatalf("insert user %d: %v", telegramID, err)
	}
	return id
}

// personalReminderFixture — активный личный дедлайн + один due reminder.
func personalReminderFixture(t *testing.T, env *testEnv) (reminderID int64) {
	t.Helper()
	ctx := context.Background()
	uid := insertUser(t, env.pool, 5001)
	dl := &domain.Deadline{
		OwnerUserID: &uid, Title: "Курсовая", DueAt: env.clock.Now().Add(24 * time.Hour),
		TZ: "Europe/Moscow", CreatedBy: uid, Status: domain.DeadlineStatusActive,
	}
	mins := 24 * 60
	reminders := []domain.Reminder{{
		Kind: domain.KindPreset, OffsetMinutes: &mins,
		FireAt: env.clock.Now().Add(-time.Minute), Status: domain.ReminderStatusPending,
	}}
	if err := repo.NewDeadlines(env.pool).Create(ctx, dl, reminders); err != nil {
		t.Fatal(err)
	}
	return reminders[0].ID
}

// groupFixture — группа + биндинг + групповой дедлайн + due reminder.
// members: (dmOverride *bool, defaultON bool, botBlocked bool) на пользователя.
type groupFixtureResult struct {
	groupID     int64
	deadlineID  int64
	reminderIDs []int64
	memberIDs   []int64 // telegram-independent: users.id, в порядке members
}

// groupFixture — группа + биндинг + групповой дедлайн с due reminder'ами.
// Слаг/чат фиксированы; пресеты задаются offsets (по умолчанию 24ч).
func groupFixture(t *testing.T, env *testEnv, members []memberSpec, offsets ...time.Duration) groupFixtureResult {
	t.Helper()
	ctx := context.Background()
	if len(offsets) == 0 {
		offsets = []time.Duration{24 * time.Hour}
	}
	owner := insertUser(t, env.pool, 5100)
	g := &domain.Group{
		Slug: "М8О-901-23", Title: "Группа", Status: domain.GroupStatusActive,
		CreatedBy: owner, DefaultPresets: offsets,
	}
	if err := repo.NewGroups(env.pool).Create(ctx, g); err != nil {
		t.Fatal(err)
	}
	if err := repo.NewBindings(env.pool).Create(ctx, &domain.ChatBinding{
		GroupID: g.ID, ChatID: -100900, BoundBy: owner,
	}); err != nil {
		t.Fatal(err)
	}
	dl := &domain.Deadline{
		GroupID: &g.ID, Title: "Курсовая", DueAt: env.clock.Now().Add(24 * time.Hour),
		TZ: "Europe/Moscow", CreatedBy: owner, Status: domain.DeadlineStatusActive,
	}
	reminders := make([]domain.Reminder, 0, len(offsets))
	for i, off := range offsets {
		mins := int(off / time.Minute)
		reminders = append(reminders, domain.Reminder{
			Kind: domain.KindPreset, OffsetMinutes: &mins,
			// Разный fire_at на пресет: unique (deadline_id, fire_at, kind)
			// запрещает два напоминания с одинаковым временем, да и воркер
			// обрабатывает пресеты независимо.
			FireAt: env.clock.Now().Add(-time.Duration(i+1) * time.Minute),
			Status: domain.ReminderStatusPending,
		})
	}
	if err := repo.NewDeadlines(env.pool).Create(ctx, dl, reminders); err != nil {
		t.Fatal(err)
	}
	reminderIDs := make([]int64, 0, len(reminders))
	for _, r := range reminders {
		reminderIDs = append(reminderIDs, r.ID)
	}
	memRepo := repo.NewMemberships(env.pool)
	memberIDs := make([]int64, 0, len(members))
	for i, spec := range members {
		uid := insertUser(t, env.pool, 5110+int64(i))
		if spec.defaultOn {
			if _, err := env.pool.Exec(ctx,
				`UPDATE users SET dm_notify_default=true WHERE id=$1`, uid); err != nil {
				t.Fatal(err)
			}
		}
		if spec.botBlocked {
			if _, err := env.pool.Exec(ctx,
				`UPDATE users SET bot_blocked=true WHERE id=$1`, uid); err != nil {
				t.Fatal(err)
			}
		}
		if err := memRepo.Upsert(ctx, &domain.Membership{
			GroupID: g.ID, UserID: uid, Role: domain.RoleMember, DMNotify: spec.dmOverride,
		}); err != nil {
			t.Fatal(err)
		}
		memberIDs = append(memberIDs, uid)
	}
	return groupFixtureResult{groupID: g.ID, deadlineID: dl.ID, reminderIDs: reminderIDs, memberIDs: memberIDs}
}

type memberSpec struct {
	dmOverride *bool
	defaultOn  bool
	botBlocked bool
}

func boolPtr(b bool) *bool { return &b }

// rowState — снимок строки reminder для assertions.
func rowState(t *testing.T, pool *pgxpool.Pool, id int64) (status string, attempts int, fireAt time.Time, lastErr *string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := pool.QueryRow(ctx,
		`SELECT status, attempts, fire_at, last_error FROM reminders WHERE id=$1`, id).
		Scan(&status, &attempts, &fireAt, &lastErr)
	if err != nil {
		t.Fatalf("rowState: %v", err)
	}
	return
}

// Тесты ------------------------------------------------------------------

// due job → отправлен ровно один раз, MarkSent записан.
func TestWorkerSendsDueReminder(t *testing.T) {
	env := newEnv(t)
	ctx := context.Background()
	remID := personalReminderFixture(t, env)

	if err := env.worker.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if got := env.notifier.Count(); got != 1 {
		t.Fatalf("notifier calls = %d, want 1", got)
	}
	status, attempts, _, _ := rowState(t, env.pool, remID)
	if status != "sent" || attempts != 0 {
		t.Errorf("row = (%q, %d), want (sent, 0)", status, attempts)
	}
	// Повторный тик: строка sent, ничего не отправляется.
	if err := env.worker.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if got := env.notifier.Count(); got != 1 {
		t.Errorf("notifier calls after 2nd tick = %d, want 1", got)
	}
}

// Ошибка отправки → pending, fire_at=now+30s (backoff[0]), attempts=1.
func TestWorkerNotifierErrorBackoff(t *testing.T) {
	env := newEnv(t)
	ctx := context.Background()
	remID := personalReminderFixture(t, env)
	now := env.clock.Now()

	env.notifier.errFn = func(c notifyCall) error { return errors.New("boom") }
	if err := env.worker.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	status, attempts, fireAt, lastErr := rowState(t, env.pool, remID)
	if status != "pending" || attempts != 1 {
		t.Fatalf("row = (%q, %d), want (pending, 1)", status, attempts)
	}
	if !fireAt.Equal(now.Add(30 * time.Second)) {
		t.Errorf("fire_at = %v, want %v", fireAt, now.Add(30*time.Second))
	}
	if lastErr == nil || *lastErr != "boom" {
		t.Errorf("last_error = %v, want boom", lastErr)
	}

	// Вторая неудача → 2m.
	env.clock.Advance(31 * time.Second)
	env.notifier.errFn = func(c notifyCall) error { return errors.New("boom2") }
	if err := env.worker.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	status, attempts, fireAt, _ = rowState(t, env.pool, remID)
	if status != "pending" || attempts != 2 {
		t.Fatalf("row2 = (%q, %d), want (pending, 2)", status, attempts)
	}
	if !fireAt.Equal(now.Add(31 * time.Second).Add(2 * time.Minute)) {
		t.Errorf("fire_at2 = %v, want %v", fireAt, now.Add(31*time.Second).Add(2*time.Minute))
	}
}

// Пять неудач → status=failed (терминально).
func TestWorkerAttemptsExhausted(t *testing.T) {
	env := newEnv(t)
	ctx := context.Background()
	remID := personalReminderFixture(t, env)
	env.notifier.errFn = func(c notifyCall) error { return errors.New("boom") }

	now := env.clock.Now()
	for range 5 {
		if err := env.worker.Tick(ctx); err != nil {
			t.Fatal(err)
		}
		// Продвигаем часы мимо fire_at (30s, 2m, 10m, 30m, 1h).
		env.clock.Advance(61 * time.Minute)
	}
	status, attempts, _, _ := rowState(t, env.pool, remID)
	if status != "failed" || attempts != 5 {
		t.Fatalf("row = (%q, %d), want (failed, 5)", status, attempts)
	}
	// Больше не отправляется.
	n := env.notifier.Count()
	if err := env.worker.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if env.notifier.Count() != n {
		t.Errorf("notifier called after terminal failure")
	}
	_ = now
}

// Рестарт: лок «ghost» старше LOCK_TTL → ReleaseStale → одна отправка.
func TestWorkerReleaseStaleLock(t *testing.T) {
	env := newEnv(t)
	ctx := context.Background()
	remID := personalReminderFixture(t, env)
	now := env.clock.Now()

	// Симулируем упавший воркер: лок 3 минуты назад.
	if _, err := env.pool.Exec(ctx,
		`UPDATE reminders SET locked_by='ghost', locked_at=$2 WHERE id=$1`,
		remID, now.Add(-3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := env.worker.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if got := env.notifier.Count(); got != 1 {
		t.Fatalf("notifier calls = %d, want 1", got)
	}
	status, _, _, _ := rowState(t, env.pool, remID)
	if status != "sent" {
		t.Errorf("status = %q, want sent", status)
	}
}

// Второй воркер на той же строке: реальная гонка двух Worker с разными
// WorkerID на одном due reminder → ровно одна отправка и один sent.
func TestWorkerMarkSentRaceSuppressesDoubleSend(t *testing.T) {
	env := newEnv(t)
	ctx := context.Background()
	remID := personalReminderFixture(t, env)

	// Второй воркер: свой WorkerID, тот же пул/БД.
	other := newEnvWith(t, env.pool, func(d *Deps, c *Config) {
		c.WorkerID = "w-other"
		d.Notifier = env.notifier // общий notifier: считаем суммарные отправки
	})

	// Гонка: оба тика стартуют одновременно. SKIP LOCKED отдаёт строку только
	// одному; проигравший не должен отправить ничего.
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, w := range []*Worker{env.worker, other.worker} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs[i] = w.Tick(ctx)
		}()
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("worker %d Tick: %v", i, err)
		}
	}

	// Ровно одна отправка, строка sent (не pending/дубль).
	if got := env.notifier.Count(); got != 1 {
		t.Errorf("notifier calls = %d, want exactly 1", got)
	}
	status, attempts, _, _ := rowState(t, env.pool, remID)
	if status != "sent" || attempts != 0 {
		t.Errorf("row = (%q, attempts=%d), want (sent, 0)", status, attempts)
	}
}

// I-4: сбой Commit fan-out'а не должен приводить к повторной отправке в чат —
// срабатывает fallback-плейн-MarkSent, строка остаётся sent (без детей).
func TestWorkerFanoutCommitFailureKeepsNoDuplicateInvariant(t *testing.T) {
	env := newEnv(t)
	ctx := context.Background()
	fx := groupFixture(t, env, []memberSpec{{dmOverride: boolPtr(true)}})

	// Ломаем только fan-out-транзакцию: FetchDue-коммит (1-й) проходит,
	// fan-out-коммит (2-й) падает.
	pool := &failingCommitPool{TxBeginner: env.pool}
	pool.FailCommitAt(2)
	env.worker.deps.Pool = pool

	if err := env.worker.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	// В чат отправлено ОДИН раз.
	if got := env.notifier.Count(); got != 1 {
		t.Fatalf("notifier calls = %d, want 1", got)
	}
	// Fallback MarkSent сработал: строка sent, а не pending (иначе после
	// ReleaseStale/рестарта чат получил бы дубль).
	status, attempts, _, _ := rowState(t, env.pool, fx.reminderIDs[0])
	if status != "sent" || attempts != 0 {
		t.Fatalf("row = (%q, attempts=%d), want (sent, 0)", status, attempts)
	}
	// И повторный тик больше ничего не отправляет.
	if err := env.worker.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if got := env.notifier.Count(); got != 1 {
		t.Errorf("after retry tick notifier calls = %d, want 1", got)
	}
}

// I-2: отмена ctx во время отправки не должна рвать фиксацию результата —
// иначе строка остаётся locked и после ReleaseStale чат получает дубль.
func TestWorkerShutdownFinalizesInFlightSend(t *testing.T) {
	env := newEnv(t)
	remID := personalReminderFixture(t, env)

	// Отправка «виснет», пока тест не отпустит её; после этого фиксация
	// должна пройти, несмотря на отменённый ctx.
	release := make(chan struct{})
	sendStarted := make(chan struct{})
	var once sync.Once
	env.notifier.errFn = func(c notifyCall) error {
		if c.kind == "user" {
			once.Do(func() { close(sendStarted) })
			<-release
		}
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	tickDone := make(chan error, 1)
	go func() { tickDone <- env.worker.Tick(ctx) }()

	<-sendStarted
	cancel()       // воркер остановлен, отправка в полёте
	close(release) // отправка завершается уже после отмены

	select {
	case err := <-tickDone:
		if err != nil {
			t.Fatalf("Tick: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Tick did not finish after cancel")
	}

	if got := env.notifier.Count(); got != 1 {
		t.Fatalf("notifier calls = %d, want 1", got)
	}
	status, _, _, _ := rowState(t, env.pool, remID)
	if status != "sent" {
		t.Fatalf("status = %q, want sent (finalize must survive cancellation)", status)
	}
}

// 429 с retry_after=7 → MarkFailed с fire_at=now+7s, attempts+1.
func TestWorkerRateLimited429(t *testing.T) {
	env := newEnv(t)
	ctx := context.Background()
	remID := personalReminderFixture(t, env)
	now := env.clock.Now()

	env.notifier.errFn = func(c notifyCall) error {
		return &domain.RateLimitError{RetryAfter: 7 * time.Second}
	}
	if err := env.worker.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	status, attempts, fireAt, lastErr := rowState(t, env.pool, remID)
	if status != "pending" || attempts != 1 {
		t.Fatalf("row = (%q, %d), want (pending, 1)", status, attempts)
	}
	if !fireAt.Equal(now.Add(7 * time.Second)) {
		t.Errorf("fire_at = %v, want %v", fireAt, now.Add(7*time.Second))
	}
	if lastErr == nil || *lastErr != "rate limited" {
		t.Errorf("last_error = %v", lastErr)
	}
}

// BlockedError → карантин на 7 дней, last_error содержит blocked,
// users.bot_blocked зафиксирован (I-1).
func TestWorkerBotBlockedQuarantine(t *testing.T) {
	env := newEnv(t)
	ctx := context.Background()
	remID := personalReminderFixture(t, env)
	now := env.clock.Now()
	missingTelegramID := int64(5001) // personalReminderFixture создаёт user с этим telegram_id

	env.notifier.errFn = func(c notifyCall) error {
		return &domain.BotBlockedError{UserID: c.userID}
	}
	if err := env.worker.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	status, attempts, fireAt, lastErr := rowState(t, env.pool, remID)
	if status != "pending" {
		t.Fatalf("status = %q, want pending", status)
	}
	if !fireAt.Equal(now.Add(7 * 24 * time.Hour)) {
		t.Errorf("fire_at = %v, want %v", fireAt, now.Add(7*24*time.Hour))
	}
	if lastErr == nil || !strings.Contains(*lastErr, "blocked") {
		t.Errorf("last_error = %v, want contains 'blocked'", lastErr)
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1", attempts)
	}

	// I-1: флаг записан в БД (иначе ListDMTargets никогда не отфильтрует).
	marked := env.users.MarkedBlocked()
	if len(marked) != 1 || marked[0].telegramID != missingTelegramID || !marked[0].blocked {
		t.Fatalf("MarkBotBlocked calls = %+v, want [{telegram_id=%d blocked=true}]", marked, missingTelegramID)
	}
	var blocked bool
	if err := env.pool.QueryRow(ctx,
		`SELECT bot_blocked FROM users WHERE telegram_id=$1`, missingTelegramID).Scan(&blocked); err != nil {
		t.Fatal(err)
	}
	if !blocked {
		t.Errorf("users.bot_blocked not persisted")
	}
}

// I-1 (продолжение): получив 403, воркер фиксирует флаг, и СЛЕДУЮЩИЙ fan-out
// этого участника уже не выбирает (ListDMTargets → NOT bot_blocked).
func TestWorkerBotBlockedExcludedFromLaterFanout(t *testing.T) {
	env := newEnv(t)
	ctx := context.Background()
	// Единственный участник с dm ON — отправим ему дубль, получим 403.
	fx := groupFixture(t, env, []memberSpec{{dmOverride: boolPtr(true)}})
	parentID := fx.reminderIDs[0]
	memberTelegramID := int64(5110)

	// Первый тик: чат + создание ребёнка.
	if err := env.worker.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	// Ребёнок падает с 403 → флаг фиксируется.
	env.notifier.errFn = func(c notifyCall) error {
		if c.kind == "user" {
			return &domain.BotBlockedError{UserID: c.userID}
		}
		return nil
	}
	env.clock.Advance(time.Second)
	if err := env.worker.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if got := env.users.MarkedBlocked(); len(got) != 1 || got[0].telegramID != memberTelegramID {
		t.Fatalf("MarkBotBlocked = %+v, want telegram_id=%d", got, memberTelegramID)
	}

	// Второе групповое напоминание того же дедлайна: участник уже blocked →
	// детей для него нет (фильтр ListDMTargets реально работает).
	second := newSecondDueGroupReminder(t, env, fx)
	env.notifier.errFn = nil
	env.clock.Advance(time.Minute)
	if err := env.worker.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if st, _, _, _ := rowState(t, env.pool, second); st != "sent" {
		t.Fatalf("second parent status = %q, want sent", st)
	}
	var children int
	if err := env.pool.QueryRow(ctx,
		`SELECT count(*) FROM reminders WHERE kind='dm_dup' AND deadline_id=$1`,
		fx.deadlineID).Scan(&children); err != nil {
		t.Fatal(err)
	}
	if children != 1 {
		t.Errorf("dm_dup children = %d, want 1 (blocked member excluded from later fan-out)", children)
	}
	_ = parentID
}

// newSecondDueGroupReminder — добавляет к тому же дедлайну ещё один due
// reminder (имитация второго пресета, сработавшего позже).
func newSecondDueGroupReminder(t *testing.T, env *testEnv, fx groupFixtureResult) int64 {
	t.Helper()
	ctx := context.Background()
	mins := 60
	batch := []domain.Reminder{{
		DeadlineID: fx.deadlineID, Kind: domain.KindCustomOffset, OffsetMinutes: &mins,
		FireAt: env.clock.Now().Add(-time.Minute), Status: domain.ReminderStatusPending,
	}}
	if err := repo.NewReminders(env.pool).CreateBatch(ctx, batch); err != nil {
		t.Fatal(err)
	}
	return batch[0].ID // CreateBatch заполняет ID элементов среза
}

// dm_dup fan-out: 3 участника с dm on/off/заблокирован → дети для 2,
// родитель sent; ошибка ребёнка не откатывает родителя.
func TestWorkerDMDupFanout(t *testing.T) {
	env := newEnv(t)
	ctx := context.Background()
	fx := groupFixture(t, env, []memberSpec{
		{dmOverride: boolPtr(true)},                   // override ON → ребёнок
		{defaultOn: true},                             // default ON → ребёнок
		{dmOverride: boolPtr(false)},                  // override OFF → нет
		{dmOverride: boolPtr(true), botBlocked: true}, // dm ON, но blocked → нет
	})
	remID := fx.reminderIDs[0]

	// Первый тик: чат-отправка + fan-out детей (не user-отправки: дети
	// fire_at=now, но tick уже выбрал батч ДО их создания).
	if err := env.worker.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	calls := env.notifier.Calls()
	var chatCalls, userCalls int
	for _, c := range calls {
		if c.kind == "chat" {
			chatCalls++
		} else {
			userCalls++
		}
	}
	if chatCalls != 1 {
		t.Fatalf("chat calls = %d, want 1", chatCalls)
	}
	if userCalls != 0 {
		t.Fatalf("user calls on first tick = %d, want 0 (children not yet due)", userCalls)
	}
	status, _, _, _ := rowState(t, env.pool, remID)
	if status != "sent" {
		t.Fatalf("parent status = %q, want sent", status)
	}

	// Дети созданы: ровно 2 (blocked и off исключены).
	childIDs := dmDupChildIDs(t, env, fx.deadlineID)
	if len(childIDs) != 2 {
		t.Fatalf("dm_dup children = %d, want 2", len(childIDs))
	}

	// Второй тик: дети due → user-отправки; первая падает.
	// failUser защищён мьютексом — Tick обрабатывает батч конкурентно
	// (гонка на флаге ловилась бы -race).
	var failOnce sync.Once
	env.notifier.errFn = func(c notifyCall) error {
		if c.kind != "user" {
			return nil
		}
		failed := false
		failOnce.Do(func() { failed = true })
		if failed {
			return errors.New("dm unavailable")
		}
		return nil
	}
	env.clock.Advance(time.Second)
	if err := env.worker.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	calls = env.notifier.Calls()
	userCalls = 0
	for _, c := range calls {
		if c.kind == "user" {
			userCalls++
		}
	}
	if userCalls != 2 {
		t.Fatalf("user calls = %d, want 2 (both children attempted)", userCalls)
	}
	// Родитель остался sent.
	status, _, _, _ = rowState(t, env.pool, remID)
	if status != "sent" {
		t.Errorf("parent status after child failure = %q, want sent", status)
	}
	// Упавший ребёнок — pending с backoff.
	var failedChild int
	childRetry := env.clock.Now().Add(30 * time.Second) // часы на момент второго тика
	for _, id := range childIDs {
		st, at, fa, _ := rowState(t, env.pool, id)
		if st == "pending" && at == 1 {
			if !fa.Equal(childRetry) {
				t.Errorf("child fire_at = %v, want %v", fa, childRetry)
			}
			failedChild++
		} else if st != "sent" {
			t.Errorf("child %d status = %q", id, st)
		}
	}
	if failedChild != 1 {
		t.Errorf("failed children = %d, want 1", failedChild)
	}
}

// dmDupChildIDs — id всех dm_dup-строк дедлайна.
func dmDupChildIDs(t *testing.T, env *testEnv, deadlineID int64) []int64 {
	t.Helper()
	rows, err := env.pool.Query(context.Background(),
		`SELECT id FROM reminders WHERE kind='dm_dup' AND deadline_id=$1 ORDER BY id`, deadlineID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		out = append(out, id)
	}
	return out
}

// I-3: дедлайн с ДВУМЯ пресетами → каждый fan-out создаёт своего ребёнка и
// участник получает ДВА ЛС. Прежний unique (deadline_id, target_user_id)
// молча съедал второй дубль; новый (…, fire_at) этого не допускает.
func TestWorkerDMDupMultipleFanoutsPerDeadline(t *testing.T) {
	env := newEnv(t)
	ctx := context.Background()
	// Один участник с dm ON — он должен получить оба ЛС.
	fx := groupFixture(t, env, []memberSpec{{dmOverride: boolPtr(true)}},
		24*time.Hour, time.Hour)

	// Первое напоминание (fire_at в прошлом у обоих, но обрабатываем по одному:
	// Batch ограничиваем до 1, чтобы fan-out'ы были разнесены во времени —
	// ровно как в проде с разными пресетами).
	env.worker.cfg.Batch = 1
	if err := env.worker.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	firstChildren := dmDupChildIDs(t, env, fx.deadlineID)
	if len(firstChildren) != 1 {
		t.Fatalf("children after 1st fan-out = %d, want 1", len(firstChildren))
	}

	// Второй пресет: сдвигаем часы (другой fan-out → другой fire_at).
	env.clock.Advance(time.Minute)
	if err := env.worker.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	children := dmDupChildIDs(t, env, fx.deadlineID)
	if len(children) != 2 {
		t.Fatalf("children after 2nd fan-out = %d, want 2 (one per reminder)", len(children))
	}

	// Оба родителя sent, оба ребёнка доставлены: 2 ЛС участнику.
	for _, id := range fx.reminderIDs {
		if st, _, _, _ := rowState(t, env.pool, id); st != "sent" {
			t.Errorf("parent %d status = %q, want sent", id, st)
		}
	}
	// Возвращаем обычный размер батча: доставка обоих детей за один тик.
	env.worker.cfg.Batch = 50
	env.clock.Advance(time.Second)
	beforeUserCalls := countUserCalls(env.notifier.Calls())
	if err := env.worker.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if got := countUserCalls(env.notifier.Calls()) - beforeUserCalls; got != 2 {
		t.Fatalf("DM sends = %d, want 2 (one per fan-out)", got)
	}
	for _, id := range children {
		if st, _, _, _ := rowState(t, env.pool, id); st != "sent" {
			t.Errorf("child %d status = %q, want sent", id, st)
		}
	}
}

func countUserCalls(calls []notifyCall) int {
	n := 0
	for _, c := range calls {
		if c.kind == "user" {
			n++
		}
	}
	return n
}

// Дедлайн завершён → reminder потребляется молча (без отправки).
func TestWorkerSkipsDoneDeadline(t *testing.T) {
	env := newEnv(t)
	ctx := context.Background()
	remID := personalReminderFixture(t, env)

	if _, err := env.pool.Exec(ctx,
		`UPDATE deadlines SET status='done' WHERE id=(SELECT deadline_id FROM reminders WHERE id=$1)`,
		remID); err != nil {
		t.Fatal(err)
	}
	if err := env.worker.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if got := env.notifier.Count(); got != 0 {
		t.Fatalf("notifier calls = %d, want 0", got)
	}
	status, _, _, _ := rowState(t, env.pool, remID)
	if status != "sent" {
		t.Errorf("status = %q, want sent (consumed silently)", status)
	}
}

// failingBindingRepo — GetByGroup отдаёт заданную ошибку (minor 6: сбой БД
// не должен выглядеть как «привязки нет»).
type failingBindingRepo struct {
	domain.ChatBindingRepo
	err error
}

func (r *failingBindingRepo) GetByGroup(ctx context.Context, groupID int64) (*domain.ChatBinding, error) {
	return nil, r.err
}

// Нет биндинга чата → MarkFailed с ретраем через 1 час.
func TestWorkerMissingBinding(t *testing.T) {
	env := newEnv(t)
	ctx := context.Background()
	// Группа без биндинга.
	owner := insertUser(t, env.pool, 5200)
	g := &domain.Group{Slug: "М8О-902-23", Title: "NB", Status: domain.GroupStatusActive,
		CreatedBy: owner, DefaultPresets: []time.Duration{24 * time.Hour}}
	if err := repo.NewGroups(env.pool).Create(ctx, g); err != nil {
		t.Fatal(err)
	}
	dl := &domain.Deadline{GroupID: &g.ID, Title: "X", DueAt: env.clock.Now().Add(24 * time.Hour),
		TZ: "Europe/Moscow", CreatedBy: owner, Status: domain.DeadlineStatusActive}
	mins := 24 * 60
	reminders := []domain.Reminder{{Kind: domain.KindPreset, OffsetMinutes: &mins,
		FireAt: env.clock.Now().Add(-time.Minute), Status: domain.ReminderStatusPending}}
	if err := repo.NewDeadlines(env.pool).Create(ctx, dl, reminders); err != nil {
		t.Fatal(err)
	}
	remID := reminders[0].ID
	now := env.clock.Now()

	if err := env.worker.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if got := env.notifier.Count(); got != 0 {
		t.Fatalf("notifier calls = %d, want 0", got)
	}
	status, attempts, fireAt, lastErr := rowState(t, env.pool, remID)
	if status != "pending" || attempts != 1 {
		t.Fatalf("row = (%q, %d), want (pending, 1)", status, attempts)
	}
	if !fireAt.Equal(now.Add(time.Hour)) {
		t.Errorf("fire_at = %v, want %v", fireAt, now.Add(time.Hour))
	}
	if lastErr == nil || *lastErr != "no chat binding" {
		t.Errorf("last_error = %v", lastErr)
	}
}

// 403 на отправку в ЧАТ ГРУППЫ не означает «пользователь заблокировал бота»:
// users.bot_blocked не трогается (иначе блокировался бы случайный telegram_id).
func TestWorkerChat403DoesNotMarkUserBlocked(t *testing.T) {
	env := newEnv(t)
	ctx := context.Background()
	fx := groupFixture(t, env, []memberSpec{{dmOverride: boolPtr(true)}})

	env.notifier.errFn = func(c notifyCall) error {
		if c.kind == "chat" {
			return &domain.BotBlockedError{UserID: c.chatID}
		}
		return nil
	}
	if err := env.worker.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if got := env.users.MarkedBlocked(); len(got) != 0 {
		t.Errorf("MarkBotBlocked called for chat send: %+v", got)
	}
	// Родитель всё равно в карантине (ошибка классифицируется как blocked).
	status, _, fireAt, _ := rowState(t, env.pool, fx.reminderIDs[0])
	now := env.clock.Now()
	if status != "pending" || !fireAt.Equal(now.Add(7*24*time.Hour)) {
		t.Errorf("row = (%q, fire_at=%v), want quarantined pending", status, fireAt)
	}
}

// minor 6: сбой БД при чтении биндинга ≠ «привязки нет» — обычный backoff
// (30s первого шага), а не часовой ретрай.
func TestWorkerBindingLoadErrorUsesBackoff(t *testing.T) {
	env := newEnv(t)
	ctx := context.Background()
	remID := groupFixture(t, env, nil).reminderIDs[0]
	now := env.clock.Now()

	env.worker.deps.Bindings = &failingBindingRepo{err: errors.New("db down")}
	if err := env.worker.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	status, attempts, fireAt, lastErr := rowState(t, env.pool, remID)
	if status != "pending" || attempts != 1 {
		t.Fatalf("row = (%q, %d), want (pending, 1)", status, attempts)
	}
	if !fireAt.Equal(now.Add(30 * time.Second)) {
		t.Errorf("fire_at = %v, want %v (backoff[0], not 1h)", fireAt, now.Add(30*time.Second))
	}
	if lastErr == nil || !strings.Contains(*lastErr, "binding load") {
		t.Errorf("last_error = %v, want 'binding load: ...'", lastErr)
	}
}

// Graceful shutdown: Run завершается по ctx.Done, дожидаясь батча.
func TestWorkerGracefulShutdown(t *testing.T) {
	env := newEnv(t)
	personalReminderFixture(t, env)

	ctx, cancel := context.WithCancel(context.Background())
	// Отправляем медленно: errFn со sleep-подобной задержкой не нужен —
	// достаточно убедиться, что Run выходит после cancel.
	done := make(chan error, 1)
	go func() { done <- env.worker.Run(ctx) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case <-done:
		// ок
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not exit after cancel")
	}
}
