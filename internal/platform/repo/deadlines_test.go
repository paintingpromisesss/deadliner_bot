package repo

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/sauron/deadliner/internal/domain"
)

// insertDeadlineFixture создаёт дедлайн (личной или групповой) вместе с
// reminders одной транзакцией репо и возвращает его ID.
func insertDeadlineFixture(t *testing.T, userID int64, groupID *int64, dueAt time.Time, offs []time.Duration) (int64, []domain.Reminder) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	owner := &userID
	if groupID != nil {
		owner = nil
	}
	d := &domain.Deadline{
		GroupID:     groupID,
		OwnerUserID: owner,
		Title:       "Курсовая",
		Description: "описание",
		DueAt:       dueAt,
		TZ:          "Europe/Moscow",
		CreatedBy:   userID,
		Status:      domain.DeadlineStatusActive,
	}
	reminders := make([]domain.Reminder, 0, len(offs))
	for _, off := range offs {
		mins := int(off / time.Minute)
		reminders = append(reminders, domain.Reminder{
			Kind:          domain.KindPreset,
			OffsetMinutes: &mins,
			FireAt:        dueAt.Add(-off),
			Status:        domain.ReminderStatusPending,
		})
	}
	if err := NewDeadlines(testPool).Create(ctx, d, reminders); err != nil {
		t.Fatalf("create deadline: %v", err)
	}
	if d.ID == 0 {
		t.Fatalf("Create did not fill deadline ID")
	}
	for i := range reminders {
		if reminders[i].DeadlineID != d.ID || reminders[i].ID == 0 {
			t.Fatalf("reminder %d not linked: %+v", i, reminders[i])
		}
	}
	return d.ID, reminders
}

