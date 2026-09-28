package repo

import (
	"context"
	"errors"
	"testing"

	"github.com/sauron/deadliner/internal/domain"
)

func int64Ptr(v int64) *int64 { return &v }

func TestBindingsCreateGetUnbind(t *testing.T) {
	pool := newTestDB(t)
	ctx := context.Background()
	groups := NewGroups(pool)
	repo := NewBindings(pool)
	owner := insertUser(t, pool, 9001)

	g := newGroup("М8О-951-23", "bind", owner, nil)
	if err := groups.Create(ctx, g); err != nil {
		t.Fatalf("Create group: %v", err)
	}

	b := &domain.ChatBinding{
		GroupID:         g.ID,
		ChatID:          -100200300,
		MessageThreadID: nil,
		ChatTitle:       "Чат группы",
		BoundBy:         owner,
	}
	if err := repo.Create(ctx, b); err != nil {
		t.Fatalf("Create binding: %v", err)
	}
	if b.ID == 0 {
		t.Fatal("Create did not set ID")
	}

	got, err := repo.GetByGroup(ctx, g.ID)
	if err != nil {
		t.Fatalf("GetByGroup: %v", err)
	}
	if got.ChatID != -100200300 || got.MessageThreadID != nil || got.ChatTitle != "Чат группы" || got.BoundBy != owner {
		t.Errorf("GetByGroup = %+v", got)
	}

	gotChat, err := repo.GetByChat(ctx, -100200300, nil)
	if err != nil {
		t.Fatalf("GetByChat: %v", err)
	}
	if gotChat.ID != b.ID {
		t.Errorf("GetByChat id = %d, want %d", gotChat.ID, b.ID)
	}

	if err := repo.Delete(ctx, g.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.GetByGroup(ctx, g.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("GetByGroup after Delete = %v, want ErrNotFound", err)
	}
	if _, err := repo.GetByChat(ctx, -100200300, nil); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("GetByChat after Delete = %v, want ErrNotFound", err)
	}
	if err := repo.Delete(ctx, g.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Delete twice = %v, want ErrNotFound", err)
	}
}

func TestBindingsChatAlreadyBoundConflict(t *testing.T) {
	pool := newTestDB(t)
	ctx := context.Background()
	groups := NewGroups(pool)
	repo := NewBindings(pool)
	owner := insertUser(t, pool, 9101)

	g1 := newGroup("М8О-952-23", "one", owner, nil)
	g2 := newGroup("М8О-953-23", "two", owner, nil)
	for _, g := range []*domain.Group{g1, g2} {
		if err := groups.Create(ctx, g); err != nil {
			t.Fatalf("Create group: %v", err)
		}
	}

	b1 := &domain.ChatBinding{GroupID: g1.ID, ChatID: -100500, BoundBy: owner}
	if err := repo.Create(ctx, b1); err != nil {
		t.Fatalf("Create first binding: %v", err)
	}
	// Same chat (NULL thread), different group → conflict (NULLS NOT DISTINCT).
	b2 := &domain.ChatBinding{GroupID: g2.ID, ChatID: -100500, BoundBy: owner}
	if err := repo.Create(ctx, b2); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("second binding of same chat = %v, want ErrConflict", err)
	}
}

func TestBindingsGroupAlreadyBoundConflict(t *testing.T) {
	pool := newTestDB(t)
	ctx := context.Background()
	groups := NewGroups(pool)
	repo := NewBindings(pool)
	owner := insertUser(t, pool, 9201)

	g := newGroup("М8О-954-23", "g", owner, nil)
	if err := groups.Create(ctx, g); err != nil {
		t.Fatalf("Create group: %v", err)
	}
	b1 := &domain.ChatBinding{GroupID: g.ID, ChatID: -111, BoundBy: owner}
	if err := repo.Create(ctx, b1); err != nil {
		t.Fatalf("Create first binding: %v", err)
	}
	// Same group, different chat → group_id unique conflict.
	b2 := &domain.ChatBinding{GroupID: g.ID, ChatID: -222, BoundBy: owner}
	if err := repo.Create(ctx, b2); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("second binding of same group = %v, want ErrConflict", err)
	}
}

func TestBindingsThreadedDistinctChats(t *testing.T) {
	pool := newTestDB(t)
	ctx := context.Background()
	groups := NewGroups(pool)
	repo := NewBindings(pool)
	owner := insertUser(t, pool, 9301)

	g1 := newGroup("М8О-955-23", "one", owner, nil)
	g2 := newGroup("М8О-956-23", "two", owner, nil)
	if err := groups.Create(ctx, g1); err != nil {
		t.Fatalf("Create g1: %v", err)
	}
	if err := groups.Create(ctx, g2); err != nil {
		t.Fatalf("Create g2: %v", err)
	}

	// Same chat, thread 10 and thread NULL: distinct bindings, no conflict.
	if err := repo.Create(ctx, &domain.ChatBinding{GroupID: g1.ID, ChatID: -777, MessageThreadID: int64Ptr(10), BoundBy: owner}); err != nil {
		t.Fatalf("Create threaded binding: %v", err)
	}
	if err := repo.Create(ctx, &domain.ChatBinding{GroupID: g2.ID, ChatID: -777, MessageThreadID: nil, BoundBy: owner}); err != nil {
		t.Fatalf("Create general binding: %v", err)
	}

	got, err := repo.GetByChat(ctx, -777, int64Ptr(10))
	if err != nil {
		t.Fatalf("GetByChat thread: %v", err)
	}
	if got.GroupID != g1.ID {
		t.Errorf("GetByChat(thread=10).GroupID = %d, want %d", got.GroupID, g1.ID)
	}
	got, err = repo.GetByChat(ctx, -777, nil)
	if err != nil {
		t.Fatalf("GetByChat nil thread: %v", err)
	}
	if got.GroupID != g2.ID {
		t.Errorf("GetByChat(nil).GroupID = %d, want %d", got.GroupID, g2.ID)
	}

	// Duplicate exact thread → conflict.
	g3 := newGroup("М8О-957-23", "three", owner, nil)
	if err := groups.Create(ctx, g3); err != nil {
		t.Fatalf("Create g3: %v", err)
	}
	err = repo.Create(ctx, &domain.ChatBinding{GroupID: g3.ID, ChatID: -777, MessageThreadID: int64Ptr(10), BoundBy: owner})
	if !errors.Is(err, domain.ErrConflict) {
		t.Errorf("duplicate thread binding = %v, want ErrConflict", err)
	}
}

var _ domain.ChatBindingRepo = (*bindingsRepo)(nil)
