package repo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sauron/deadliner/internal/domain"
)

func TestGroupsCreateAndGet(t *testing.T) {
	pool := newTestDB(t)
	ctx := context.Background()
	repo := NewGroups(pool)
	userID := insertUser(t, pool, 1001)

	claimExpires := time.Now().UTC().Add(72 * time.Hour).Truncate(time.Microsecond)
	g := newGroup("М8О-401Б-23", "Моя группа", userID, &claimExpires)
	if err := repo.Create(ctx, g); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if g.ID == 0 {
		t.Fatal("Create did not set ID")
	}

	got, err := repo.GetByID(ctx, g.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Slug != "М8О-401Б-23" || got.SlugNorm != "М8О-401Б-23" {
		t.Errorf("slug=%q slug_norm=%q, want М8О-401Б-23", got.Slug, got.SlugNorm)
	}
	if got.Title != "Моя группа" || got.Status != domain.GroupStatusPending {
		t.Errorf("title=%q status=%q", got.Title, got.Status)
	}
	if got.CreatedBy != userID || !got.ClaimExpiresAt.Equal(claimExpires) {
		t.Errorf("created_by=%d claim_expires_at=%v, want %d / %v",
			got.CreatedBy, got.ClaimExpiresAt, userID, claimExpires)
	}
	if len(got.DefaultPresets) != 3 || got.DefaultPresets[0] != 7*24*time.Hour {
		t.Errorf("default_presets=%v", got.DefaultPresets)
	}

	// Defensive re-normalization: lowercase raw slug must be stored uppercase.
	g2 := newGroup("м8о-402б-23", "Lower", userID, nil)
	if err := repo.Create(ctx, g2); err != nil {
		t.Fatalf("Create lowercase slug: %v", err)
	}
	got2, err := repo.GetByID(ctx, g2.ID)
	if err != nil {
		t.Fatalf("GetByID 2: %v", err)
	}
	if got2.SlugNorm != "М8О-402Б-23" {
		t.Errorf("slug_norm=%q, want defensively normalized М8О-402Б-23", got2.SlugNorm)
	}
}

func TestGroupsCreateConflictNormalizesSlug(t *testing.T) {
	pool := newTestDB(t)
	ctx := context.Background()
	repo := NewGroups(pool)
	userID := insertUser(t, pool, 1002)

	if err := repo.Create(ctx, newGroup("М8О-401Б-23", "A", userID, nil)); err != nil {
		t.Fatalf("Create first: %v", err)
	}
	err := repo.Create(ctx, newGroup("м8о-401б-23", "B", userID, nil))
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("Create duplicate-after-normalization = %v, want ErrConflict", err)
	}
}

func TestGroupsGetBySlugNorm(t *testing.T) {
	pool := newTestDB(t)
	ctx := context.Background()
	repo := NewGroups(pool)
	userID := insertUser(t, pool, 1003)

	if err := repo.Create(ctx, newGroup("ИКБО-33-21", "Икбо", userID, nil)); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := repo.GetBySlugNorm(ctx, "ИКБО-33-21")
	if err != nil {
		t.Fatalf("GetBySlugNorm: %v", err)
	}
	if got.Title != "Икбо" {
		t.Errorf("title=%q", got.Title)
	}
	if _, err := repo.GetBySlugNorm(ctx, "НЕТ-99"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("GetBySlugNorm missing = %v, want ErrNotFound", err)
	}
}

func TestGroupsGetByIDSoftDeleted(t *testing.T) {
	pool := newTestDB(t)
	ctx := context.Background()
	repo := NewGroups(pool)
	userID := insertUser(t, pool, 1004)

	g := newGroup("М8О-111-23", "Doomed", userID, nil)
	if err := repo.Create(ctx, g); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := repo.SoftDelete(ctx, g.ID); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}
	if _, err := repo.GetByID(ctx, g.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("GetByID after soft delete = %v, want ErrNotFound", err)
	}
	if err := repo.SoftDelete(ctx, 999999); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("SoftDelete missing = %v, want ErrNotFound", err)
	}
}

