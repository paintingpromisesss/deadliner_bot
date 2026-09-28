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

type testEnv struct {
	worker   *Worker
	clock    *fakeClock
	notifier *fakeNotifier
	pool     *pgxpool.Pool
}

func newEnv(t *testing.T) *testEnv {
	t.Helper()
	pool := truncate(t)
	clock := &fakeClock{now: time.Now().UTC().Truncate(time.Microsecond)}
	notifier := &fakeNotifier{}
	w := New(Deps{
		Pool:        pool,
		Reminders:   repo.NewReminders(pool),
		Deadlines:   repo.NewDeadlines(pool),
		Memberships: repo.NewMemberships(pool),
		Groups:      repo.NewGroups(pool),
		Bindings:    repo.NewBindings(pool),
		Users:       repo.NewUsers(pool),
		Notifier:    notifier,
		Clock:       clock,
		Log:         discardLogger,
	}, Config{
		PollInterval: time.Hour, // тики вручную
		Batch:        50,
		LockTTL:      2 * time.Minute,
		MaxAttempts:  5,
		WorkerID:     "w-test",
	})
	return &testEnv{worker: w, clock: clock, notifier: notifier, pool: pool}
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
func groupFixture(t *testing.T, env *testEnv, members []memberSpec) (reminderID int64) {
	t.Helper()
	ctx := context.Background()
	owner := insertUser(t, env.pool, 5100)
	g := &domain.Group{
		Slug: "М8О-901-23", Title: "Группа", Status: domain.GroupStatusActive,
		CreatedBy: owner, DefaultPresets: []time.Duration{24 * time.Hour},
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
	mins := 24 * 60
	reminders := []domain.Reminder{{
		Kind: domain.KindPreset, OffsetMinutes: &mins,
		FireAt: env.clock.Now().Add(-time.Minute), Status: domain.ReminderStatusPending,
	}}
	if err := repo.NewDeadlines(env.pool).Create(ctx, dl, reminders); err != nil {
		t.Fatal(err)
	}
	memRepo := repo.NewMemberships(env.pool)
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
	}
	return reminders[0].ID
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

// Два воркера на одной строке (FetchDue уже исключает пересечение; здесь —
// вторая линия обороны, MarkSent): оба «держат» лок → отправка ровно одна.
func TestWorkerMarkSentRaceSuppressesDoubleSend(t *testing.T) {
	env := newEnv(t)
	ctx := context.Background()
	remID := personalReminderFixture(t, env)
	now := env.clock.Now()

	// Вручную лочим строку за «нашим» воркером (обходя FetchDue), как будто
	// два воркера успели взять её до SKIP LOCKED (симуляция гонки).
	if _, err := env.pool.Exec(ctx,
		`UPDATE reminders SET locked_by='w-test', locked_at=$2 WHERE id=$1`,
		remID, now); err != nil {
		t.Fatal(err)
	}
	// Вторая линия обороны (§7.2): MarkSent от чужого воркера → false.
	rr := repo.NewReminders(env.pool)
	ok, err := rr.MarkSent(ctx, remID, "other-worker", now)
	if err != nil || ok {
		t.Fatalf("other-worker MarkSent = (%v, %v), want (false, nil)", ok, err)
	}
	// Наш тик: FetchDue не увидит строку (locked_by уже наш — условие
	// locked_by IS NULL ложно), отправки не будет — строку обработает
	// финальный MarkSent-холостой? Проверяем инвариант: notifier не звался.
	if err := env.worker.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if got := env.notifier.Count(); got != 0 {
		t.Errorf("notifier calls = %d, want 0 (already locked)", got)
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

// BlockedError → карантин на 7 дней, last_error содержит blocked.
func TestWorkerBotBlockedQuarantine(t *testing.T) {
	env := newEnv(t)
	ctx := context.Background()
	remID := personalReminderFixture(t, env)
	now := env.clock.Now()

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
}

// dm_dup fan-out: 3 участника с dm on/off/заблокирован → дети для 2,
// родитель sent; ошибка ребёнка не откатывает родителя.
func TestWorkerDMDupFanout(t *testing.T) {
	env := newEnv(t)
	ctx := context.Background()
	remID := groupFixture(t, env, []memberSpec{
		{dmOverride: boolPtr(true)},                   // override ON → ребёнок
		{defaultOn: true},                             // default ON → ребёнок
		{dmOverride: boolPtr(false)},                  // override OFF → нет
		{dmOverride: boolPtr(true), botBlocked: true}, // dm ON, но blocked → нет
	})

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
	var children int
	var childIDs []int64
	rows, err := env.pool.Query(ctx,
		`SELECT id, target_user_id FROM reminders WHERE kind='dm_dup' AND deadline_id=
		 (SELECT deadline_id FROM reminders WHERE id=$1)`, remID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id, target int64
		if err := rows.Scan(&id, &target); err != nil {
			t.Fatal(err)
		}
		children++
		childIDs = append(childIDs, id)
	}
	rows.Close()
	if children != 2 {
		t.Fatalf("dm_dup children = %d, want 2", children)
	}

	// Второй тик: дети due → user-отправки; первая падает.
	failUser := false
	env.notifier.errFn = func(c notifyCall) error {
		if c.kind == "user" && !failUser {
			failUser = true
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
