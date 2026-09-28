package repo

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/sauron/deadliner/internal/domain"
	"github.com/sauron/deadliner/internal/platform/db"
)

// testPool is the shared pool over a single postgres container started in
// TestMain; newTestDB truncates all tables so tests stay isolated.
var testPool *pgxpool.Pool

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

	code := m.Run()

	pool.Close()
	_ = ctr.Terminate(context.Background())
	os.Exit(code)
}

// newTestDB truncates every table and returns the shared pool.
func newTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := testPool.Exec(ctx, `TRUNCATE
		users, groups, chat_bindings, group_memberships, deadlines, reminders,
		invites, claim_codes, user_action_counters, sessions, outbox_messages,
		audit_log CASCADE`)
	if err != nil {
		t.Fatalf("truncate: %v", err)
	}
	return testPool
}

// insertUser creates a bare user row and returns its ID.
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

// newGroup builds a domain.Group fixture with raw (non-normalized) slug,
// pending status and a 72h claim window — the repo must re-normalize Slug.
func newGroup(slug, title string, createdBy int64, claimExpiresAt *time.Time) *domain.Group {
	return &domain.Group{
		Slug:           slug,
		Title:          title,
		Status:         domain.GroupStatusPending,
		CreatedBy:      createdBy,
		DefaultPresets: []time.Duration{7 * 24 * time.Hour, 3 * 24 * time.Hour, 24 * time.Hour},
		ClaimExpiresAt: claimExpiresAt,
	}
}
