package repo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sauron/deadliner/internal/domain"
)

// insertClaimGroup создаёт pending-группу с заданным слагом.
func insertClaimGroup(t *testing.T, pool *pgxpool.Pool, slug string, owner int64) *domain.Group {
	t.Helper()
	g := newGroup(slug, "claim", owner, nil)
	if err := NewGroups(pool).Create(context.Background(), g); err != nil {
		t.Fatalf("create group: %v", err)
	}
	return g
}

func TestClaimsCreateGetActive(t *testing.T) {
	pool := newTestDB(t)
	ctx := context.Background()
	repo := NewClaims(pool)
	owner := insertUser(t, pool, 7001)
	g := insertClaimGroup(t, pool, "М8О-701-23", owner)

	// Postgres timestamptz хранит микросекунды: обрезаем фикстуры, иначе
	// равноценность времени проверялась бы на наносекундном хвосте.
	now := time.Now().UTC().Truncate(time.Microsecond)
	active := &domain.ClaimCode{
		GroupID:   g.ID,
		CodeHash:  "hash-active",
		ChatID:    -100700,
		MessageID: 42,
		CreatedBy: owner,
		ExpiresAt: now.Add(10 * time.Minute),
	}
	if err := repo.Create(ctx, active); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if active.ID == 0 {
		t.Fatal("Create did not set ID")
	}

	got, err := repo.GetActiveByGroup(ctx, g.ID, now)
	if err != nil {
		t.Fatalf("GetActiveByGroup: %v", err)
	}
	if got.ID != active.ID || got.CodeHash != "hash-active" || got.ChatID != -100700 ||
		got.MessageID != 42 || got.CreatedBy != owner || got.UsedAt != nil {
		t.Errorf("GetActiveByGroup = %+v", got)
	}
	if !got.ExpiresAt.Equal(active.ExpiresAt) {
		t.Errorf("ExpiresAt = %v, want %v", got.ExpiresAt, active.ExpiresAt)
	}

	list, err := repo.ListActiveByGroup(ctx, g.ID, now)
	if err != nil {
		t.Fatalf("ListActiveByGroup: %v", err)
	}
	if len(list) != 1 || list[0].ID != active.ID {
		t.Errorf("ListActiveByGroup = %+v, want the single active code", list)
	}
}

// Истёкший код невидим: сравнение expires_at > now отсекает его, как и
// отсутствующую строку — обе ситуации ErrNotFound.
func TestClaimsExpiredHidden(t *testing.T) {
	pool := newTestDB(t)
	ctx := context.Background()
	repo := NewClaims(pool)
	owner := insertUser(t, pool, 7101)
	g := insertClaimGroup(t, pool, "М8О-702-23", owner)

	now := time.Now().UTC()
	// Граница: expires_at ровно now — уже не активен (фильтр строгий).
	for _, exp := range []time.Time{now.Add(-time.Second), now} {
		code := &domain.ClaimCode{
			GroupID: g.ID, CodeHash: "h", ChatID: -1, MessageID: 1,
			CreatedBy: owner, ExpiresAt: exp,
		}
		if err := repo.Create(ctx, code); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if _, err := repo.GetActiveByGroup(ctx, g.ID, now); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("GetActiveByGroup(expires_at=%v) = %v, want ErrNotFound", exp, err)
		}
		list, err := repo.ListActiveByGroup(ctx, g.ID, now)
		if err != nil {
			t.Fatalf("ListActiveByGroup: %v", err)
		}
		if len(list) != 0 {
			t.Errorf("ListActiveByGroup = %+v, want empty", list)
		}
	}

	// Несуществующая группа — тот же ErrNotFound.
	if _, err := repo.GetActiveByGroup(ctx, 999999, now); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("GetActiveByGroup(unknown group) = %v, want ErrNotFound", err)
	}
}

// Гонка Confirm: MarkUsed — условный UPDATE, побеждает ровно один вызов.
func TestClaimsMarkUsedConditional(t *testing.T) {
	pool := newTestDB(t)
	ctx := context.Background()
	repo := NewClaims(pool)
	owner := insertUser(t, pool, 7201)
	g := insertClaimGroup(t, pool, "М8О-703-23", owner)

	now := time.Now().UTC().Truncate(time.Microsecond)
	code := &domain.ClaimCode{
		GroupID: g.ID, CodeHash: "h", ChatID: -1, MessageID: 1,
		CreatedBy: owner, ExpiresAt: now.Add(10 * time.Minute),
	}
	if err := repo.Create(ctx, code); err != nil {
		t.Fatalf("Create: %v", err)
	}

	used := now.Add(time.Minute)
	if err := repo.MarkUsed(ctx, code.ID, used); err != nil {
		t.Fatalf("first MarkUsed: %v", err)
	}
	if err := repo.MarkUsed(ctx, code.ID, used); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("second MarkUsed = %v, want ErrNotFound", err)
	}
	if _, err := repo.GetActiveByGroup(ctx, g.ID, now); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("GetActiveByGroup after MarkUsed = %v, want ErrNotFound", err)
	}
	if err := repo.MarkUsed(ctx, 999999, used); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("MarkUsed(unknown id) = %v, want ErrNotFound", err)
	}

	// Погашенный код остался в таблице с used_at (проверяем явным SELECT).
	var usedAt *time.Time
	if err := pool.QueryRow(ctx,
		`SELECT used_at FROM claim_codes WHERE id = $1`, code.ID).Scan(&usedAt); err != nil {
		t.Fatalf("select used_at: %v", err)
	}
	if usedAt == nil || !usedAt.Equal(used) {
		t.Errorf("used_at = %v, want %v", usedAt, used)
	}
}

// RevokeActiveByGroup гасит все действующие коды группы и не трогает чужие.
func TestClaimsRevokeActiveByGroup(t *testing.T) {
	pool := newTestDB(t)
	ctx := context.Background()
	repo := NewClaims(pool)
	owner := insertUser(t, pool, 7301)
	g1 := insertClaimGroup(t, pool, "М8О-704-23", owner)
	g2 := insertClaimGroup(t, pool, "М8О-705-23", owner)

	now := time.Now().UTC()
	// Два действующих кода в первой группе (StartClaim отзывает предыдущий, но
	// гонка может оставить два) и один во второй.
	for i, g := range []*domain.Group{g1, g1, g2} {
		code := &domain.ClaimCode{
			GroupID: g.ID, CodeHash: "h", ChatID: -1, MessageID: int64(i + 1),
			CreatedBy: owner, ExpiresAt: now.Add(10 * time.Minute),
		}
		if err := repo.Create(ctx, code); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	if err := repo.RevokeActiveByGroup(ctx, g1.ID, now); err != nil {
		t.Fatalf("RevokeActiveByGroup: %v", err)
	}
	if _, err := repo.GetActiveByGroup(ctx, g1.ID, now); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("GetActiveByGroup(g1) after revoke = %v, want ErrNotFound", err)
	}
	if _, err := repo.GetActiveByGroup(ctx, g2.ID, now); err != nil {
		t.Errorf("GetActiveByGroup(g2) = %v, want the untouched code", err)
	}
	// Идемпотентность: повторный revoke без активных кодов — без ошибки.
	if err := repo.RevokeActiveByGroup(ctx, g1.ID, now); err != nil {
		t.Errorf("second RevokeActiveByGroup = %v, want nil", err)
	}
}

var _ domain.ClaimRepo = (*claimsRepo)(nil)
