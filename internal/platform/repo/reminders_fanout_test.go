package repo

import (
	"context"
	"testing"
	"time"

	"github.com/sauron/deadliner/internal/domain"
)

func TestMarkSentWithFanoutCreatesChildren(t *testing.T) {
	pool := newTestDB(t)
	ctx := context.Background()
	uid := insertUser(t, pool, 3001)
	now := time.Now().UTC().Truncate(time.Microsecond)

	groups := NewGroups(pool)
	g := newGroup("М8О-911-23", "fanout", uid, nil)
	if err := groups.Create(ctx, g); err != nil {
		t.Fatal(err)
	}
	bindings := NewBindings(pool)
	if err := bindings.Create(ctx, &domain.ChatBinding{GroupID: g.ID, ChatID: -100911, BoundBy: uid}); err != nil {
		t.Fatal(err)
	}
	dID, rems := insertDeadlineFixture(t, uid, &g.ID, now.Add(48*time.Hour), []time.Duration{24 * time.Hour})
	rem := rems[0]

	// Лочим строку как воркер.
	if _, err := pool.Exec(ctx,
		`UPDATE reminders SET locked_by='w1', locked_at=$2 WHERE id=$1`, rem.ID, now); err != nil {
		t.Fatal(err)
	}

	rr := NewReminders(pool)
	kids := []domain.Reminder{
		{DeadlineID: dID, Kind: domain.KindDMDup, TargetUserID: int64Ptr(1), FireAt: now, Status: domain.ReminderStatusPending},
		{DeadlineID: dID, Kind: domain.KindDMDup, TargetUserID: int64Ptr(2), FireAt: now, Status: domain.ReminderStatusPending},
		{DeadlineID: dID, Kind: domain.KindDMDup, TargetUserID: int64Ptr(3), FireAt: now, Status: domain.ReminderStatusPending},
	}
	tx, rollback, err := BeginDomainTx(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	defer rollback()
	ok, err := rr.MarkSentWithFanout(ctx, tx, rem.ID, "w1", now, kids)
	if err != nil || !ok {
		t.Fatalf("MarkSentWithFanout = (%v, %v), want (true, nil)", ok, err)
	}
	if err := CommitDomainTx(ctx, tx); err != nil {
		t.Fatal(err)
	}

	// Родитель sent, дети pending с целью.
	list, err := rr.ListByDeadline(ctx, dID)
	if err != nil {
		t.Fatal(err)
	}
	var sentParents, children int
	for _, r := range list {
		switch {
		case r.ID == rem.ID:
			if r.Status != domain.ReminderStatusSent || r.SentAt == nil || !r.SentAt.Equal(now) {
				t.Errorf("parent = %+v, want sent at %v", r, now)
			}
			sentParents++
		case r.Kind == domain.KindDMDup:
			children++
			if r.Status != domain.ReminderStatusPending || r.TargetUserID == nil || r.OffsetMinutes != nil {
				t.Errorf("child = %+v, want pending dm_dup with target", r)
			}
		}
	}
	if sentParents != 1 || children != 3 {
		t.Errorf("rows: parents=%d children=%d, want 1/3", sentParents, children)
	}

	// Повтор (идемпотентность гонки): MarkSent уже сделан — ok=false, новых
	// детей нет.
	tx2, rollback2, err := BeginDomainTx(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	defer rollback2()
	ok, err = rr.MarkSentWithFanout(ctx, tx2, rem.ID, "w1", now, kids)
	if err != nil || ok {
		t.Fatalf("second MarkSentWithFanout = (%v, %v), want (false, nil)", ok, err)
	}
	list2, _ := rr.ListByDeadline(ctx, dID)
	if len(list2) != len(list) {
		t.Errorf("after repeat rows = %d, want %d", len(list2), len(list))
	}
}

// Дубль ребёнка (тот же target) — ON CONFLICT DO NOTHING, ошибка нет.
func TestMarkSentWithFanoutDuplicateChild(t *testing.T) {
	pool := newTestDB(t)
	ctx := context.Background()
	uid := insertUser(t, pool, 3002)
	now := time.Now().UTC().Truncate(time.Microsecond)
	groups := NewGroups(pool)
	g := newGroup("М8О-912-23", "dup", uid, nil)
	if err := groups.Create(ctx, g); err != nil {
		t.Fatal(err)
	}
	dID, rems := insertDeadlineFixture(t, uid, &g.ID, now.Add(48*time.Hour), []time.Duration{24 * time.Hour})
	rem := rems[0]
	if _, err := pool.Exec(ctx,
		`UPDATE reminders SET locked_by='w1', locked_at=$2 WHERE id=$1`, rem.ID, now); err != nil {
		t.Fatal(err)
	}
	rr := NewReminders(pool)
	kids := []domain.Reminder{
		{DeadlineID: dID, Kind: domain.KindDMDup, TargetUserID: int64Ptr(42), FireAt: now},
		{DeadlineID: dID, Kind: domain.KindDMDup, TargetUserID: int64Ptr(42), FireAt: now},
	}
	tx, rollback, err := BeginDomainTx(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	defer rollback()
	if ok, err := rr.MarkSentWithFanout(ctx, tx, rem.ID, "w1", now, kids); err != nil || !ok {
		t.Fatalf("MarkSentWithFanout = (%v, %v)", ok, err)
	}
	if err := CommitDomainTx(ctx, tx); err != nil {
		t.Fatal(err)
	}
	list, _ := rr.ListByDeadline(ctx, dID)
	var kids42 int
	for _, r := range list {
		if r.Kind == domain.KindDMDup && r.TargetUserID != nil && *r.TargetUserID == 42 {
			kids42++
		}
	}
	if kids42 != 1 {
		t.Errorf("dm_dup target=42 rows = %d, want 1", kids42)
	}
}

// Чужой лок: ok=false, дети не создаются.
func TestMarkSentWithFanoutForeignLock(t *testing.T) {
	pool := newTestDB(t)
	ctx := context.Background()
	uid := insertUser(t, pool, 3003)
	now := time.Now().UTC()
	groups := NewGroups(pool)
	g := newGroup("М8О-913-23", "fl", uid, nil)
	if err := groups.Create(ctx, g); err != nil {
		t.Fatal(err)
	}
	dID, rems := insertDeadlineFixture(t, uid, &g.ID, now.Add(48*time.Hour), []time.Duration{24 * time.Hour})
	rem := rems[0]
	if _, err := pool.Exec(ctx,
		`UPDATE reminders SET locked_by='ghost', locked_at=$2 WHERE id=$1`, rem.ID, now); err != nil {
		t.Fatal(err)
	}
	rr := NewReminders(pool)
	tx, rollback, err := BeginDomainTx(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	defer rollback()
	kids := []domain.Reminder{{DeadlineID: dID, Kind: domain.KindDMDup, TargetUserID: int64Ptr(1), FireAt: now}}
	ok, err := rr.MarkSentWithFanout(ctx, tx, rem.ID, "w1", now, kids)
	if err != nil || ok {
		t.Fatalf("foreign lock MarkSentWithFanout = (%v, %v), want (false, nil)", ok, err)
	}
	list, _ := rr.ListByDeadline(ctx, dID)
	if len(list) != 1 {
		t.Errorf("children created for foreign lock: %d rows", len(list))
	}
}

// Два fan-out'а одного дедлайна → по ребёнку на (цель, fire_at), а не
// «один на цель навсегда».
func TestMarkSentWithFanoutMultipleFanoutsPerDeadline(t *testing.T) {
	pool := newTestDB(t)
	ctx := context.Background()
	uid := insertUser(t, pool, 3004)
	now := time.Now().UTC().Truncate(time.Microsecond)

	groups := NewGroups(pool)
	g := newGroup("М8О-914-23", "multi", uid, nil)
	if err := groups.Create(ctx, g); err != nil {
		t.Fatal(err)
	}
	dID, rems := insertDeadlineFixture(t, uid, &g.ID, now.Add(48*time.Hour),
		[]time.Duration{24 * time.Hour, time.Hour, 30 * time.Minute})
	rr := NewReminders(pool)

	// Каждое напоминание — свой fan-out со своим fire_at.
	for i, rem := range rems {
		if _, err := pool.Exec(ctx,
			`UPDATE reminders SET locked_by='w1', locked_at=$2 WHERE id=$1`, rem.ID, now); err != nil {
			t.Fatal(err)
		}
		fanoutAt := now.Add(time.Duration(i) * time.Minute)
		tx, rollback, err := BeginDomainTx(ctx, pool)
		if err != nil {
			t.Fatal(err)
		}
		ok, err := rr.MarkSentWithFanout(ctx, tx, rem.ID, "w1", now,
			[]domain.Reminder{{DeadlineID: dID, Kind: domain.KindDMDup, TargetUserID: int64Ptr(777), FireAt: fanoutAt}})
		if err != nil || !ok {
			rollback()
			t.Fatalf("fan-out %d = (%v, %v), want (true, nil)", i, ok, err)
		}
		if err := CommitDomainTx(ctx, tx); err != nil {
			rollback()
			t.Fatal(err)
		}
		rollback()
	}

	list, err := rr.ListByDeadline(ctx, dID)
	if err != nil {
		t.Fatal(err)
	}
	var children int
	for _, r := range list {
		if r.Kind == domain.KindDMDup {
			children++
			if r.Status != domain.ReminderStatusPending || r.TargetUserID == nil || *r.TargetUserID != 777 {
				t.Errorf("child = %+v, want pending with target 777", r)
			}
		}
	}
	if children != len(rems) {
		t.Errorf("dm_dup children = %d, want %d (one per fan-out)", children, len(rems))
	}
}

// Реальная гонка двух fan-out'ов ОДНОГО напоминания (одинаковый fire_at):
// unique (deadline_id, target_user_id, fire_at) + ON CONFLICT DO NOTHING
// оставляют ровно одного ребёнка на цель.
func TestMarkSentWithFanoutSameFireAtRaceDedupes(t *testing.T) {
	pool := newTestDB(t)
	ctx := context.Background()
	uid := insertUser(t, pool, 3005)
	now := time.Now().UTC().Truncate(time.Microsecond)

	groups := NewGroups(pool)
	g := newGroup("М8О-915-23", "samefire", uid, nil)
	if err := groups.Create(ctx, g); err != nil {
		t.Fatal(err)
	}
	dID, rems := insertDeadlineFixture(t, uid, &g.ID, now.Add(48*time.Hour), []time.Duration{24 * time.Hour})
	rem := rems[0]
	rr := NewReminders(pool)

	// Первый fan-out вручную (как будто его сделал другой воркер), затем
	// повторный MarkSentWithFanout с тем же fire_at.
	if _, err := pool.Exec(ctx,
		`UPDATE reminders SET locked_by='w1', locked_at=$2 WHERE id=$1`, rem.ID, now); err != nil {
		t.Fatal(err)
	}
	tx1, rb1, err := BeginDomainTx(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	defer rb1()
	if ok, err := rr.MarkSentWithFanout(ctx, tx1, rem.ID, "w1", now,
		[]domain.Reminder{{DeadlineID: dID, Kind: domain.KindDMDup, TargetUserID: int64Ptr(9), FireAt: now}}); err != nil || !ok {
		t.Fatalf("first fan-out = (%v, %v)", ok, err)
	}
	if err := CommitDomainTx(ctx, tx1); err != nil {
		t.Fatal(err)
	}

	// Повтор того же fan-out: родитель уже sent → ok=false, детей не трогаем.
	tx2, rb2, err := BeginDomainTx(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	defer rb2()
	if ok, err := rr.MarkSentWithFanout(ctx, tx2, rem.ID, "w1", now,
		[]domain.Reminder{{DeadlineID: dID, Kind: domain.KindDMDup, TargetUserID: int64Ptr(9), FireAt: now}}); err != nil || ok {
		t.Fatalf("repeat fan-out = (%v, %v), want (false, nil)", ok, err)
	}

	list, _ := rr.ListByDeadline(ctx, dID)
	var kids int
	for _, r := range list {
		if r.Kind == domain.KindDMDup {
			kids++
		}
	}
	if kids != 1 {
		t.Errorf("dm_dup children = %d, want 1 (same-fire_at dedupe)", kids)
	}
}

func TestListDMTargets(t *testing.T) {
	pool := newTestDB(t)
	ctx := context.Background()
	owner := insertUser(t, pool, 3100)
	groups := NewGroups(pool)
	g := newGroup("М8О-921-23", "dmt", owner, nil)
	if err := groups.Create(ctx, g); err != nil {
		t.Fatal(err)
	}
	mem := NewMemberships(pool)

	// owner: membership dm_notify=NULL, user default false → вне списка.
	if err := mem.Upsert(ctx, &domain.Membership{GroupID: g.ID, UserID: owner, Role: domain.RoleAdmin}); err != nil {
		t.Fatal(err)
	}
	// u2: membership ON (override true) → в списке.
	u2 := insertUser(t, pool, 3102)
	if err := mem.Upsert(ctx, &domain.Membership{GroupID: g.ID, UserID: u2, Role: domain.RoleMember, DMNotify: boolPtr(true)}); err != nil {
		t.Fatal(err)
	}
	// u3: default ON (users.dm_notify_default=true), membership NULL → в списке.
	u3 := insertUser(t, pool, 3103)
	if _, err := pool.Exec(ctx,
		`UPDATE users SET dm_notify_default=true WHERE id=$1`, u3); err != nil {
		t.Fatal(err)
	}
	if err := mem.Upsert(ctx, &domain.Membership{GroupID: g.ID, UserID: u3, Role: domain.RoleMember}); err != nil {
		t.Fatal(err)
	}
	// u4: default ON, но override false → вне списка.
	u4 := insertUser(t, pool, 3104)
	if _, err := pool.Exec(ctx,
		`UPDATE users SET dm_notify_default=true WHERE id=$1`, u4); err != nil {
		t.Fatal(err)
	}
	if err := mem.Upsert(ctx, &domain.Membership{GroupID: g.ID, UserID: u4, Role: domain.RoleMember, DMNotify: boolPtr(false)}); err != nil {
		t.Fatal(err)
	}
	// u5: dm_notify ON, но bot_blocked → вне списка.
	u5 := insertUser(t, pool, 3105)
	if _, err := pool.Exec(ctx,
		`UPDATE users SET bot_blocked=true WHERE id=$1`, u5); err != nil {
		t.Fatal(err)
	}
	if err := mem.Upsert(ctx, &domain.Membership{GroupID: g.ID, UserID: u5, Role: domain.RoleMember, DMNotify: boolPtr(true)}); err != nil {
		t.Fatal(err)
	}

	got, err := mem.ListDMTargets(ctx, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := []int64{u2, u3}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("ListDMTargets = %v, want %v", got, want)
	}
}
