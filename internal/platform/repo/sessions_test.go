package repo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sauron/deadliner/internal/domain"
)

func TestSessions_CreateGetActiveTouchRevoke(t *testing.T) {
	pool := newTestDB(t)
	uid := insertUser(t, pool, 1001)
	ctx := context.Background()

	sessions := NewSessions(pool)
	now := time.Now().UTC().Truncate(time.Microsecond)
	ttl := 30 * 24 * time.Hour
	s := &domain.Session{
		TokenHash: "aaaa",
		UserID:    uid,
		ExpiresAt: now.Add(ttl),
		CreatedAt: now,
		LastSeen:  now,
	}
	if err := sessions.Create(ctx, s); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := sessions.GetActive(ctx, "aaaa", now.Add(time.Minute))
	if err != nil {
		t.Fatalf("GetActive: %v", err)
	}
	if got.UserID != uid || !got.ExpiresAt.Equal(s.ExpiresAt) {
		t.Errorf("GetActive = %+v, want session for user %d", got, uid)
	}

	// Истёкшая сессия — ErrNotFound.
	if _, err := sessions.GetActive(ctx, "aaaa", now.Add(ttl+time.Hour)); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("GetActive(expired) err = %v, want ErrNotFound", err)
	}

	// Touch продлевает expires_at и last_seen.
	later := now.Add(2 * time.Hour)
	if err := sessions.Touch(ctx, "aaaa", later, later.Add(ttl)); err != nil {
		t.Fatalf("Touch: %v", err)
	}
	got, err = sessions.GetActive(ctx, "aaaa", now.Add(ttl+time.Hour))
	if err != nil {
		t.Fatalf("GetActive after Touch: %v", err)
	}
	if !got.LastSeen.Equal(later) {
		t.Errorf("LastSeen = %v, want %v", got.LastSeen, later)
	}
	if err := sessions.Touch(ctx, "missing", later, later); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Touch(missing) err = %v, want ErrNotFound", err)
	}

	if err := sessions.Revoke(ctx, "aaaa"); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if _, err := sessions.GetActive(ctx, "aaaa", now); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("GetActive after Revoke err = %v, want ErrNotFound", err)
	}
	if err := sessions.Revoke(ctx, "aaaa"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Revoke(second) err = %v, want ErrNotFound", err)
	}
}

func TestSessions_RevokeAllForUser(t *testing.T) {
	pool := newTestDB(t)
	uid := insertUser(t, pool, 1002)
	ctx := context.Background()

	sessions := NewSessions(pool)
	now := time.Now().UTC()
	for _, h := range []string{"s1", "s2"} {
		err := sessions.Create(ctx, &domain.Session{
			TokenHash: h, UserID: uid,
			ExpiresAt: now.Add(time.Hour), CreatedAt: now, LastSeen: now,
		})
		if err != nil {
			t.Fatalf("Create %s: %v", h, err)
		}
	}

	if err := sessions.RevokeAllForUser(ctx, uid); err != nil {
		t.Fatalf("RevokeAllForUser: %v", err)
	}
	if _, err := sessions.GetActive(ctx, "s1", now); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("session s1 still active: %v", err)
	}
	if _, err := sessions.GetActive(ctx, "s2", now); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("session s2 still active: %v", err)
	}
}
