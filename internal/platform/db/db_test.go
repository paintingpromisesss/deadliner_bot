package db_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/sauron/deadliner/internal/platform/db"
)

func startPostgres(t *testing.T) (context.Context, string) {
	t.Helper()
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
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() {
		_ = ctr.Terminate(context.Background())
	})

	url, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	return ctx, url
}

func TestMigrateUpDown(t *testing.T) {
	ctx, url := startPostgres(t)

	if err := db.RunUp(ctx, url); err != nil {
		t.Fatalf("RunUp: %v", err)
	}
	// Second Up must be idempotent (no error, no change).
	if err := db.RunUp(ctx, url); err != nil {
		t.Fatalf("RunUp (second time): %v", err)
	}

	pool, err := db.Connect(ctx, url, 4)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer pool.Close()

	wantTables := []string{
		"users", "groups", "chat_bindings", "group_memberships",
		"deadlines", "reminders", "invites",
		"user_action_counters", "sessions", "outbox_messages", "audit_log",
	}
	for _, table := range wantTables {
		var exists bool
		err := pool.QueryRow(ctx,
			`SELECT EXISTS (
				SELECT 1 FROM information_schema.tables
				WHERE table_schema = 'public' AND table_name = $1)`, table).Scan(&exists)
		if err != nil {
			t.Fatalf("check table %s: %v", table, err)
		}
		if !exists {
			t.Errorf("table %s does not exist after Up", table)
		}
	}

	// groups.slug_norm must reject duplicates (unique violation).
	_, err = pool.Exec(ctx,
		`INSERT INTO users (telegram_id, username) VALUES (111, 'test')`)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	var userID int64
	if err := pool.QueryRow(ctx, `SELECT id FROM users WHERE telegram_id = 111`).Scan(&userID); err != nil {
		t.Fatalf("select user: %v", err)
	}

	_, err = pool.Exec(ctx,
		`INSERT INTO groups (slug, slug_norm, title, created_by)
		 VALUES ('MAI-2026', upper('MAI-2026'), 'Group A', $1)`, userID)
	if err != nil {
		t.Fatalf("insert first group: %v", err)
	}
	_, err = pool.Exec(ctx,
		`INSERT INTO groups (slug, slug_norm, title, created_by)
		 VALUES ('mai-2026', upper('mai-2026'), 'Group B', $1)`, userID)
	if err == nil {
		t.Fatal("duplicate slug_norm accepted, want unique violation")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		t.Errorf("duplicate slug_norm error = %v, want unique_violation (23505)", err)
	}

	// deadlines CHECK: group_id and owner_user_id both NULL must be rejected.
	_, err = pool.Exec(ctx,
		`INSERT INTO deadlines (group_id, owner_user_id, title, due_at, created_by)
		 VALUES (NULL, NULL, 'bad', now(), $1)`, userID)
	if err == nil {
		t.Fatal("deadline with both group_id and owner_user_id NULL accepted, want check violation")
	}
	pgErr = nil
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
		t.Errorf("both-NULL deadline error = %v, want check_violation (23514)", err)
	}

	// chat_bindings: UNIQUE NULLS NOT DISTINCT (chat_id, message_thread_id) —
	// an ordinary chat (thread NULL) must not bind to two groups.
	var groupID int64
	if err := pool.QueryRow(ctx, `SELECT id FROM groups WHERE slug_norm = 'MAI-2026'`).Scan(&groupID); err != nil {
		t.Fatalf("select group: %v", err)
	}
	_, err = pool.Exec(ctx,
		`INSERT INTO groups (slug, slug_norm, title, created_by)
		 VALUES ('MAI-2027', 'MAI-2027', 'Group C', $1)`, userID)
	if err != nil {
		t.Fatalf("insert second group: %v", err)
	}
	var groupID2 int64
	if err := pool.QueryRow(ctx, `SELECT id FROM groups WHERE slug_norm = 'MAI-2027'`).Scan(&groupID2); err != nil {
		t.Fatalf("select second group: %v", err)
	}

	_, err = pool.Exec(ctx,
		`INSERT INTO chat_bindings (group_id, chat_id, message_thread_id, bound_by)
		 VALUES ($1, -100, NULL, $2)`, groupID, userID)
	if err != nil {
		t.Fatalf("insert first chat binding: %v", err)
	}
	_, err = pool.Exec(ctx,
		`INSERT INTO chat_bindings (group_id, chat_id, message_thread_id, bound_by)
		 VALUES ($1, -100, NULL, $2)`, groupID2, userID)
	if err == nil {
		t.Fatal("duplicate (chat_id, NULL thread) binding accepted, want unique violation")
	}
	pgErr = nil
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		t.Errorf("duplicate chat binding error = %v, want unique_violation (23505)", err)
	}

	// reminders: partial unique (deadline_id, kind, offset_minutes) WHERE
	// offset_minutes IS NOT NULL must reject duplicates.
	_, err = pool.Exec(ctx,
		`INSERT INTO deadlines (group_id, owner_user_id, title, due_at, created_by)
		 VALUES (NULL, $1, 'personal', now() + interval '1 day', $1)`, userID)
	if err != nil {
		t.Fatalf("insert personal deadline: %v", err)
	}
	var deadlineID int64
	if err := pool.QueryRow(ctx,
		`SELECT id FROM deadlines WHERE title = 'personal'`).Scan(&deadlineID); err != nil {
		t.Fatalf("select deadline: %v", err)
	}

	for i := 0; i < 2; i++ {
		_, err = pool.Exec(ctx,
			`INSERT INTO reminders (deadline_id, kind, offset_minutes, fire_at)
			 VALUES ($1, 'preset', 1440, now() + interval '1 day')`, deadlineID)
		if i == 0 {
			if err != nil {
				t.Fatalf("insert first reminder: %v", err)
			}
			continue
		}
		if err == nil {
			t.Fatal("duplicate (deadline_id, kind, offset_minutes) reminder accepted, want unique violation")
		}
		pgErr = nil
		if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
			t.Errorf("duplicate reminder error = %v, want unique_violation (23505)", err)
		}
	}

	if err := db.RunDown(ctx, url); err != nil {
		t.Fatalf("RunDown: %v", err)
	}

	var remaining int
	err = pool.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.tables
		 WHERE table_schema = 'public'
		   AND table_type = 'BASE TABLE'
		   AND table_name <> 'schema_migrations'`).Scan(&remaining)
	if err != nil {
		t.Fatalf("count tables after Down: %v", err)
	}
	if remaining != 0 {
		t.Errorf("after Down %d tables remain, want 0", remaining)
	}
}

// minor 5: Down миграции 000003 не должен падать на непустой таблице dm_dup —
// пересоздание полного unique-индекса (deadline_id, fire_at, kind) иначе
// упирается в дубликаты дочерних строк.
func TestMigrateDownWithDMDupChildren(t *testing.T) {
	ctx, url := startPostgres(t)

	if err := db.RunUp(ctx, url); err != nil {
		t.Fatalf("RunUp: %v", err)
	}
	pool, err := db.Connect(ctx, url, 4)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer pool.Close()

	var userID, deadlineID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (telegram_id, username) VALUES (222, 'dm') RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO deadlines (owner_user_id, title, due_at, created_by)
		 VALUES ($1, 'dm', now() + interval '1 day', $1) RETURNING id`, userID).Scan(&deadlineID); err != nil {
		t.Fatalf("insert deadline: %v", err)
	}
	// Три dm_dup-ребёнка одного fan-out: ОДИН и тот же fire_at (как их пишет
	// воркер: fire_at = момент fan-out), то есть именно те строки, на которых
	// полный unique-индекс (deadline_id, fire_at, kind) падал бы. Отдельные
	// INSERT с now() дали бы разные timestamptz и не воспроизвели бы коллизию —
	// поэтому время фиксируется явно и передаётся всем трём строкам.
	fanoutAt := time.Now().UTC().Truncate(time.Microsecond)
	for _, target := range []int64{1, 2, 3} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO reminders (deadline_id, kind, target_user_id, fire_at)
			 VALUES ($1, 'dm_dup', $2, $3)`, deadlineID, target, fanoutAt); err != nil {
			t.Fatalf("insert dm_dup child: %v", err)
		}
	}
	// Плюс два дубля ОДНОЙ цели из разных fan-out (разный fire_at): их должен
	// схлопнуть откат 000004 перед восстановлением строгого (deadline_id,
	// target_user_id) индекса.
	for i := range 2 {
		if _, err := pool.Exec(ctx,
			`INSERT INTO reminders (deadline_id, kind, target_user_id, fire_at)
			 VALUES ($1, 'dm_dup', 4, $2)`, deadlineID,
			fanoutAt.Add(time.Duration(i+1)*time.Minute)); err != nil {
			t.Fatalf("insert multi-fanout dm_dup child: %v", err)
		}
	}

	if err := db.RunDown(ctx, url); err != nil {
		t.Fatalf("RunDown with dm_dup children: %v", err)
	}
}

func TestConnectRejectsNonPositivePoolMax(t *testing.T) {
	ctx := context.Background()
	for _, poolMax := range []int32{0, -1} {
		pool, err := db.Connect(ctx, "postgres://deadliner:deadliner@127.0.0.1:1/deadliner?sslmode=disable", poolMax)
		if err == nil {
			pool.Close()
			t.Fatalf("Connect(poolMax=%d) succeeded, want error", poolMax)
		}
	}
}

func TestConnectFailsOnUnreachableHost(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := db.Connect(ctx, "postgres://deadliner:deadliner@127.0.0.1:1/deadliner?sslmode=disable", 2)
	if err == nil {
		pool.Close()
		t.Fatal("Connect to unreachable host succeeded, want error")
	}
}
