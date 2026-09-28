package db_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

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
		"deadlines", "reminders", "invites", "claim_codes",
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

func TestConnectFailsOnUnreachableHost(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := db.Connect(ctx, "postgres://deadliner:deadliner@127.0.0.1:1/deadliner?sslmode=disable", 2)
	if err == nil {
		pool.Close()
		t.Fatal("Connect to unreachable host succeeded, want error")
	}
}
