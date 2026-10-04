package cmd

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/sauron/deadliner/internal/platform/db"
)

// NOTE: контейнерная обвязка продублирована из internal/platform/repo и
// internal/platform/httpapi — хелперы тестов не импортируются между пакетами.
var (
	testPool *pgxpool.Pool
	// testDatabaseURL — строка подключения контейнера: нужна тестам serve
	// (Serve стартует с миграций и подключения к БД).
	testDatabaseURL string
)

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
	testPool = pool

	// URL контейнера нужен тестам serve: Serve начинается с миграций и
	// подключения, поэтому заглушки БД недостаточно.
	testDatabaseURL = url

	// CLI читает конфиг из env: DATABASE_URL указывает на контейнер, а
	// BOT_TOKEN обязателен для config.Load (serve-переменные CLI не нужны).
	os.Setenv("DATABASE_URL", url)
	os.Setenv("BOT_TOKEN", "123456:CLI-TEST")

	code := m.Run()

	pool.Close()
	_ = ctr.Terminate(context.Background())
	os.Exit(code)
}

func newTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := testPool.Exec(ctx, `TRUNCATE
		users, groups, chat_bindings, group_memberships, deadlines, reminders,
		invites, user_action_counters, chat_action_counters, sessions,
		outbox_messages, audit_log CASCADE`)
	if err != nil {
		t.Fatalf("truncate: %v", err)
	}
	return testPool
}

func insertUser(t *testing.T, telegramID int64) int64 {
	t.Helper()
	var id int64
	err := testPool.QueryRow(t.Context(),
		`INSERT INTO users (telegram_id, username, first_name)
		 VALUES ($1, 'user', 'Иван') RETURNING id`, telegramID).Scan(&id)
	if err != nil {
		t.Fatalf("insert user %d: %v", telegramID, err)
	}
	return id
}

func insertGroup(t *testing.T, slug string, createdBy int64, expires *time.Time) int64 {
	t.Helper()
	var id int64
	err := testPool.QueryRow(t.Context(),
		`INSERT INTO groups (slug, slug_norm, title, status, created_by, claim_expires_at)
		 VALUES ($1, upper($1), $2, 'pending', $3, $4) RETURNING id`,
		slug, "Группа "+slug, createdBy, expires).Scan(&id)
	if err != nil {
		t.Fatalf("insert group %s: %v", slug, err)
	}
	return id
}

// runAdmin гоняет CLI-путь целиком (config.Load → db.Connect → репозитории →
// moderation.Service → команда) и возвращает код выхода и вывод (stdout +
// stderr в один буфер: тестам важен суммарный текст ответа).
func runAdmin(t *testing.T, args ...string) (int, string) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	var out bytes.Buffer
	code := run(t.Context(), args, log, &out, &out)
	return code, out.String()
}

// --- usage (exit 2, без подключения к БД) ---

func TestAdminUsageErrors(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"no subcommand", nil},
		{"unknown subcommand", []string{"frobnicate"}},
		{"promote without arg", []string{"promote"}},
		{"ban without arg", []string{"ban"}},
		{"unban without arg", []string{"unban"}},
		{"delete-group without arg", []string{"delete-group"}},
		{"promote non-numeric", []string{"promote", "ivan"}},
		{"ban non-numeric", []string{"ban", "1.5"}},
		{"stats with extra arg", []string{"stats", "42"}},
		{"cleanup with extra arg", []string{"cleanup", "now"}},
		{"list-groups with extra arg", []string{"list-groups", "active", "extra"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, out := runAdmin(t, c.args...)
			if code != 2 {
				t.Errorf("exit code = %d, want 2; output: %s", code, out)
			}
			if !strings.Contains(out, "deadliner admin") {
				t.Errorf("usage text missing from output: %q", out)
			}
		})
	}
}

// Ошибка использования распознаётся ДО подключения к БД: опечатка в команде не
// должна требовать рабочей базы (и не должна её дёргать).
func TestAdminUsageErrorNeedsNoDatabase(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	code, out := runAdmin(t, "frobnicate")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2; output: %s", code, out)
	}
	if strings.Contains(out, "config:") {
		t.Errorf("usage path touched config/db: %q", out)
	}
}