// TestDeadlineCreateTx — deadline + reminders создаются одной транзакцией,
// поля заполняются, GetByID возвращает полный ряд.
func TestDeadlineCreateTx(t *testing.T) {
	pool := newTestDB(t)
	ctx := t.Context()
	uid := insertUser(t, pool, 1001)
	due := time.Now().UTC().Add(72 * time.Hour).Truncate(time.Second)

	dID, reminders := insertDeadlineFixture(t, uid, nil, due, []time.Duration{24 * time.Hour, time.Hour})
	if len(reminders) != 2 {
		t.Fatalf("reminders = %d, want 2", len(reminders))
	}

	got, err := NewDeadlines(pool).GetByID(ctx, dID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Title != "Курсовая" || got.Description != "описание" || got.TZ != "Europe/Moscow" {
		t.Errorf("deadline fields: %+v", got)
	}
	if !got.DueAt.Equal(due) {
		t.Errorf("due_at = %v, want %v", got.DueAt, due)
	}
	if got.OwnerUserID == nil || *got.OwnerUserID != uid {
		t.Errorf("owner = %v, want %d", got.OwnerUserID, uid)
	}
	if got.Status != domain.DeadlineStatusActive {
		t.Errorf("status = %q, want active", got.Status)
	}

	list, err := NewReminders(pool).ListByDeadline(ctx, dID)
	if err != nil {
		t.Fatalf("ListByDeadline: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("stored reminders = %d, want 2", len(list))
	}
	for _, r := range list {
		if r.DeadlineID != dID || r.Kind != domain.KindPreset || r.Status != domain.ReminderStatusPending {
			t.Errorf("reminder row: %+v", r)
		}
	}
}

// TestDeadlineCreateAtomicity — ошибка вставки reminders откатывает deadline.
func TestDeadlineCreateAtomicity(t *testing.T) {
	pool := newTestDB(t)
	ctx := t.Context()
	uid := insertUser(t, pool, 1002)
	due := time.Now().UTC().Add(48 * time.Hour)

	// Два reminder с одинаковым (kind, offset) → unique violation на втором;
	// deadline не должен остаться в БД.
	mins := 60
	d := &domain.Deadline{
		OwnerUserID: &uid, Title: "T", DueAt: due, TZ: "UTC",
		CreatedBy: uid, Status: domain.DeadlineStatusActive,
	}
	bad := []domain.Reminder{
		{Kind: domain.KindPreset, OffsetMinutes: &mins, FireAt: due.Add(-time.Hour), Status: domain.ReminderStatusPending},
		{Kind: domain.KindPreset, OffsetMinutes: &mins, FireAt: due.Add(-2 * time.Hour), Status: domain.ReminderStatusPending},
	}
	err := NewDeadlines(pool).Create(ctx, d, bad)
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("Create with duplicate offsets = %v, want ErrConflict", err)
	}

	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM deadlines`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("deadlines rows = %d, want 0 (rollback)", count)
	}
}

// TestDeadlineUpdatePatchFields — Update меняет только заданные поля.
func TestDeadlineUpdatePatchFields(t *testing.T) {
	pool := newTestDB(t)
	ctx := t.Context()
	uid := insertUser(t, pool, 1003)
	due := time.Now().UTC().Add(72 * time.Hour).Truncate(time.Second)
	dID, _ := insertDeadlineFixture(t, uid, nil, due, nil)

	repo := NewDeadlines(pool)
	newTitle := "Новый заголовок"
	if err := repo.Update(ctx, dID, domain.DeadlinePatch{Title: &newTitle}); err != nil {
		t.Fatalf("Update title: %v", err)
	}
	got, err := repo.GetByID(ctx, dID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != newTitle {
		t.Errorf("title = %q, want %q", got.Title, newTitle)
	}
	if got.Description != "описание" || !got.DueAt.Equal(due) || got.TZ != "Europe/Moscow" {
		t.Errorf("untouched fields changed: %+v", got)
	}

	newDue := due.Add(24 * time.Hour)
	newTZ := "Asia/Novosibirsk"
	newDesc := ""
	if err := repo.Update(ctx, dID, domain.DeadlinePatch{DueAt: &newDue, TZ: &newTZ, Description: &newDesc}); err != nil {
		t.Fatalf("Update due/tz/desc: %v", err)
	}
	got, err = repo.GetByID(ctx, dID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.DueAt.Equal(newDue) || got.TZ != newTZ || got.Description != "" || got.Title != newTitle {
		t.Errorf("after patch: %+v", got)
	}

	if err := repo.Update(ctx, 999999, domain.DeadlinePatch{Title: &newTitle}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Update missing = %v, want ErrNotFound", err)
	}
}

// TestDeadlineSetStatusAndSoftDelete — статусы валидируются, soft delete
// скрывает ряд из GetByID и списков.
func TestDeadlineSetStatusAndSoftDelete(t *testing.T) {
	pool := newTestDB(t)
	ctx := t.Context()
	uid := insertUser(t, pool, 1004)
	due := time.Now().UTC().Add(72 * time.Hour)
	dID, _ := insertDeadlineFixture(t, uid, nil, due, nil)

	repo := NewDeadlines(pool)
	if err := repo.SetStatus(ctx, dID, "bogus"); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("SetStatus(bogus) = %v, want ErrValidation", err)
	}
	if err := repo.SetStatus(ctx, dID, domain.DeadlineStatusDone); err != nil {
		t.Fatalf("SetStatus(done): %v", err)
	}
	got, _ := repo.GetByID(ctx, dID)
	if got.Status != domain.DeadlineStatusDone {
		t.Errorf("status = %q, want done", got.Status)
	}
	if err := repo.SetStatus(ctx, 999999, domain.DeadlineStatusDone); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("SetStatus(missing) = %v, want ErrNotFound", err)
	}

	if err := repo.SoftDelete(ctx, dID); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}
	if _, err := repo.GetByID(ctx, dID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("GetByID after soft delete = %v, want ErrNotFound", err)
	}
	list, err := repo.ListByOwner(ctx, uid, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Errorf("ListByOwner after soft delete = %d rows, want 0", len(list))
	}
	if err := repo.SoftDelete(ctx, dID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("double SoftDelete = %v, want ErrNotFound", err)
	}
}

// TestDeadlineListFilters — from/to/status, порядок due_at ASC, групповые
// и личные списки не пересекаются.
func TestDeadlineListFilters(t *testing.T) {
	pool := newTestDB(t)
	ctx := t.Context()
	uid := insertUser(t, pool, 1005)
	other := insertUser(t, pool, 1006)
	now := time.Now().UTC().Truncate(time.Second)

	// Три личных дедлайна: через 1h, 24h, 72h; третий — done.
	d1, _ := insertDeadlineFixture(t, uid, nil, now.Add(time.Hour), nil)
	d2, _ := insertDeadlineFixture(t, uid, nil, now.Add(24*time.Hour), nil)
	d3, _ := insertDeadlineFixture(t, uid, nil, now.Add(72*time.Hour), nil)
	repo := NewDeadlines(pool)
	if err := repo.SetStatus(ctx, d3, domain.DeadlineStatusDone); err != nil {
		t.Fatal(err)
	}
	// Чужой личный дедлайн не должен попадать в список.
	insertDeadlineFixture(t, other, nil, now.Add(2*time.Hour), nil)

	all, err := repo.ListByOwner(ctx, uid, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("ListByOwner all = %d, want 3", len(all))
	}
	if all[0].ID != d1 || all[1].ID != d2 || all[2].ID != d3 {
		t.Errorf("order = %d,%d,%d, want due_at ASC %d,%d,%d",
			all[0].ID, all[1].ID, all[2].ID, d1, d2, d3)
	}

	// Фильтр окна: [now, now+48h] → d1, d2.
	from, to := now, now.Add(48*time.Hour)
	win, err := repo.ListByOwner(ctx, uid, &from, &to, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(win) != 2 || win[0].ID != d1 || win[1].ID != d2 {
		t.Errorf("window = %+v, want d1,d2", win)
	}

	// Фильтр статуса: active → d1, d2.
	st := domain.DeadlineStatusActive
	active, err := repo.ListByOwner(ctx, uid, nil, nil, &st)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 2 || active[1].ID != d2 {
		t.Errorf("active = %+v, want d1,d2", active)
	}

	// Групповой список изолирован.
	var gid int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO groups (slug, slug_norm, title, status, created_by)
		 VALUES ('Г-1', 'г-1', 'G', 'active', $1) RETURNING id`, uid).Scan(&gid); err != nil {
		t.Fatal(err)
	}
	gd1, _ := insertDeadlineFixture(t, uid, &gid, now.Add(5*time.Hour), nil)
	insertDeadlineFixture(t, uid, &gid, now.Add(10*time.Hour), nil)
	gl, err := repo.ListByGroup(ctx, gid, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(gl) != 2 || gl[0].ID != gd1 {
		t.Errorf("ListByGroup = %+v, want 2 rows starting with %d", gl, gd1)
	}
	if gl[0].GroupID == nil || *gl[0].GroupID != gid {
		t.Errorf("group_id = %v, want %d", gl[0].GroupID, gid)
	}
	// Личные не содержат групповых и наоборот.
	personal, _ := repo.ListByOwner(ctx, uid, nil, nil, nil)
	if len(personal) != 3 {
		t.Errorf("personal after group creates = %d, want 3", len(personal))
	}
}

// --- reminders ---

// TestReminderRegenerate — pending отменяются, новые вставляются одной
// транзакцией; sent/cancelled не трогаются.
func TestReminderRegenerate(t *testing.T) {
	pool := newTestDB(t)
	ctx := t.Context()
	uid := insertUser(t, pool, 2001)
	due := time.Now().UTC().Add(72 * time.Hour).Truncate(time.Second)
	dID, _ := insertDeadlineFixture(t, uid, nil, due, []time.Duration{7 * 24 * time.Hour, 24 * time.Hour, time.Hour})

	// Один reminder помечаем sent — он должен пережить регенерацию.
	rr := NewReminders(pool)
	rows, err := pool.Query(ctx,
		`SELECT id FROM reminders WHERE deadline_id = $1 ORDER BY fire_at LIMIT 1`, dID)
	if err != nil {
		t.Fatal(err)
	}
	rows.Next()
	var sentID int64
	if err := rows.Scan(&sentID); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if _, err := pool.Exec(ctx,
		`UPDATE reminders SET status='sent', sent_at=now() WHERE id=$1`, sentID); err != nil {
		t.Fatal(err)
	}

	newDue := due.Add(48 * time.Hour)
	fresh := domain.PlanReminders(domain.Deadline{ID: dID, DueAt: newDue},
		[]time.Duration{24 * time.Hour, time.Hour}, time.Now().UTC())
	inserted, err := rr.Regenerate(ctx, dID, fresh)
	if err != nil {
		t.Fatalf("Regenerate: %v", err)
	}
	if inserted != 2 {
		t.Errorf("inserted = %d, want 2", inserted)
	}

	list, err := rr.ListByDeadline(ctx, dID)
	if err != nil {
		t.Fatal(err)
	}
	// Unique-индекс (deadline_id, kind, offset) не учитывает status, поэтому
	// Regenerate воскрешает только что отменённые строки с теми же offsets
	// in-place: sent (1, неприкосновенна) + pending (2, fire_at пересчитан).
	if len(list) != 3 {
		t.Fatalf("rows = %d, want 3: %+v", len(list), list)
	}
	var pending int
	for _, r := range list {
		switch r.Status {
		case domain.ReminderStatusPending:
			pending++
			if !r.FireAt.Equal(newDue.Add(-time.Duration(*r.OffsetMinutes) * time.Minute)) {
				t.Errorf("pending fire_at = %v, want scaled to new due", r.FireAt)
			}
		case domain.ReminderStatusSent:
			if r.ID != sentID {
				t.Errorf("unexpected sent row %d", r.ID)
			}
		default:
			t.Errorf("unexpected status %q on row %d", r.Status, r.ID)
		}
	}
	if pending != 2 {
		t.Errorf("pending = %d, want 2", pending)
	}
}

// TestReminderRegenerateConflictTolerated — повторный Regenerate с теми же
// offsets не падает на unique-индексе: дубликаты пропускаются, счётчик
// отражает фактически вставленные строки.
func TestReminderRegenerateConflictTolerated(t *testing.T) {
	pool := newTestDB(t)
	ctx := t.Context()
	uid := insertUser(t, pool, 2002)
	due := time.Now().UTC().Add(72 * time.Hour).Truncate(time.Second)
	dID, _ := insertDeadlineFixture(t, uid, nil, due, []time.Duration{24 * time.Hour})

	rr := NewReminders(pool)
	now := time.Now().UTC()
	// Первая регенерация: cancel pending (24h), insert 1h.
	first := domain.PlanReminders(domain.Deadline{ID: dID, DueAt: due}, []time.Duration{time.Hour}, now)
	if n, err := rr.Regenerate(ctx, dID, first); err != nil || n != 1 {
		t.Fatalf("first Regenerate = (%d, %v), want (1, nil)", n, err)
	}
	// Вторая — те же offsets: идемпотентна. Unique-индекс не учитывает status,
	// поэтому строка воскрешается из cancelled, а не вставляется заново:
	// без конфликта и без дублей.
	second := domain.PlanReminders(domain.Deadline{ID: dID, DueAt: due}, []time.Duration{time.Hour}, now)
	n, err := rr.Regenerate(ctx, dID, second)
	if err != nil {
		t.Fatalf("second Regenerate must tolerate conflict: %v", err)
	}
	if n != 1 {
		t.Errorf("inserted = %d, want 1 (cancelled row resurrected)", n)
	}
	list, _ := rr.ListByDeadline(ctx, dID)
	var pending1h int
	for _, r := range list {
		if r.Status == domain.ReminderStatusPending && r.OffsetMinutes != nil && *r.OffsetMinutes == 60 {
			pending1h++
		}
	}
	if pending1h != 1 {
		t.Errorf("pending 1h rows = %d, want exactly 1 (no duplicates)", pending1h)
	}
}

// TestReminderRegenerateMultipleCustomAt — регенерация (как при PATCH due_at)
// на дедлайне с двумя custom_at и одним preset: старый upsert переписал бы ВСЕ
// cancelled custom_at на один fire_at и упал в 23505; теперь каждая custom_at
// воскрешается по точному совпадению fire_at, preset — по offset. Успех,
// итоговый набор корректен, дублей нет.
func TestReminderRegenerateMultipleCustomAt(t *testing.T) {
	pool := newTestDB(t)
	ctx := t.Context()
	uid := insertUser(t, pool, 2010)
	now := time.Now().UTC().Truncate(time.Second)
	due := now.Add(72 * time.Hour)
	dID, _ := insertDeadlineFixture(t, uid, nil, due, []time.Duration{24 * time.Hour})

	rr := NewReminders(pool)
	// Два custom_at reminder'а (независимые от due_at).
	ca1 := now.Add(10 * time.Hour)
	ca2 := now.Add(20 * time.Hour)
	if err := rr.CreateBatch(ctx, []domain.Reminder{
		{DeadlineID: dID, Kind: domain.KindCustomAt, FireAt: ca1, Status: domain.ReminderStatusPending},
		{DeadlineID: dID, Kind: domain.KindCustomAt, FireAt: ca2, Status: domain.ReminderStatusPending},
	}); err != nil {
		t.Fatal(err)
	}

	// PATCH due_at → service.replan строит новый набор: custom_at как есть,
	// preset от нового due.
	newDue := due.Add(48 * time.Hour)
	planned := []domain.Reminder{
		domain.NewCustomAtReminder(dID, ca1),
		domain.NewCustomAtReminder(dID, ca2),
	}
	mins := 1440
	planned = append(planned, domain.Reminder{
		DeadlineID: dID, Kind: domain.KindPreset, OffsetMinutes: &mins,
		FireAt: newDue.Add(-24 * time.Hour), Status: domain.ReminderStatusPending,
	})

	inserted, err := rr.Regenerate(ctx, dID, planned)
	if err != nil {
		t.Fatalf("Regenerate with 2 custom_at: %v", err)
	}
	if inserted != 3 {
		t.Errorf("inserted = %d, want 3", inserted)
	}

	list, err := rr.ListByDeadline(ctx, dID)
	if err != nil {
		t.Fatal(err)
	}
	// fire_at возвращается в tz сессии БД — сравниваем через Equal, не форматом.
	var pending []domain.Reminder
	for _, r := range list {
		if r.Status == domain.ReminderStatusPending {
			pending = append(pending, r)
		} else if r.Status != domain.ReminderStatusCancelled {
			t.Errorf("unexpected status %q on row %d", r.Status, r.ID)
		}
	}
	if len(pending) != 3 {
		t.Fatalf("pending = %+v, want ca1/ca2/preset(new due)", pending)
	}
	var gotCA1, gotCA2, gotPreset bool
	presetWant := newDue.Add(-24 * time.Hour)
	for _, r := range pending {
		switch {
		case r.Kind == domain.KindCustomAt && r.FireAt.Equal(ca1):
			gotCA1 = true
		case r.Kind == domain.KindCustomAt && r.FireAt.Equal(ca2):
			gotCA2 = true
		case r.Kind == domain.KindPreset && r.FireAt.Equal(presetWant):
			gotPreset = true
		default:
			t.Errorf("unexpected pending reminder: %+v", r)
		}
	}
	if !gotCA1 || !gotCA2 || !gotPreset {
		t.Errorf("pending set incomplete: ca1=%v ca2=%v preset=%v", gotCA1, gotCA2, gotPreset)
	}
}

// TestReminderRegenerateResurrectionCollision — воскрешение preset, чей новый
// fire_at занят sent-строкой того же (deadline, fire_at, kind): без ошибки
// (NOT EXISTS-гард), sent-строка не тронута, кандидат остаётся cancelled.
func TestReminderRegenerateResurrectionCollision(t *testing.T) {
	pool := newTestDB(t)
	ctx := t.Context()
	uid := insertUser(t, pool, 2011)
	now := time.Now().UTC().Truncate(time.Second)
	due := now.Add(72 * time.Hour)
	// Один pending preset (будет cancelled) — кандидат на воскрешение.
	dID, _ := insertDeadlineFixture(t, uid, nil, due, []time.Duration{24 * time.Hour})

	rr := NewReminders(pool)
	// sent-строка: preset offset 60, fire_at = T.
	sentFire := now.Add(5 * time.Hour)
	sentMins := 60
	sent := []domain.Reminder{{
		DeadlineID: dID, Kind: domain.KindPreset, OffsetMinutes: &sentMins,
		FireAt: sentFire, Status: domain.ReminderStatusPending,
	}}
	if err := rr.CreateBatch(ctx, sent); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE reminders SET status='sent', sent_at=now() WHERE id=$1`, sent[0].ID); err != nil {
		t.Fatal(err)
	}

	// Regenerate: новый preset offset 24h, но fire_at = sentFire — коллизия
	// по unique (deadline_id, fire_at, kind) с sent-строкой.
	mins := 1440
	colliding := []domain.Reminder{{
		DeadlineID: dID, Kind: domain.KindPreset, OffsetMinutes: &mins,
		FireAt: sentFire, Status: domain.ReminderStatusPending,
	}}
	inserted, err := rr.Regenerate(ctx, dID, colliding)
	if err != nil {
		t.Fatalf("Regenerate with fire_at collision: %v", err)
	}
	if inserted != 0 {
		t.Errorf("inserted = %d, want 0 (collision skipped)", inserted)
	}

	list, _ := rr.ListByDeadline(ctx, dID)
	for _, r := range list {
		switch {
		case r.ID == sent[0].ID:
			if r.Status != domain.ReminderStatusSent || !r.FireAt.Equal(sentFire) {
				t.Errorf("sent row mutated: %+v", r)
			}
		case r.OffsetMinutes != nil && *r.OffsetMinutes == 1440:
			if r.Status != domain.ReminderStatusCancelled {
				t.Errorf("candidate must stay cancelled: %+v", r)
			}
		}
	}
}

// TestReminderCancelByDeadline — все pending отменяются, остальные не трогаются.
func TestReminderCancelByDeadline(t *testing.T) {
	pool := newTestDB(t)
	ctx := t.Context()
	uid := insertUser(t, pool, 2003)
	due := time.Now().UTC().Add(72 * time.Hour)
	dID, _ := insertDeadlineFixture(t, uid, nil, due, []time.Duration{24 * time.Hour, time.Hour})

	rr := NewReminders(pool)
	if _, err := pool.Exec(ctx,
		`UPDATE reminders SET status='sent' WHERE deadline_id=$1 AND offset_minutes=60`, dID); err != nil {
		t.Fatal(err)
	}
	if err := rr.CancelByDeadline(ctx, dID); err != nil {
		t.Fatalf("CancelByDeadline: %v", err)
	}
	list, _ := rr.ListByDeadline(ctx, dID)
	for _, r := range list {
		if *r.OffsetMinutes == 60 && r.Status != domain.ReminderStatusSent {
			t.Errorf("sent row flipped to %q", r.Status)
		}
		if *r.OffsetMinutes == 1440 && r.Status != domain.ReminderStatusCancelled {
			t.Errorf("pending row status = %q, want cancelled", r.Status)
		}
	}
}

// TestReminderCreateBatch — пакетная вставка вне транзакции deadline.
func TestReminderCreateBatch(t *testing.T) {
	pool := newTestDB(t)
	ctx := t.Context()
	uid := insertUser(t, pool, 2004)
	due := time.Now().UTC().Add(72 * time.Hour).Truncate(time.Second)
	dID, _ := insertDeadlineFixture(t, uid, nil, due, nil)

	rr := NewReminders(pool)
	mins := 30
	batch := []domain.Reminder{
		{DeadlineID: dID, Kind: domain.KindCustomOffset, OffsetMinutes: &mins, FireAt: due.Add(-30 * time.Minute), Status: domain.ReminderStatusPending},
		{DeadlineID: dID, Kind: domain.KindCustomAt, FireAt: due.Add(-time.Hour), Status: domain.ReminderStatusPending},
	}
	if err := rr.CreateBatch(ctx, batch); err != nil {
		t.Fatalf("CreateBatch: %v", err)
	}
	for _, r := range batch {
		if r.ID == 0 {
			t.Errorf("CreateBatch did not fill ID: %+v", r)
		}
	}
	list, _ := rr.ListByDeadline(ctx, dID)
	if len(list) != 2 {
		t.Fatalf("rows = %d, want 2", len(list))
	}
	if err := rr.CreateBatch(ctx, nil); err != nil {
		t.Errorf("CreateBatch(nil) = %v, want nil", err)
	}
}

// TestReminderFetchDueSkipLocked — два конкурентных воркера на трёх due
// reminders: объединение = 3, пересечение = 0 (ключевая гарантия §7.2).
func TestReminderFetchDueSkipLocked(t *testing.T) {
	pool := newTestDB(t)
	ctx := t.Context()
	uid := insertUser(t, pool, 2005)
	now := time.Now().UTC()
	// Три дедлайна, у каждого reminder «просрочен» (fire_at в прошлом).
	for i := range 3 {
		dID, _ := insertDeadlineFixture(t, uid, nil, now.Add(72*time.Hour), nil)
		mins := 5
		r := domain.Reminder{
			DeadlineID: dID, Kind: domain.KindPreset, OffsetMinutes: &mins,
			FireAt: now.Add(-time.Duration(i+1) * time.Minute), Status: domain.ReminderStatusPending,
		}
		if err := NewReminders(pool).CreateBatch(ctx, []domain.Reminder{r}); err != nil {
			t.Fatal(err)
		}
	}

	rr := NewReminders(pool)
	// Барьер: обе горутины стартуют FetchDue одновременно и держат транзакции
	// открытыми, пока вторая не закончит выборку — иначе SKIP LOCKED не
	// проявится (первая уже отпустит локи коммитом).
	start := make(chan struct{})
	bothFetched := make(chan struct{})
	var once sync.Once

	type result struct {
		ids []int64
		err error
	}
	fetch := func(worker string) result {
		tx, err := pool.Begin(ctx)
		if err != nil {
			return result{err: err}
		}
		<-start
		got, err := rr.FetchDue(ctx, &pgxTxAdapter{tx: tx}, now, 10, worker)
		if err != nil {
			tx.Rollback(ctx)
			return result{err: err}
		}
		once.Do(func() { close(bothFetched) }) // первая завершила выборку
		ids := make([]int64, 0, len(got))
		for _, r := range got {
			ids = append(ids, r.ID)
			if r.LockedBy == nil || *r.LockedBy != worker {
				return result{err: fmt.Errorf("reminder %d locked_by = %v, want %s", r.ID, r.LockedBy, worker)}
			}
		}
		// Держим транзакцию, пока напарник не выберет свои строки.
		<-bothFetched
		time.Sleep(100 * time.Millisecond)
		if err := tx.Commit(ctx); err != nil {
			return result{err: err}
		}
		return result{ids: ids}
	}

	resA := make(chan result, 1)
	resB := make(chan result, 1)
	go func() { resA <- fetch("worker-A") }()
	go func() { resB <- fetch("worker-B") }()
	close(start)

	a, b := <-resA, <-resB
	if a.err != nil || b.err != nil {
		t.Fatalf("FetchDue errors: A=%v B=%v", a.err, b.err)
	}
	seen := map[int64]string{}
	for _, id := range a.ids {
		seen[id] = "A"
	}
	for _, id := range b.ids {
		if w, dup := seen[id]; dup {
			t.Errorf("reminder %d fetched by both %s and B", id, w)
		}
		seen[id] = "B"
	}
	if len(seen) != 3 {
		t.Errorf("union = %d ids (%v), want 3", len(seen), seen)
	}

	// Локи записаны в БД: после коммитов все три locked.
	var locked int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM reminders WHERE status='pending' AND locked_by IS NOT NULL`).Scan(&locked); err != nil {
		t.Fatal(err)
	}
	if locked != 3 {
		t.Errorf("locked rows = %d, want 3", locked)
	}
}

