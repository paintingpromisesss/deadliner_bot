package repo

import (
	"context"
	"errors"
	"testing"

	"github.com/sauron/deadliner/internal/domain"
)

func boolPtr(b bool) *bool { return &b }

func TestMembershipsUpsertGetLists(t *testing.T) {
	pool := newTestDB(t)
	ctx := context.Background()
	groups := NewGroups(pool)
	repo := NewMemberships(pool)
	owner := insertUser(t, pool, 5001)
	member := insertUser(t, pool, 5002)

	g := newGroup("М8О-701-23", "mem", owner, nil)
	if err := groups.Create(ctx, g); err != nil {
		t.Fatalf("Create group: %v", err)
	}

	m := &domain.Membership{GroupID: g.ID, UserID: owner, Role: domain.RoleAdmin}
	if err := repo.Upsert(ctx, m); err != nil {
		t.Fatalf("Upsert owner: %v", err)
	}
	m2 := &domain.Membership{GroupID: g.ID, UserID: member, Role: domain.RoleMember}
	if err := repo.Upsert(ctx, m2); err != nil {
		t.Fatalf("Upsert member: %v", err)
	}

	got, err := repo.Get(ctx, g.ID, owner)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Role != domain.RoleAdmin || got.DMNotify != nil {
		t.Errorf("Get = %+v, want admin / nil dm_notify", got)
	}

	if _, err := repo.Get(ctx, g.ID, 999999); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Get missing = %v, want ErrNotFound", err)
	}

	byGroup, err := repo.ListByGroup(ctx, g.ID)
	if err != nil {
		t.Fatalf("ListByGroup: %v", err)
	}
	if len(byGroup) != 2 {
		t.Errorf("ListByGroup = %d rows, want 2", len(byGroup))
	}

	byUser, err := repo.ListByUser(ctx, member)
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	if len(byUser) != 1 || byUser[0].GroupID != g.ID {
		t.Errorf("ListByUser = %+v, want one membership in %d", byUser, g.ID)
	}

	n, err := repo.CountAdmins(ctx, g.ID)
	if err != nil {
		t.Fatalf("CountAdmins: %v", err)
	}
	if n != 1 {
		t.Errorf("CountAdmins = %d, want 1", n)
	}
}

func TestMembershipsUpsertKeepsExistingRole(t *testing.T) {
	pool := newTestDB(t)
	ctx := context.Background()
	groups := NewGroups(pool)
	repo := NewMemberships(pool)
	owner := insertUser(t, pool, 6001)

	g := newGroup("М8О-801-23", "roles", owner, nil)
	if err := groups.Create(ctx, g); err != nil {
		t.Fatalf("Create group: %v", err)
	}

	if err := repo.Upsert(ctx, &domain.Membership{GroupID: g.ID, UserID: owner, Role: domain.RoleAdmin}); err != nil {
		t.Fatalf("Upsert admin: %v", err)
	}
	// Re-upsert without an explicit role change must keep admin.
	if err := repo.Upsert(ctx, &domain.Membership{GroupID: g.ID, UserID: owner}); err != nil {
		t.Fatalf("Upsert again: %v", err)
	}
	got, err := repo.Get(ctx, g.ID, owner)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Role != domain.RoleAdmin {
		t.Errorf("role = %q, want admin kept on re-upsert", got.Role)
	}

	if err := repo.SetRole(ctx, g.ID, owner, domain.RoleMember); err != nil {
		t.Fatalf("SetRole: %v", err)
	}
	got, err = repo.Get(ctx, g.ID, owner)
	if err != nil {
		t.Fatalf("Get after SetRole: %v", err)
	}
	if got.Role != domain.RoleMember {
		t.Errorf("role = %q, want member", got.Role)
	}
	if err := repo.SetRole(ctx, g.ID, 999999, domain.RoleMember); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("SetRole missing = %v, want ErrNotFound", err)
	}
}

func TestMembershipsDMNotify(t *testing.T) {
	pool := newTestDB(t)
	ctx := context.Background()
	groups := NewGroups(pool)
	repo := NewMemberships(pool)
	owner := insertUser(t, pool, 7001)

	g := newGroup("М8О-901-23", "dm", owner, nil)
	if err := groups.Create(ctx, g); err != nil {
		t.Fatalf("Create group: %v", err)
	}
	if err := repo.Upsert(ctx, &domain.Membership{GroupID: g.ID, UserID: owner, Role: domain.RoleMember}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	if err := repo.SetDMNotify(ctx, g.ID, owner, boolPtr(true)); err != nil {
		t.Fatalf("SetDMNotify true: %v", err)
	}
	got, err := repo.Get(ctx, g.ID, owner)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.DMNotify == nil || *got.DMNotify != true {
		t.Errorf("dm_notify = %v, want true", got.DMNotify)
	}

	if err := repo.SetDMNotify(ctx, g.ID, owner, nil); err != nil {
		t.Fatalf("SetDMNotify nil: %v", err)
	}
	got, err = repo.Get(ctx, g.ID, owner)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.DMNotify != nil {
		t.Errorf("dm_notify = %v, want nil", *got.DMNotify)
	}
	if err := repo.SetDMNotify(ctx, g.ID, 999999, boolPtr(true)); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("SetDMNotify missing = %v, want ErrNotFound", err)
	}
}

func TestMembershipsDelete(t *testing.T) {
	pool := newTestDB(t)
	ctx := context.Background()
	groups := NewGroups(pool)
	repo := NewMemberships(pool)
	owner := insertUser(t, pool, 8001)

	g := newGroup("М8О-911-23", "del", owner, nil)
	if err := groups.Create(ctx, g); err != nil {
		t.Fatalf("Create group: %v", err)
	}
	if err := repo.Upsert(ctx, &domain.Membership{GroupID: g.ID, UserID: owner, Role: domain.RoleMember}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := repo.Delete(ctx, g.ID, owner); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.Get(ctx, g.ID, owner); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Get after Delete = %v, want ErrNotFound", err)
	}
	if err := repo.Delete(ctx, g.ID, 999999); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Delete missing = %v, want ErrNotFound", err)
	}
}

var _ domain.MembershipRepo = (*membershipsRepo)(nil)