// --- happy paths on a real database ---

func TestAdminPromoteAlias(t *testing.T) {
	newTestDB(t)
	insertUser(t, 555)

	code, out := runAdmin(t, "promote-superadmin", "555")
	if code != 0 {
		t.Fatalf("promote-superadmin exit = %d, want 0; output: %s", code, out)
	}
	var superadmin bool
	if err := testPool.QueryRow(t.Context(),
		`SELECT is_superadmin FROM users WHERE telegram_id = 555`).Scan(&superadmin); err != nil {
		t.Fatalf("select superadmin flag: %v", err)
	}
	if !superadmin {
		t.Error("promote-superadmin did not promote the user")
	}
}

func TestAdminPromoteThenStats(t *testing.T) {
	newTestDB(t)
	insertUser(t, 555)
	insertUser(t, 777)

	code, out := runAdmin(t, "promote", "555")
	if code != 0 {
		t.Fatalf("promote exit = %d, want 0; output: %s", code, out)
	}
	if !strings.Contains(out, "555") {
		t.Errorf("promote output does not mention the target: %q", out)
	}
	var superadmin bool
	if err := testPool.QueryRow(t.Context(),
		`SELECT is_superadmin FROM users WHERE telegram_id = 555`).Scan(&superadmin); err != nil {
		t.Fatalf("select superadmin flag: %v", err)
	}
	if !superadmin {
		t.Error("user 555 was not promoted in the database")
	}

	// Аудит действия CLI: actor_user_id NULL (системный актор, id=0).
	var action string
	var actor *int64
	if err := testPool.QueryRow(t.Context(),
		`SELECT action, actor_user_id FROM audit_log ORDER BY id DESC LIMIT 1`).
		Scan(&action, &actor); err != nil {
		t.Fatalf("select audit: %v", err)
	}
	if action != "user.promote" {
		t.Errorf("audit action = %q, want user.promote", action)
	}
	if actor != nil {
		t.Errorf("audit actor = %v, want NULL for the CLI system actor", *actor)
	}

	// stats: числа из БД (2 пользователя, 0 групп) в человекочитаемом виде.
	code, out = runAdmin(t, "stats")
	if code != 0 {
		t.Fatalf("stats exit = %d, want 0; output: %s", code, out)
	}
	for _, want := range []string{"Пользователи", "2", "Группы", "0"} {
		if !strings.Contains(out, want) {
			t.Errorf("stats output missing %q: %s", want, out)
		}
	}

	// promote идемпотентен: повторный вызов — успех.
	if code, out = runAdmin(t, "promote", "555"); code != 0 {
		t.Errorf("second promote = %d, want 0; output: %s", code, out)
	}
	if code, out = runAdmin(t, "promote", "999"); code != 1 {
		t.Errorf("promote of an unknown user = %d, want 1; output: %s", code, out)
	}
}

func TestAdminBanAndUnban(t *testing.T) {
	newTestDB(t)
	uid := insertUser(t, 555)
	if _, err := testPool.Exec(t.Context(),
		`INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ('h', $1, now() + interval '1 day')`,
		uid); err != nil {
		t.Fatalf("insert session: %v", err)
	}

	code, out := runAdmin(t, "ban", "555")
	if code != 0 {
		t.Fatalf("ban exit = %d, want 0; output: %s", code, out)
	}
	var banned bool
	if err := testPool.QueryRow(t.Context(),
		`SELECT is_banned FROM users WHERE telegram_id = 555`).Scan(&banned); err != nil {
		t.Fatalf("select banned flag: %v", err)
	}
	if !banned {
		t.Error("user 555 was not banned")
	}
	var sessions int
	if err := testPool.QueryRow(t.Context(),
		`SELECT count(*) FROM sessions WHERE user_id = $1`, uid).Scan(&sessions); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if sessions != 0 {
		t.Errorf("sessions = %d, want 0 (ban revokes them immediately)", sessions)
	}

	code, out = runAdmin(t, "unban", "555")
	if code != 0 {
		t.Fatalf("unban exit = %d, want 0; output: %s", code, out)
	}
	if err := testPool.QueryRow(t.Context(),
		`SELECT is_banned FROM users WHERE telegram_id = 555`).Scan(&banned); err != nil {
		t.Fatalf("select banned flag: %v", err)
	}
	if banned {
		t.Error("user 555 is still banned after unban")
	}

	if code, out = runAdmin(t, "ban", "999"); code != 1 {
		t.Errorf("ban of an unknown user = %d, want 1; output: %s", code, out)
	}
}

