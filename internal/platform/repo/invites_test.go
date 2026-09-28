package repo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sauron/deadliner/internal/domain"
)

func TestMembershipsGuardedDemoteAndRemove(t *testing.T) {
	pool := newTestDB(t)
	ctx := context.Background()
	groups := NewGroups(pool)
	repo := NewMemberships(pool)
	owner := insertUser(t, pool, 11001)
	second := insertUser(t, pool, 11002)

	g := newGroup("ЗАЩ-111-1", "guard", owner, nil)
	if err := groups.Create(ctx, g); err != nil {
		t.Fatalf("Create group: %v", err)
	}
	for _, uid := range []int64{owner, second} {
		if err := repo.Upsert(ctx, &domain.Membership{GroupID: g.ID, UserID: uid, Role: domain.RoleAdmin}); err != nil {
			t.Fatalf("Upsert admin %d: %v", uid, err)
		}
	}

	// Два админа: первая демotion проходит, вторая (последний админ) — конфликт.
	if err := repo.DemoteIfNotLastAdmin(ctx, g.ID, second); err != nil {
		t.Fatalf("DemoteIfNotLastAdmin (2 admins): %v", err)
	}
	err := repo.DemoteIfNotLastAdmin(ctx, g.ID, owner)
	if !errors.Is(err, domain.ErrConflict) {
		t.Errorf("DemoteIfNotLastAdmin last admin = %v, want ErrConflict", err)
	}
	got, err := repo.Get(ctx, g.ID, owner)
	if err != nil || got.Role != domain.RoleAdmin {
		t.Errorf("last admin rolled back? role=%v err=%v", got.Role, err)
	}

	// Демotion не-админа → ErrValidation.
	if err := repo.DemoteIfNotLastAdmin(ctx, g.ID, second); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("demote non-admin = %v, want ErrValidation", err)
	}

	// RemoveIfNotLastAdmin: member удаляется, последний админ — конфликт.
	if err := repo.RemoveIfNotLastAdmin(ctx, g.ID, second); err != nil {
		t.Fatalf("RemoveIfNotLastAdmin (member): %v", err)
	}
	if err := repo.RemoveIfNotLastAdmin(ctx, g.ID, owner); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("RemoveIfNotLastAdmin last admin = %v, want ErrConflict", err)
	}
	if _, err := repo.Get(ctx, g.ID, owner); err != nil {
		t.Errorf("last admin membership gone: %v", err)
	}

	// Отсутствующая строка → ErrNotFound.
	if err := repo.RemoveIfNotLastAdmin(ctx, g.ID, 999999); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("RemoveIfNotLastAdmin missing = %v, want ErrNotFound", err)
	}
	if err := repo.DemoteIfNotLastAdmin(ctx, g.ID, 999999); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("DemoteIfNotLastAdmin missing = %v, want ErrNotFound", err)
	}
}

func TestInvitesIncrementUsedConditional(t *testing.T) {
	pool := newTestDB(t)
	ctx := context.Background()
	groups := NewGroups(pool)
	invites := NewInvites(pool)
	owner := insertUser(t, pool, 12001)

	g := newGroup("ИНВ-222-1", "inv", owner, nil)
	if err := groups.Create(ctx, g); err != nil {
		t.Fatalf("Create group: %v", err)
	}
	inv := &domain.Invite{
		GroupID: g.ID, Code: "ABCD2345", Role: domain.RoleMember,
		MaxUses: 2, CreatedBy: owner, ExpiresAt: time.Now().UTC().Add(time.Hour),
	}
	if err := invites.Create(ctx, inv); err != nil {
		t.Fatalf("Create invite: %v", err)
	}

	if err := invites.IncrementUsed(ctx, inv.ID); err != nil {
		t.Fatalf("IncrementUsed #1: %v", err)
	}
	if err := invites.IncrementUsed(ctx, inv.ID); err != nil {
		t.Fatalf("IncrementUsed #2: %v", err)
	}
	// Третье использование — лимит исчерпан → ErrConflict (условие в SQL).
	if err := invites.IncrementUsed(ctx, inv.ID); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("IncrementUsed exhausted = %v, want ErrConflict", err)
	}
	got, err := invites.GetByCode(ctx, "ABCD2345")
	if err != nil || got.UsedCount != 2 {
		t.Errorf("used_count = %v (err %v), want 2", got.UsedCount, err)
	}

	// Отозванный инвайт → ErrConflict.
	revoked := &domain.Invite{
		GroupID: g.ID, Code: "REVK2345", Role: domain.RoleMember,
		MaxUses: 5, CreatedBy: owner, ExpiresAt: time.Now().UTC().Add(time.Hour),
	}
	if err := invites.Create(ctx, revoked); err != nil {
		t.Fatalf("Create revoked invite: %v", err)
	}
	if err := invites.Revoke(ctx, g.ID, "REVK2345"); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if err := invites.IncrementUsed(ctx, revoked.ID); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("IncrementUsed revoked = %v, want ErrConflict", err)
	}

	// Истёкший инвайт → ErrConflict.
	expired := &domain.Invite{
		GroupID: g.ID, Code: "EXPR2345", Role: domain.RoleMember,
		MaxUses: 5, CreatedBy: owner, ExpiresAt: time.Now().UTC().Add(-time.Hour),
	}
	if err := invites.Create(ctx, expired); err != nil {
		t.Fatalf("Create expired invite: %v", err)
	}
	if err := invites.IncrementUsed(ctx, expired.ID); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("IncrementUsed expired = %v, want ErrConflict", err)
	}

	// max_uses = -1 (без лимита) — инкремент всегда проходит.
	unlimited := &domain.Invite{
		GroupID: g.ID, Code: "UNLM2345", Role: domain.RoleMember,
		MaxUses: -1, CreatedBy: owner, ExpiresAt: time.Now().UTC().Add(time.Hour),
	}
	if err := invites.Create(ctx, unlimited); err != nil {
		t.Fatalf("Create unlimited invite: %v", err)
	}
	for i := 0; i < 5; i++ {
		if err := invites.IncrementUsed(ctx, unlimited.ID); err != nil {
			t.Fatalf("IncrementUsed unlimited #%d: %v", i+1, err)
		}
	}

	// Несуществующий id → ErrConflict (нулевое число строк неотличимо от условий).
	if err := invites.IncrementUsed(ctx, 999999); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("IncrementUsed missing = %v, want ErrConflict", err)
	}
}

var _ domain.InviteRepo = (*invitesRepo)(nil)