// TestReminderMarkSentIdempotent — повторный MarkSent тем же воркером и чужим
// воркером → ok=false; sent не перезаписывается.
func TestReminderMarkSentIdempotent(t *testing.T) {
	pool := newTestDB(t)
	ctx := t.Context()
	uid := insertUser(t, pool, 2006)
	// timestamptz хранит микросекунды — truncate, чтобы Equal() со sent_at сошёлся.
	now := time.Now().UTC().Truncate(time.Microsecond)
	dID, _ := insertDeadlineFixture(t, uid, nil, now.Add(72*time.Hour), nil)
	mins := 5
	batch := []domain.Reminder{{DeadlineID: dID, Kind: domain.KindPreset, OffsetMinutes: &mins, FireAt: now, Status: domain.ReminderStatusPending}}
	rr := NewReminders(pool)
	if err := rr.CreateBatch(ctx, batch); err != nil {
		t.Fatal(err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got, err := rr.FetchDue(ctx, &pgxTxAdapter{tx: tx}, now, 10, "w1")
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("FetchDue = %d, want 1", len(got))
	}

	ok, err := rr.MarkSent(ctx, got[0].ID, "w1", now.Add(time.Second))
	if err != nil || !ok {
		t.Fatalf("first MarkSent = (%v, %v), want (true, nil)", ok, err)
	}
	ok, err = rr.MarkSent(ctx, got[0].ID, "w1", now.Add(2*time.Second))
	if err != nil || ok {
		t.Errorf("second MarkSent = (%v, %v), want (false, nil)", ok, err)
	}
	ok, err = rr.MarkSent(ctx, got[0].ID, "w2", now.Add(3*time.Second))
	if err != nil || ok {
		t.Errorf("other-worker MarkSent = (%v, %v), want (false, nil)", ok, err)
	}
	var status string
	var sentAt time.Time
	if err := pool.QueryRow(ctx,
		`SELECT status, sent_at FROM reminders WHERE id=$1`, got[0].ID).Scan(&status, &sentAt); err != nil {
		t.Fatal(err)
	}
	if status != "sent" || !sentAt.Equal(now.Add(time.Second)) {
		t.Errorf("row = (%q, %v), want (sent, first-mark time)", status, sentAt)
	}
}

// TestReminderMarkFailedRetryAndExhaust — attempts растёт, retryAt пишется в
// fire_at, при исчерпании maxAttempts → failed.
func TestReminderMarkFailedRetryAndExhaust(t *testing.T) {
	pool := newTestDB(t)
	ctx := t.Context()
	uid := insertUser(t, pool, 2007)
	now := time.Now().UTC().Truncate(time.Second)
	dID, _ := insertDeadlineFixture(t, uid, nil, now.Add(72*time.Hour), nil)
	mins := 5
	r := domain.Reminder{DeadlineID: dID, Kind: domain.KindPreset, OffsetMinutes: &mins, FireAt: now, Status: domain.ReminderStatusPending}
	rr := NewReminders(pool)
	if err := rr.CreateBatch(ctx, []domain.Reminder{r}); err != nil {
		t.Fatal(err)
	}
	tx, _ := pool.Begin(ctx)
	got, err := rr.FetchDue(ctx, &pgxTxAdapter{tx: tx}, now, 10, "w1")
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	id := got[0].ID

	// Попытка 1 из max=2: остаётся pending, fire_at = retryAt, лок снят.
	retryAt := now.Add(30 * time.Second)
	failed, err := rr.MarkFailed(ctx, id, "w1", "boom", retryAt, 2)
	if err != nil || failed {
		t.Fatalf("MarkFailed#1 = (%v, %v), want (false, nil)", failed, err)
	}
	var (
		status   string
		attempts int
		fireAt   time.Time
		lastErr  *string
		lockedBy *string
	)
	if err := pool.QueryRow(ctx,
		`SELECT status, attempts, fire_at, last_error, locked_by FROM reminders WHERE id=$1`, id).
		Scan(&status, &attempts, &fireAt, &lastErr, &lockedBy); err != nil {
		t.Fatal(err)
	}
	if status != "pending" || attempts != 1 || !fireAt.Equal(retryAt) || lastErr == nil || *lastErr != "boom" || lockedBy != nil {
		t.Errorf("after retry: status=%q attempts=%d fire_at=%v last_error=%v locked_by=%v",
			status, attempts, fireAt, lastErr, lockedBy)
	}

	// Попытка 2 = max → failed. MarkFailed срабатывает только на строке,
	// запертой этим воркером, — как в реальном цикле, сначала повторный
	// FetchDue (retryAt уже наступил), затем MarkFailed.
	tx2, _ := pool.Begin(ctx)
	relocked, err := rr.FetchDue(ctx, &pgxTxAdapter{tx: tx2}, retryAt.Add(time.Second), 10, "w1")
	if err != nil {
		t.Fatal(err)
	}
	if err := tx2.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if len(relocked) != 1 || relocked[0].ID != id {
		t.Fatalf("re-fetch = %+v, want our row back in queue", relocked)
	}
	failed, err = rr.MarkFailed(ctx, id, "w1", "boom again", now.Add(time.Minute), 2)
	if err != nil || !failed {
		t.Fatalf("MarkFailed#2 = (%v, %v), want (true, nil)", failed, err)
	}
	if err := pool.QueryRow(ctx, `SELECT status, attempts FROM reminders WHERE id=$1`, id).
		Scan(&status, &attempts); err != nil {
		t.Fatal(err)
	}
	if status != "failed" || attempts != 2 {
		t.Errorf("after exhaust: status=%q attempts=%d, want failed/2", status, attempts)
	}
}

// TestReminderReleaseStale — снимает только протухшие локи pending-строк.
func TestReminderReleaseStale(t *testing.T) {
	pool := newTestDB(t)
	ctx := t.Context()
	uid := insertUser(t, pool, 2008)
	now := time.Now().UTC()
	dID, _ := insertDeadlineFixture(t, uid, nil, now.Add(72*time.Hour), nil)
	rr := NewReminders(pool)

	stale := now.Add(-10 * time.Minute)
	freshLock := now
	for i, lockAt := range []time.Time{stale, freshLock} {
		mins := 5 + i
		batch := []domain.Reminder{{DeadlineID: dID, Kind: domain.KindPreset, OffsetMinutes: &mins, FireAt: now.Add(time.Duration(i) * time.Minute), Status: domain.ReminderStatusPending}}
		if err := rr.CreateBatch(ctx, batch); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx,
			`UPDATE reminders SET locked_by='w1', locked_at=$2 WHERE id=$1`, batch[0].ID, lockAt); err != nil {
			t.Fatal(err)
		}
	}

	released, err := rr.ReleaseStale(ctx, now.Add(-2*time.Minute))
	if err != nil {
		t.Fatalf("ReleaseStale: %v", err)
	}
	if released != 1 {
		t.Errorf("released = %d, want 1", released)
	}
	list, _ := rr.ListByDeadline(ctx, dID)
	for _, r := range list {
		if *r.OffsetMinutes == 5 {
			if r.LockedBy != nil || r.LockedAt != nil || r.Attempts != 1 {
				t.Errorf("stale row not released: %+v", r)
			}
		} else if r.LockedBy == nil || *r.LockedBy != "w1" {
			t.Errorf("fresh lock released: %+v", r)
		}
	}
}