func TestAdminDeleteGroup(t *testing.T) {
	newTestDB(t)
	uid := insertUser(t, 555)
	past := time.Now().UTC().Add(-time.Hour)
	gid := insertGroup(t, "ИКБО-33-21", uid, &past)
	if _, err := testPool.Exec(t.Context(),
		`INSERT INTO deadlines (group_id, title, due_at, created_by)
		 VALUES ($1, 'Курсовая', now() + interval '2 days', $2)`, gid, uid); err != nil {
		t.Fatalf("insert deadline: %v", err)
	}

	// Слаг в произвольном регистре и с пробелами — нормализуется.
	code, out := runAdmin(t, "delete-group", " икбо-33-21 ")
	if code != 0 {
		t.Fatalf("delete-group exit = %d, want 0; output: %s", code, out)
	}
	if !strings.Contains(out, "ИКБО-33-21") {
		t.Errorf("output does not mention the slug: %q", out)
	}
	var deleted *time.Time
	if err := testPool.QueryRow(t.Context(),
		`SELECT deleted_at FROM groups WHERE id = $1`, gid).Scan(&deleted); err != nil {
		t.Fatalf("select deleted_at: %v", err)
	}
	if deleted == nil {
		t.Error("group was not soft-deleted")
	}

	if code, out = runAdmin(t, "delete-group", "НЕТ-1-1"); code != 1 {
		t.Errorf("delete-group of an unknown slug = %d, want 1; output: %s", code, out)
	}
}

// cleanup удаляет протухшую pending-группу без привязки и админа и печатает отчёт.
func TestAdminCleanup(t *testing.T) {
	newTestDB(t)
	uid := insertUser(t, 555)
	past := time.Now().UTC().Add(-time.Hour)
	doomed := insertGroup(t, "ИКБО-41-21", uid, &past)
	future := time.Now().UTC().Add(time.Hour)
	alive := insertGroup(t, "ИКБО-42-21", uid, &future)
	fresh := nowInsertedGroup(t, uid)

	code, out := runAdmin(t, "cleanup")
	if code != 0 {
		t.Fatalf("cleanup exit = %d, want 0; output: %s", code, out)
	}
	if !strings.Contains(out, "Групп удалено: 1") {
		t.Errorf("cleanup output missing the report: %q", out)
	}

	var doomedCount int
	if err := testPool.QueryRow(t.Context(),
		`SELECT count(*) FROM groups WHERE id = $1`, doomed).Scan(&doomedCount); err != nil {
		t.Fatalf("select doomed: %v", err)
	}
	if doomedCount != 0 {
		t.Errorf("expired pending group was not hard-deleted by cleanup: count = %d", doomedCount)
	}
	var deleted *time.Time
	for _, id := range []int64{alive, fresh} {
		if err := testPool.QueryRow(t.Context(),
			`SELECT deleted_at FROM groups WHERE id = $1`, id).Scan(&deleted); err != nil {
			t.Fatalf("select group %d: %v", id, err)
		}
		if deleted != nil {
			t.Errorf("group %d must be kept by cleanup", id)
		}
	}

	// Аудит cleanup-прогона: системный актор (actor_user_id NULL).
	var actor *int64
	if err := testPool.QueryRow(t.Context(),
		`SELECT actor_user_id FROM audit_log WHERE action = 'cleanup.run' ORDER BY id DESC LIMIT 1`).
		Scan(&actor); err != nil {
		t.Fatalf("select cleanup audit: %v", err)
	}
	if actor != nil {
		t.Errorf("cleanup audit actor = %v, want NULL", *actor)
	}
}