func TestGroupsSetStatusAndUpdate(t *testing.T) {
	pool := newTestDB(t)
	ctx := context.Background()
	repo := NewGroups(pool)
	userID := insertUser(t, pool, 1005)

	g := newGroup("М8О-123-23", "Before", userID, nil)
	if err := repo.Create(ctx, g); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := repo.SetStatus(ctx, g.ID, domain.GroupStatusActive); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	got, err := repo.GetByID(ctx, g.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Status != domain.GroupStatusActive {
		t.Errorf("status=%q, want active", got.Status)
	}

	got.Title = "After"
	got.DefaultPresets = []time.Duration{30 * time.Minute, time.Hour}
	if err := repo.Update(ctx, got); err != nil {
		t.Fatalf("Update: %v", err)
	}
	updated, err := repo.GetByID(ctx, g.ID)
	if err != nil {
		t.Fatalf("GetByID after update: %v", err)
	}
	if updated.Title != "After" {
		t.Errorf("title=%q, want After", updated.Title)
	}
	if len(updated.DefaultPresets) != 2 || updated.DefaultPresets[0] != 30*time.Minute {
		t.Errorf("default_presets=%v, want [30m 1h]", updated.DefaultPresets)
	}
	if !updated.UpdatedAt.After(updated.CreatedAt) && !updated.UpdatedAt.Equal(updated.CreatedAt) {
		t.Errorf("updated_at=%v not >= created_at=%v", updated.UpdatedAt, updated.CreatedAt)
	}

	if err := repo.Update(ctx, &domain.Group{ID: 999999, Title: "X"}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Update missing = %v, want ErrNotFound", err)
	}
}

func TestGroupsSearchByPrefix(t *testing.T) {
	pool := newTestDB(t)
	ctx := context.Background()
	groups := NewGroups(pool)
	owner := insertUser(t, pool, 2001)
	other := insertUser(t, pool, 2002)

	mk := func(t *testing.T, slug, title string, by int64, status domain.GroupStatus) int64 {
		t.Helper()
		g := newGroup(slug, title, by, nil)
		if err := groups.Create(ctx, g); err != nil {
			t.Fatalf("Create %s: %v", slug, err)
		}
		if status != domain.GroupStatusPending {
			if err := groups.SetStatus(ctx, g.ID, status); err != nil {
				t.Fatalf("SetStatus %s: %v", slug, err)
			}
		}
		return g.ID
	}

	mineActive := mk(t, "М8О-401Б-23", "mine active", owner, domain.GroupStatusActive)
	minePending := mk(t, "М8О-402Б-23", "mine pending", owner, domain.GroupStatusPending)
	otherActive := mk(t, "М8О-403Б-23", "other active", other, domain.GroupStatusActive)
	otherPending := mk(t, "М8О-404Б-23", "other pending", other, domain.GroupStatusPending)
	archived := mk(t, "М8О-405Б-23", "archived", owner, domain.GroupStatusArchived)
	deleted := mk(t, "М8О-406Б-23", "deleted", owner, domain.GroupStatusActive)
	if err := groups.SoftDelete(ctx, deleted); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}

	got, err := groups.SearchByPrefix(ctx, "М8О-40", owner, 100)
	if err != nil {
		t.Fatalf("SearchByPrefix: %v", err)
	}
	ids := map[int64]bool{}
	for _, g := range got {
		ids[g.ID] = true
	}
	wantVisible := []int64{mineActive, minePending, otherActive}
	for _, id := range wantVisible {
		if !ids[id] {
			t.Errorf("group %d missing from search results", id)
		}
	}
	for _, id := range []int64{otherPending, archived, deleted} {
		if ids[id] {
			t.Errorf("group %d must not appear in search results", id)
		}
	}

	// Case-insensitive prefix via normalization.
	got, err = groups.SearchByPrefix(ctx, "м8о-401", owner, 100)
	if err != nil {
		t.Fatalf("SearchByPrefix lowercase: %v", err)
	}
	if len(got) != 1 || got[0].ID != mineActive {
		t.Errorf("lowercase prefix results = %v, want only mineActive", got)
	}

	// Limit is respected.
	got, err = groups.SearchByPrefix(ctx, "М8О-40", owner, 2)
	if err != nil {
		t.Fatalf("SearchByPrefix limit: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("limited results = %d, want 2", len(got))
	}
}

func TestGroupsListMine(t *testing.T) {
	pool := newTestDB(t)
	ctx := context.Background()
	groups := NewGroups(pool)
	members := NewMemberships(pool)
	owner := insertUser(t, pool, 3001)
	stranger := insertUser(t, pool, 3002)

	g1 := newGroup("М8О-501-23", "one", owner, nil)
	g2 := newGroup("М8О-502-23", "two", owner, nil)
	g3 := newGroup("М8О-503-23", "three", owner, nil)
	for _, g := range []*domain.Group{g1, g2, g3} {
		if err := groups.Create(ctx, g); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}
	if err := groups.SetStatus(ctx, g1.ID, domain.GroupStatusActive); err != nil {
		t.Fatalf("SetStatus g1: %v", err)
	}
	for _, m := range []domain.Membership{
		{GroupID: g1.ID, UserID: owner, Role: domain.RoleAdmin},
		{GroupID: g2.ID, UserID: owner, Role: domain.RoleMember},
		{GroupID: g2.ID, UserID: stranger, Role: domain.RoleMember},
	} {
		mm := m
		if err := members.Upsert(ctx, &mm); err != nil {
			t.Fatalf("Upsert membership: %v", err)
		}
	}
	if err := groups.SoftDelete(ctx, g3.ID); err != nil {
		t.Fatalf("SoftDelete g3: %v", err)
	}

	got, err := groups.ListMine(ctx, owner)
	if err != nil {
		t.Fatalf("ListMine: %v", err)
	}
	if len(got) != 2 || got[0].ID != g1.ID || got[1].ID != g2.ID {
		t.Errorf("ListMine = %v, want [g1 g2] ordered by slug_norm", got)
	}

	got, err = groups.ListMine(ctx, stranger)
	if err != nil {
		t.Fatalf("ListMine stranger: %v", err)
	}
	if len(got) != 1 || got[0].ID != g2.ID {
		t.Errorf("ListMine stranger = %v, want [g2]", got)
	}
}

func TestGroupsListPendingExpired(t *testing.T) {
	pool := newTestDB(t)
	ctx := context.Background()
	repo := NewGroups(pool)
	userID := insertUser(t, pool, 4001)

	past := time.Now().UTC().Add(-time.Hour)
	expired := newGroup("М8О-601-23", "expired", userID, &past)
	if err := repo.Create(ctx, expired); err != nil {
		t.Fatalf("Create expired: %v", err)
	}
	future := time.Now().UTC().Add(time.Hour)
	alive := newGroup("М8О-602-23", "alive", userID, &future)
	if err := repo.Create(ctx, alive); err != nil {
		t.Fatalf("Create alive: %v", err)
	}
	noClaim := newGroup("М8О-603-23", "noclaim", userID, nil)
	if err := repo.Create(ctx, noClaim); err != nil {
		t.Fatalf("Create noclaim: %v", err)
	}
	active := newGroup("М8О-604-23", "active", userID, &past)
	if err := repo.Create(ctx, active); err != nil {
		t.Fatalf("Create active: %v", err)
	}
	if err := repo.SetStatus(ctx, active.ID, domain.GroupStatusActive); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}

	got, err := repo.ListPendingExpired(ctx, time.Now().UTC(), 100)
	if err != nil {
		t.Fatalf("ListPendingExpired: %v", err)
	}
	if len(got) != 1 || got[0].ID != expired.ID {
		t.Errorf("ListPendingExpired = %v, want only the expired pending group", got)
	}
}

var _ domain.GroupRepo = (*groupsRepo)(nil)