// TestRemindersRepoTxAdapter — FetchDue работает через domain.Tx-адаптер пула.
func TestRemindersRepoTxAdapter(t *testing.T) {
	pool := newTestDB(t)
	ctx := t.Context()
	uid := insertUser(t, pool, 2009)
	now := time.Now().UTC()
	dID, _ := insertDeadlineFixture(t, uid, nil, now.Add(72*time.Hour), nil)
	rr := NewReminders(pool)
	mins := 5
	r := domain.Reminder{DeadlineID: dID, Kind: domain.KindPreset, OffsetMinutes: &mins, FireAt: now.Add(-time.Minute), Status: domain.ReminderStatusPending}
	if err := rr.CreateBatch(ctx, []domain.Reminder{r}); err != nil {
		t.Fatal(err)
	}

	pgTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer pgTx.Rollback(ctx)
	got, err := rr.FetchDue(ctx, &pgxTxAdapter{tx: pgTx}, now, 10, "w-tx")
	if err != nil {
		t.Fatalf("FetchDue via adapter: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got = %d, want 1", len(got))
	}
	// Внутри той же транзакции строка ещё не видна как sent.
	var count int
	if err := pgTx.QueryRow(ctx, `SELECT count(*) FROM reminders WHERE locked_by='w-tx'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("locked in tx = %d, want 1", count)
	}
}

// pgxTxAdapter — минимальная реализация domain.Tx поверх pgx.Tx для теста.
type pgxTxAdapter struct{ tx pgx.Tx }

func (a *pgxTxAdapter) Exec(ctx context.Context, sql string, args ...any) error {
	_, err := a.tx.Exec(ctx, sql, args...)
	return err
}

func (a *pgxTxAdapter) QueryRow(ctx context.Context, sql string, args ...any) domain.Row {
	return a.tx.QueryRow(ctx, sql, args...)
}

func (a *pgxTxAdapter) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return a.tx.Query(ctx, sql, args...)
}