// Демонстрация сквозного wiring'а: CLI-cleanup берёт ретенцию из конфига
// (COUNTER_RETENTION). Живая строка недельного лимита выживает,
// а 200-часовая вычищается.
func TestAdminCleanupKeepsLiveWeeklyCounter(t *testing.T) {
	newTestDB(t)
	uid := insertUser(t, 555)
	now := time.Now().UTC()
	// Возраст 150ч: живая строка недельного окна (LIMIT_GROUP_CREATE_WEEK,
	// Truncate(168h) — возраст до 168ч).
	liveWeek := now.Add(-150 * time.Hour).Truncate(time.Microsecond)
	stale := now.Add(-200 * time.Hour).Truncate(time.Microsecond)
	for _, w := range []time.Time{liveWeek, stale} {
		if _, err := testPool.Exec(t.Context(),
			`INSERT INTO user_action_counters (user_id, action, window_start, count)
			 VALUES ($1, 'group_create_week', $2, 4)`, uid, w); err != nil {
			t.Fatalf("insert counter %v: %v", w, err)
		}
	}

	code, out := runAdmin(t, "cleanup")
	if code != 0 {
		t.Fatalf("cleanup exit = %d, want 0; output: %s", code, out)
	}
	// Обе строки в одном окне/действии — считаем по точному window_start.
	var live, gone int64
	if err := testPool.QueryRow(t.Context(),
		`SELECT count(*) FROM user_action_counters WHERE window_start = $1`, liveWeek).Scan(&live); err != nil {
		t.Fatalf("count live week row: %v", err)
	}
	if err := testPool.QueryRow(t.Context(),
		`SELECT count(*) FROM user_action_counters WHERE window_start = $1`, stale).Scan(&gone); err != nil {
		t.Fatalf("count stale row: %v", err)
	}
	if live != 1 {
		t.Errorf("live week counter row (150h old) = %d, want 1 (COUNTER_RETENTION must exceed 168h)", live)
	}
	if gone != 0 {
		t.Errorf("stale counter row (200h old) = %d, want 0 (purged)", gone)
	}
	if !strings.Contains(out, "Счётчиков вычищено: 1") {
		t.Errorf("cleanup output missing the purge report: %q", out)
	}
}

// list-groups (спека §2): таблица со слагом, названием, статусом, числом
// участников и датой создания — по всем неудалённым группам, включая pending
// (они не видны ни в одном /groups — супер-админу нужны именно они).
func TestAdminListGroups(t *testing.T) {
	newTestDB(t)
	uid := insertUser(t, 555)
	active := insertGroup(t, "ИКБО-33-21", uid, nil)
	if _, err := testPool.Exec(t.Context(),
		`UPDATE groups SET status='active' WHERE id=$1`, active); err != nil {
		t.Fatalf("activate group: %v", err)
	}
	insertGroup(t, "М8О-401Б-23", uid, nil) // pending
	// Участники активной группы: создатель + второй пользователь.
	other := insertUser(t, 556)
	for _, member := range []int64{uid, other} {
		if _, err := testPool.Exec(t.Context(),
			`INSERT INTO group_memberships (group_id, user_id, role) VALUES ($1, $2, 'member')`,
			active, member); err != nil {
			t.Fatalf("insert membership: %v", err)
		}
	}

	code, out := runAdmin(t, "list-groups")
	if code != 0 {
		t.Fatalf("list-groups exit = %d, want 0; output: %s", code, out)
	}
	for _, want := range []string{
		"ИКБО-33-21", "М8О-401Б-23", // оба слага
		"active", "pending", // статусы обеих групп
		"СЛАГ", "СТАТУС", "УЧАСТНИКИ", "СОЗДАНА", // заголовок таблицы
	} {
		if !strings.Contains(out, want) {
			t.Errorf("list-groups output missing %q:\n%s", want, out)
		}
	}

	// Число участников — из group_memberships: активная группа собрала двух,
	// pending — ни одного.
	lines := groupLines(out, "ИКБО-33-21", "М8О-401Б-23")
	if !strings.Contains(lines["ИКБО-33-21"], " 2 ") {
		t.Errorf("active group row does not show 2 members: %q", lines["ИКБО-33-21"])
	}
	if !strings.Contains(lines["М8О-401Б-23"], " 0 ") {
		t.Errorf("pending group row does not show 0 members: %q", lines["М8О-401Б-23"])
	}
}

// Фильтр по статусу сужает список, мусорный статус — ошибка использования (2),
// а не тихий показ всех групп.
func TestAdminListGroupsStatusFilterAndUsage(t *testing.T) {
	newTestDB(t)
	uid := insertUser(t, 555)
	insertGroup(t, "ИКБО-33-21", uid, nil)
	active := insertGroup(t, "М8О-401Б-23", uid, nil)
	if _, err := testPool.Exec(t.Context(),
		`UPDATE groups SET status='active' WHERE id=$1`, active); err != nil {
		t.Fatalf("activate group: %v", err)
	}

	code, out := runAdmin(t, "list-groups", "active")
	if code != 0 {
		t.Fatalf("list-groups active exit = %d, want 0; output: %s", code, out)
	}
	if !strings.Contains(out, "М8О-401Б-23") {
		t.Errorf("active filter dropped the active group:\n%s", out)
	}
	if strings.Contains(out, "ИКБО-33-21") {
		t.Errorf("active filter kept the pending group:\n%s", out)
	}

	if code, out = runAdmin(t, "list-groups", "frobnicate"); code != 2 {
		t.Errorf("list-groups frobnicate = %d, want 2 (usage); output: %s", code, out)
	}
	if code, out = runAdmin(t, "list-groups", "active", "extra"); code != 2 {
		t.Errorf("list-groups with a spare argument = %d, want 2; output: %s", code, out)
	}
}

// Удалённые группы в список не попадают: супер-админ смотрит живой инстанс.
func TestAdminListGroupsSkipsDeleted(t *testing.T) {
	newTestDB(t)
	uid := insertUser(t, 555)
	insertGroup(t, "ИКБО-33-21", uid, nil)
	gone := insertGroup(t, "М8О-401Б-23", uid, nil)
	if _, err := testPool.Exec(t.Context(),
		`UPDATE groups SET deleted_at = now() WHERE id=$1`, gone); err != nil {
		t.Fatalf("soft delete: %v", err)
	}

	code, out := runAdmin(t, "list-groups")
	if code != 0 {
		t.Fatalf("list-groups exit = %d, want 0; output: %s", code, out)
	}
	if !strings.Contains(out, "ИКБО-33-21") {
		t.Errorf("live group missing from the list:\n%s", out)
	}
	if strings.Contains(out, "М8О-401Б-23") {
		t.Errorf("soft-deleted group leaked into the list:\n%s", out)
	}
}

// Пустой инстанс — понятная строка, а не пустой вывод и не заголовок таблицы.
func TestAdminListGroupsEmpty(t *testing.T) {
	newTestDB(t)

	code, out := runAdmin(t, "list-groups")
	if code != 0 {
		t.Fatalf("list-groups exit = %d, want 0; output: %s", code, out)
	}
	if !strings.Contains(out, "Групп нет") {
		t.Errorf("empty list output = %q, want the empty-state line", out)
	}
}

// groupLines — строки вывода, содержащие заданный слаг: точное совпадение по
// подстроке в тесте хрупко (слаг есть и в заголовке), поэтому берём строку с
// названием группы.
func groupLines(out string, slugs ...string) map[string]string {
	res := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		for _, slug := range slugs {
			if strings.HasPrefix(strings.TrimSpace(line), slug) {
				res[slug] = line
			}
		}
	}
	return res
}

// nowInsertedGroup — pending-группа без TTL (claim_expires_at NULL): cleanup её
// не трогает (отбор идёт по claim_expires_at).
func nowInsertedGroup(t *testing.T, uid int64) int64 {
	t.Helper()
	var id int64
	err := testPool.QueryRow(t.Context(),
		`INSERT INTO groups (slug, slug_norm, title, status, created_by)
		 VALUES ('ОВФ-99', 'ОВФ-99', 'Без TTL', 'pending', $1) RETURNING id`, uid).Scan(&id)
	if err != nil {
		t.Fatalf("insert group without TTL: %v", err)
	}
	return id
}
