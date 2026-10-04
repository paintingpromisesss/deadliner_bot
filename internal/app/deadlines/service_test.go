package deadlines

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/sauron/deadliner/internal/domain"
)

// --- fakes ---

type fakeClock struct{ now time.Time }

func (c fakeClock) Now() time.Time { return c.now }

// onCreateHook эмулирует транзакцию Create: reminders пишутся в связанный
// fakeReminderRepo тем же вызовом; сбой хука откатывает deadline.
type onCreateHook func(deadlineID int64, reminders []domain.Reminder) error

type fakeDeadlineRepo struct {
	rows     map[int64]*domain.Deadline
	nextID   int64
	deleted  map[int64]bool
	onCreate onCreateHook
	// журнал Update-вызовов для assertions компенсации.
	updateCalls []domain.DeadlinePatch
	updateErr   error
	// failUpdateCall > 0 — вернуть updateErr на N-м Update-вызове (1-based).
	failUpdateCall int
}

func newFakeDeadlineRepo() *fakeDeadlineRepo {
	return &fakeDeadlineRepo{rows: map[int64]*domain.Deadline{}, deleted: map[int64]bool{}, nextID: 1}
}

func (r *fakeDeadlineRepo) Create(ctx context.Context, d *domain.Deadline, reminders []domain.Reminder) error {
	if err := d.ValidateOwnership(); err != nil {
		return err
	}
	d.ID = r.nextID
	r.nextID++
	cp := *d
	r.rows[d.ID] = &cp
	if r.onCreate != nil {
		if err := r.onCreate(d.ID, reminders); err != nil {
			delete(r.rows, d.ID) // «откат» транзакции
			return err
		}
	}
	return nil
}

func (r *fakeDeadlineRepo) GetByID(ctx context.Context, id int64) (*domain.Deadline, error) {
	d, ok := r.rows[id]
	if !ok || r.deleted[id] {
		return nil, fmt.Errorf("%w: deadline id=%d", domain.ErrNotFound, id)
	}
	cp := *d
	return &cp, nil
}

func (r *fakeDeadlineRepo) Update(ctx context.Context, id int64, patch domain.DeadlinePatch) error {
	if _, ok := r.rows[id]; !ok || r.deleted[id] {
		return fmt.Errorf("%w: deadline id=%d", domain.ErrNotFound, id)
	}
	r.updateCalls = append(r.updateCalls, patch)
	if r.updateErr != nil && (r.failUpdateCall == 0 || r.failUpdateCall == len(r.updateCalls)) {
		return r.updateErr
	}
	cur := r.rows[id]
	if patch.Title != nil {
		cur.Title = *patch.Title
	}
	if patch.Description != nil {
		cur.Description = *patch.Description
	}
	if patch.DueAt != nil {
		cur.DueAt = *patch.DueAt
	}
	if patch.TZ != nil {
		cur.TZ = *patch.TZ
	}
	return nil
}

func (r *fakeDeadlineRepo) SetStatus(ctx context.Context, id int64, status domain.DeadlineStatus) error {
	switch status {
	case domain.DeadlineStatusActive, domain.DeadlineStatusDone, domain.DeadlineStatusArchived:
	default:
		return fmt.Errorf("%w: status %q", domain.ErrValidation, status)
	}
	if _, err := r.GetByID(ctx, id); err != nil {
		return err
	}
	r.rows[id].Status = status
	return nil
}

func (r *fakeDeadlineRepo) SoftDelete(ctx context.Context, id int64) error {
	if _, err := r.GetByID(ctx, id); err != nil {
		return err
	}
	r.deleted[id] = true
	return nil
}

func (r *fakeDeadlineRepo) ListByGroup(ctx context.Context, groupID int64, from, to *time.Time, status *domain.DeadlineStatus) ([]domain.Deadline, error) {
	return r.list(func(d *domain.Deadline) bool {
		return d.GroupID != nil && *d.GroupID == groupID
	}, from, to, status)
}

func (r *fakeDeadlineRepo) ListByOwner(ctx context.Context, ownerID int64, from, to *time.Time, status *domain.DeadlineStatus) ([]domain.Deadline, error) {
	return r.list(func(d *domain.Deadline) bool {
		return d.OwnerUserID != nil && *d.OwnerUserID == ownerID
	}, from, to, status)
}

func (r *fakeDeadlineRepo) list(match func(*domain.Deadline) bool, from, to *time.Time, status *domain.DeadlineStatus) ([]domain.Deadline, error) {
	out := []domain.Deadline{}
	for _, d := range r.rows {
		if r.deleted[d.ID] || !match(d) {
			continue
		}
		if from != nil && d.DueAt.Before(*from) {
			continue
		}
		if to != nil && d.DueAt.After(*to) {
			continue
		}
		if status != nil && d.Status != *status {
			continue
		}
		out = append(out, *d)
	}
	return out, nil
}

type fakeReminderRepo struct {
	rows   map[int64]*domain.Reminder
	nextID int64
	// журнал вызовов для assertions
	regenerateCalls []regenerateCall
	cancelCalls     []int64
	regenerateErr   error
	cancelErr       error
}

type regenerateCall struct {
	deadlineID int64
	reminders  []domain.Reminder
}

func newFakeReminderRepo() *fakeReminderRepo {
	return &fakeReminderRepo{rows: map[int64]*domain.Reminder{}, nextID: 1}
}

func (r *fakeReminderRepo) CreateBatch(ctx context.Context, reminders []domain.Reminder) error {
	for i := range reminders {
		rem := reminders[i]
		rem.ID = r.nextID
		r.nextID++
		cp := rem
		r.rows[cp.ID] = &cp
		reminders[i] = rem
	}
	return nil
}

func (r *fakeReminderRepo) ListByDeadline(ctx context.Context, deadlineID int64) ([]domain.Reminder, error) {
	out := []domain.Reminder{}
	for _, rem := range r.rows {
		if rem.DeadlineID == deadlineID {
			out = append(out, *rem)
		}
	}
	return out, nil
}

func (r *fakeReminderRepo) FetchDue(ctx context.Context, tx domain.Tx, now time.Time, limit int, workerID string) ([]domain.Reminder, error) {
	return nil, errors.New("not used in service tests")
}

func (r *fakeReminderRepo) MarkSent(ctx context.Context, id int64, workerID string, now time.Time) (bool, error) {
	return false, errors.New("not used in service tests")
}

func (r *fakeReminderRepo) MarkSentWithFanout(ctx context.Context, tx domain.Tx, reminderID int64, workerID string, now time.Time, children []domain.Reminder) (bool, error) {
	return false, errors.New("not used in service tests")
}

func (r *fakeReminderRepo) MarkFailed(ctx context.Context, id int64, workerID, errText string, retryAt time.Time, maxAttempts int) (bool, error) {
	return false, errors.New("not used in service tests")
}

func (r *fakeReminderRepo) ReleaseStale(ctx context.Context, olderThan time.Time) (int64, error) {
	return 0, errors.New("not used in service tests")
}

func (r *fakeReminderRepo) CancelByDeadline(ctx context.Context, deadlineID int64) error {
	r.cancelCalls = append(r.cancelCalls, deadlineID)
	if r.cancelErr != nil {
		return r.cancelErr
	}
	for _, rem := range r.rows {
		if rem.DeadlineID == deadlineID && rem.Status == domain.ReminderStatusPending {
			rem.Status = domain.ReminderStatusCancelled
		}
	}
	return nil
}

func (r *fakeReminderRepo) Regenerate(ctx context.Context, deadlineID int64, newReminders []domain.Reminder) (int, error) {
	r.regenerateCalls = append(r.regenerateCalls, regenerateCall{deadlineID, newReminders})
	if r.regenerateErr != nil {
		return 0, r.regenerateErr
	}
	for _, rem := range r.rows {
		if rem.DeadlineID == deadlineID && rem.Status == domain.ReminderStatusPending {
			rem.Status = domain.ReminderStatusCancelled
		}
	}
	inserted := 0
	for _, rem := range newReminders {
		nr := rem
		nr.ID = r.nextID
		r.nextID++
		r.rows[nr.ID] = &nr
		inserted++
	}
	return inserted, nil
}

type fakeGroupRepo struct {
	groups map[int64]*domain.Group
}

func newFakeGroupRepo() *fakeGroupRepo {
	return &fakeGroupRepo{groups: map[int64]*domain.Group{}}
}

func (r *fakeGroupRepo) Create(ctx context.Context, g *domain.Group) error { return nil }
func (r *fakeGroupRepo) GetByID(ctx context.Context, id int64) (*domain.Group, error) {
	g, ok := r.groups[id]
	if !ok {
		return nil, fmt.Errorf("%w: group id=%d", domain.ErrNotFound, id)
	}
	cp := *g
	return &cp, nil
}
func (r *fakeGroupRepo) GetBySlugNorm(ctx context.Context, norm string) (*domain.Group, error) {
	return nil, domain.ErrNotFound
}
func (r *fakeGroupRepo) SearchByPrefix(ctx context.Context, prefix string, callerID int64, limit int) ([]domain.Group, error) {
	return nil, nil
}
func (r *fakeGroupRepo) Update(ctx context.Context, g *domain.Group) error { return nil }
func (r *fakeGroupRepo) SetStatus(ctx context.Context, id int64, status domain.GroupStatus) error {
	return nil
}
func (r *fakeGroupRepo) SoftDelete(ctx context.Context, id int64) error { return nil }
func (r *fakeGroupRepo) HardDelete(ctx context.Context, id int64) error { return nil }
func (r *fakeGroupRepo) ListMine(ctx context.Context, userID int64) ([]domain.Group, error) {
	return nil, nil
}
func (r *fakeGroupRepo) ListPendingExpired(ctx context.Context, now time.Time, limit int) ([]domain.Group, error) {
	return nil, nil
}

// ListAll — часть domain.GroupRepo, нужная только CLI `admin list-groups`;
// сервисам этих пакетов не требуется.
func (r *fakeGroupRepo) ListAll(ctx context.Context, status *domain.GroupStatus, limit int) ([]domain.Group, error) {
	return nil, nil
}

type memKey struct{ groupID, userID int64 }

type fakeMembershipRepo struct {
	mems map[memKey]*domain.Membership
}

func newFakeMembershipRepo() *fakeMembershipRepo {
	return &fakeMembershipRepo{mems: map[memKey]*domain.Membership{}}
}

func (r *fakeMembershipRepo) add(groupID, userID int64, role domain.Role) {
	r.mems[memKey{groupID, userID}] = &domain.Membership{GroupID: groupID, UserID: userID, Role: role}
}

func (r *fakeMembershipRepo) Upsert(ctx context.Context, m *domain.Membership) error { return nil }
func (r *fakeMembershipRepo) Get(ctx context.Context, groupID, userID int64) (*domain.Membership, error) {
	m, ok := r.mems[memKey{groupID, userID}]
	if !ok {
		return nil, fmt.Errorf("%w: membership group=%d user=%d", domain.ErrNotFound, groupID, userID)
	}
	cp := *m
	return &cp, nil
}
func (r *fakeMembershipRepo) ListByGroup(ctx context.Context, groupID int64) ([]domain.Membership, error) {
	return nil, nil
}
func (r *fakeMembershipRepo) ListByGroupDetailed(ctx context.Context, groupID int64) ([]domain.MembershipDetail, error) {
	return nil, nil
}
func (r *fakeMembershipRepo) ListByUser(ctx context.Context, userID int64) ([]domain.Membership, error) {
	out := []domain.Membership{}
	for k, m := range r.mems {
		if k.userID == userID {
			out = append(out, *m)
		}
	}
	return out, nil
}
func (r *fakeMembershipRepo) SetRole(ctx context.Context, groupID, userID int64, role domain.Role) error {
	return nil
}
func (r *fakeMembershipRepo) DemoteIfNotLastAdmin(ctx context.Context, groupID, userID int64) error {
	return nil
}
func (r *fakeMembershipRepo) RemoveIfNotLastAdmin(ctx context.Context, groupID, userID int64) error {
	return nil
}
func (r *fakeMembershipRepo) SetDMNotify(ctx context.Context, groupID, userID int64, dm *bool) error {
	return nil
}

func (r *fakeMembershipRepo) ListDMTargets(ctx context.Context, groupID int64) ([]int64, error) {
	return nil, errors.New("not used")
}
func (r *fakeMembershipRepo) Delete(ctx context.Context, groupID, userID int64) error { return nil }
func (r *fakeMembershipRepo) CountAdmins(ctx context.Context, groupID int64) (int, error) {
	return 0, nil
}

type fakeAuditRepo struct{ entries []*domain.AuditEntry }

func (r *fakeAuditRepo) Write(ctx context.Context, e *domain.AuditEntry) error {
	r.entries = append(r.entries, e)
	return nil
}

// --- helpers ---

type testEnv struct {
	svc       *Service
	deadlines *fakeDeadlineRepo
	reminders *fakeReminderRepo
	groups    *fakeGroupRepo
	members   *fakeMembershipRepo
	bindings  *fakeBindingRepo
	users     *fakeUserRepo
	audit     *fakeAuditRepo
	clock     *fakeClock
	logs      *bytes.Buffer
}

// fakeBindingRepo — привязки чатов (для announceGroup при апруве).
type fakeBindingRepo struct {
	byGroup map[int64]*domain.ChatBinding
}

func newFakeBindingRepo() *fakeBindingRepo {
	return &fakeBindingRepo{byGroup: map[int64]*domain.ChatBinding{}}
}

func (r *fakeBindingRepo) Create(ctx context.Context, b *domain.ChatBinding) error { return nil }
func (r *fakeBindingRepo) GetByGroup(ctx context.Context, groupID int64) (*domain.ChatBinding, error) {
	b, ok := r.byGroup[groupID]
	if !ok {
		return nil, fmt.Errorf("%w: binding group=%d", domain.ErrNotFound, groupID)
	}
	cp := *b
	return &cp, nil
}
func (r *fakeBindingRepo) GetByChat(ctx context.Context, chatID int64, threadID *int64) (*domain.ChatBinding, error) {
	return nil, domain.ErrNotFound
}
func (r *fakeBindingRepo) Delete(ctx context.Context, groupID int64) error { return nil }

// fakeUserRepo — ровно то, что читает notifyAdminsPending (GetByID).
type fakeUserRepo struct {
	users map[int64]*domain.User
}

func newFakeUserRepo() *fakeUserRepo {
	return &fakeUserRepo{users: map[int64]*domain.User{}}
}

func (r *fakeUserRepo) GetByID(ctx context.Context, id int64) (*domain.User, error) {
	u, ok := r.users[id]
	if !ok {
		return nil, fmt.Errorf("%w: user id=%d", domain.ErrNotFound, id)
	}
	cp := *u
	return &cp, nil
}

func (r *fakeUserRepo) GetByTelegramID(ctx context.Context, telegramID int64) (*domain.User, error) {
	return nil, domain.ErrNotFound
}
func (r *fakeUserRepo) UpsertByTelegram(ctx context.Context, u *domain.User) error { return nil }
func (r *fakeUserRepo) UpdateSettings(ctx context.Context, id int64, tz string, dm bool) error {
	return nil
}
func (r *fakeUserRepo) UpdateProfile(ctx context.Context, id int64, firstName string) error {
	return nil
}
func (r *fakeUserRepo) SetBanned(ctx context.Context, id int64, banned bool) error { return nil }
func (r *fakeUserRepo) SetSuperadmin(ctx context.Context, id int64, sa bool) error { return nil }
func (r *fakeUserRepo) MarkBotBlocked(ctx context.Context, telegramID int64, b bool) error {
	return nil
}
func (r *fakeUserRepo) ListSuperadmins(ctx context.Context) ([]domain.User, error) {
	return nil, nil
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	rr := newFakeReminderRepo()
	dr := newFakeDeadlineRepo()
	dr.onCreate = func(deadlineID int64, reminders []domain.Reminder) error {
		for i := range reminders {
			reminders[i].DeadlineID = deadlineID
		}
		return rr.CreateBatch(context.Background(), reminders)
	}
	env := &testEnv{
		deadlines: dr,
		reminders: rr,
		groups:    newFakeGroupRepo(),
		members:   newFakeMembershipRepo(),
		bindings:  newFakeBindingRepo(),
		users:     newFakeUserRepo(),
		audit:     &fakeAuditRepo{},
		clock:     &fakeClock{now: time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)},
		logs:      &bytes.Buffer{},
	}
	env.svc = NewService(dr, rr, env.groups, env.members, env.bindings, env.users, env.audit, env.clock,
		slog.New(slog.NewTextHandler(env.logs, &slog.HandlerOptions{Level: slog.LevelError})))
	return env
}

func (e *testEnv) addGroup(id int64, presets []time.Duration) {
	e.groups.groups[id] = &domain.Group{ID: id, Status: domain.GroupStatusActive, DefaultPresets: presets}
}

func user(id int64, superadmin bool) *domain.User {
	return &domain.User{ID: id, TZ: "Europe/Moscow", IsSuperadmin: superadmin}
}

func intPtr(v int) *int              { return &v }
func timePtr(v time.Time) *time.Time { return &v }
func i64Ptr(v int64) *int64          { return &v }

// --- tests ---

func TestCreatePersonalHappyPath(t *testing.T) {
	env := newTestEnv(t)
	actor := user(1, false)
	due := env.clock.now.Add(72 * time.Hour)

	view, err := env.svc.Create(t.Context(), actor, CreateInput{
		Title: "Курсовая", Description: "по БД", DueAt: due,
		Reminders: []ReminderSpec{
			{Kind: domain.KindPreset, OffsetMinutes: intPtr(1440)},
			{Kind: domain.KindCustomAt, FireAt: timePtr(due.Add(-time.Hour))},
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if view.Deadline.ID == 0 || view.Deadline.OwnerUserID == nil || *view.Deadline.OwnerUserID != 1 {
		t.Errorf("deadline: %+v", view.Deadline)
	}
	if view.Deadline.TZ != "Europe/Moscow" {
		t.Errorf("tz = %q, want actor default", view.Deadline.TZ)
	}
	if len(view.Reminders) != 2 {
		t.Fatalf("reminders = %d, want 2 (preset 24h + custom_at)", len(view.Reminders))
	}
	// Личный дедлайн без явных reminders — никаких пресетов.
	view2, err := env.svc.Create(t.Context(), actor, CreateInput{Title: "Без напоминаний", DueAt: due})
	if err != nil {
		t.Fatal(err)
	}
	if len(view2.Reminders) != 0 {
		t.Errorf("personal without specs: %d reminders, want 0", len(view2.Reminders))
	}
}

func TestCreateGroupUsesGroupPresets(t *testing.T) {
	env := newTestEnv(t)
	admin := user(1, false)
	env.addGroup(10, []time.Duration{7 * 24 * time.Hour, 24 * time.Hour})
	env.members.add(10, 1, domain.RoleAdmin)
	due := env.clock.now.Add(30 * 24 * time.Hour)

	view, err := env.svc.Create(t.Context(), admin, CreateInput{
		GroupID: i64Ptr(10), Title: "Экзамен", DueAt: due,
	})
	if err != nil {
		t.Fatalf("Create group: %v", err)
	}
	if len(view.Reminders) != 2 {
		t.Errorf("reminders = %d, want 2 group presets", len(view.Reminders))
	}
	for _, r := range view.Reminders {
		if r.Kind != domain.KindPreset {
			t.Errorf("kind = %q, want preset", r.Kind)
		}
	}

	// Явные reminders переопределяют пресеты группы.
	view2, err := env.svc.Create(t.Context(), admin, CreateInput{
		GroupID: i64Ptr(10), Title: "Лаб", DueAt: due,
		Reminders: []ReminderSpec{{Kind: domain.KindPreset, OffsetMinutes: intPtr(60)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(view2.Reminders) != 1 || *view2.Reminders[0].OffsetMinutes != 60 {
		t.Errorf("explicit reminders: %+v, want single 60min", view2.Reminders)
	}
}

func TestCreatePermissionsMatrix(t *testing.T) {
	env := newTestEnv(t)
	admin := user(1, false)
	member := user(2, false)
	outsider := user(3, false)
	super := user(4, true)
	env.addGroup(10, []time.Duration{time.Hour})
	env.members.add(10, 1, domain.RoleAdmin)
	env.members.add(10, 2, domain.RoleMember)
	due := env.clock.now.Add(72 * time.Hour)
	in := CreateInput{GroupID: i64Ptr(10), Title: "T", DueAt: due}

	// member создаёт групповой дедлайн в pending_approval (модерация).
	view, err := env.svc.Create(t.Context(), member, in)
	if err != nil {
		t.Fatalf("member create = %v, want nil", err)
	}
	if view.Deadline.Status != domain.DeadlineStatusPendingApproval {
		t.Errorf("member deadline status = %q, want pending_approval", view.Deadline.Status)
	}
	if len(view.Reminders) != 0 {
		t.Errorf("member deadline reminders = %d, want 0 (напоминания — после апрува)", len(view.Reminders))
	}
	// не-участник тоже.
	if _, err := env.svc.Create(t.Context(), outsider, in); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("outsider create = %v, want ErrForbidden", err)
	}
	// admin может.
	if _, err := env.svc.Create(t.Context(), admin, in); err != nil {
		t.Errorf("admin create = %v", err)
	}
	// superadmin может без membership.
	if _, err := env.svc.Create(t.Context(), super, in); err != nil {
		t.Errorf("superadmin create = %v", err)
	}
	// несуществующая группа → ErrNotFound.
	if _, err := env.svc.Create(t.Context(), super, CreateInput{GroupID: i64Ptr(99), Title: "T", DueAt: due}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("missing group = %v, want ErrNotFound", err)
	}
}

func TestCreateValidationBounds(t *testing.T) {
	env := newTestEnv(t)
	actor := user(1, false)
	now := env.clock.now
	ctx := t.Context()

	cases := []struct {
		name string
		in   CreateInput
	}{
		{"empty title", CreateInput{Title: "   ", DueAt: now.Add(time.Hour)}},
		{"long title", CreateInput{Title: string(make([]rune, 201)), DueAt: now.Add(time.Hour)}},
		{"long description", CreateInput{Title: "T", Description: string(make([]rune, 2001)), DueAt: now.Add(time.Hour)}},
		{"due in past", CreateInput{Title: "T", DueAt: now.Add(-time.Hour)}},
		{"due now", CreateInput{Title: "T", DueAt: now}},
		{"due > 5y", CreateInput{Title: "T", DueAt: now.Add(6 * 365 * 24 * time.Hour)}},
		{"11 reminders", CreateInput{Title: "T", DueAt: now.Add(72 * time.Hour),
			Reminders: func() []ReminderSpec {
				out := make([]ReminderSpec, 11)
				for i := range out {
					out[i] = ReminderSpec{Kind: domain.KindCustomAt, FireAt: timePtr(now.Add(time.Duration(i+1) * time.Hour))}
				}
				return out
			}()}},
		{"offset < 5min", CreateInput{Title: "T", DueAt: now.Add(72 * time.Hour),
			Reminders: []ReminderSpec{{Kind: domain.KindPreset, OffsetMinutes: intPtr(4)}}}},
		{"custom_at past", CreateInput{Title: "T", DueAt: now.Add(72 * time.Hour),
			Reminders: []ReminderSpec{{Kind: domain.KindCustomAt, FireAt: timePtr(now.Add(-time.Hour))}}}},
		{"bad tz", CreateInput{Title: "T", DueAt: now.Add(72 * time.Hour), TZ: "Mars/Olympus"}},
		{"bad kind", CreateInput{Title: "T", DueAt: now.Add(72 * time.Hour),
			Reminders: []ReminderSpec{{Kind: "bogus"}}}},
		{"preset without offset", CreateInput{Title: "T", DueAt: now.Add(72 * time.Hour),
			Reminders: []ReminderSpec{{Kind: domain.KindPreset}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := env.svc.Create(ctx, actor, tc.in); !errors.Is(err, domain.ErrValidation) {
				t.Errorf("= %v, want ErrValidation", err)
			}
		})
	}

	// 200 рун в title — валидно (граница включительно).
	ok := CreateInput{Title: string(make([]rune, 200)), DueAt: now.Add(72 * time.Hour)}
	for i := range []rune(ok.Title) {
		_ = i
	}
	title200 := make([]rune, 200)
	for i := range title200 {
		title200[i] = 'я'
	}
	ok.Title = string(title200)
	if _, err := env.svc.Create(ctx, actor, ok); err != nil {
		t.Errorf("200-rune title = %v, want ok", err)
	}
}

func TestCreateSkipsPastFireAtSilently(t *testing.T) {
	env := newTestEnv(t)
	actor := user(1, false)
	// due через 30 минут: пресет 24h «в прошлом» относительно due → не создаётся.
	due := env.clock.now.Add(30 * time.Minute)
	view, err := env.svc.Create(t.Context(), actor, CreateInput{
		Title: "Скоро", DueAt: due,
		Reminders: []ReminderSpec{
			{Kind: domain.KindPreset, OffsetMinutes: intPtr(1440)},
			{Kind: domain.KindPreset, OffsetMinutes: intPtr(10)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Reminders) != 1 || *view.Reminders[0].OffsetMinutes != 10 {
		t.Errorf("reminders = %+v, want only 10min", view.Reminders)
	}
}

func TestUpdateDueChangeRegenerates(t *testing.T) {
	env := newTestEnv(t)
	actor := user(1, false)
	due := env.clock.now.Add(72 * time.Hour)
	view, err := env.svc.Create(t.Context(), actor, CreateInput{
		Title: "Курсовая", DueAt: due,
		Reminders: []ReminderSpec{
			{Kind: domain.KindPreset, OffsetMinutes: intPtr(1440)},
			{Kind: domain.KindCustomOffset, OffsetMinutes: intPtr(120)},
			{Kind: domain.KindCustomAt, FireAt: timePtr(env.clock.now.Add(10 * time.Hour))},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Reminders) != 3 {
		t.Fatalf("setup reminders = %d, want 3", len(view.Reminders))
	}

	newDue := due.Add(24 * time.Hour)
	updated, err := env.svc.Update(t.Context(), actor, view.Deadline.ID,
		UpdateInput{DueAt: &newDue})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(env.reminders.regenerateCalls) != 1 {
		t.Fatalf("Regenerate calls = %d, want 1", len(env.reminders.regenerateCalls))
	}
	call := env.reminders.regenerateCalls[0]
	if call.deadlineID != view.Deadline.ID {
		t.Errorf("Regenerate deadline = %d", call.deadlineID)
	}
	// preset 1440 пересчитан от нового due; custom_offset 120 сохранён и тоже
	// пересчитан; custom_at воссоздан как есть (fire_at в будущем).
	kinds := map[domain.ReminderKind]int{}
	for _, r := range call.reminders {
		kinds[r.Kind]++
		switch r.Kind {
		case domain.KindPreset:
			if want := newDue.Add(-24 * time.Hour); !r.FireAt.Equal(want) {
				t.Errorf("preset fire_at = %v, want %v", r.FireAt, want)
			}
		case domain.KindCustomOffset:
			if want := newDue.Add(-2 * time.Hour); !r.FireAt.Equal(want) {
				t.Errorf("custom_offset fire_at = %v, want %v", r.FireAt, want)
			}
		case domain.KindCustomAt:
			if want := env.clock.now.Add(10 * time.Hour); !r.FireAt.Equal(want) {
				t.Errorf("custom_at fire_at = %v, want unchanged %v", r.FireAt, want)
			}
		}
	}
	if kinds[domain.KindPreset] != 1 || kinds[domain.KindCustomOffset] != 1 || kinds[domain.KindCustomAt] != 1 {
		t.Errorf("kinds = %v, want 1/1/1", kinds)
	}
	if !updated.Deadline.DueAt.Equal(newDue) {
		t.Errorf("due = %v, want %v", updated.Deadline.DueAt, newDue)
	}
	// Итоговый набор pending: ровно три пересозданных reminders; старых нет.
	pending := map[domain.ReminderKind]time.Time{}
	for _, r := range updated.Reminders {
		if r.Status == domain.ReminderStatusPending {
			if _, dup := pending[r.Kind]; dup {
				t.Errorf("duplicate pending %s reminder", r.Kind)
			}
			pending[r.Kind] = r.FireAt
		}
	}
	if len(pending) != 3 {
		t.Errorf("pending kinds = %v, want preset/custom_offset/custom_at", pending)
	}
	if want := newDue.Add(-24 * time.Hour); !pending[domain.KindPreset].Equal(want) {
		t.Errorf("final preset fire_at = %v, want %v", pending[domain.KindPreset], want)
	}
}

func TestUpdateDueChangeDropsPastCustomAt(t *testing.T) {
	env := newTestEnv(t)
	actor := user(1, false)
	due := env.clock.now.Add(2 * time.Hour)
	view, err := env.svc.Create(t.Context(), actor, CreateInput{
		Title: "T", DueAt: env.clock.now.Add(72 * time.Hour),
		Reminders: []ReminderSpec{
			{Kind: domain.KindCustomAt, FireAt: timePtr(due)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Сдвигаем часы так, что custom_at (now+2h) оказывается в прошлом.
	env.clock.now = env.clock.now.Add(3 * time.Hour)
	newDue := env.clock.now.Add(72 * time.Hour)
	_, err = env.svc.Update(t.Context(), actor, view.Deadline.ID, UpdateInput{DueAt: &newDue})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	call := env.reminders.regenerateCalls[0]
	for _, r := range call.reminders {
		if r.Kind == domain.KindCustomAt {
			t.Errorf("past custom_at recreated: %+v", r)
		}
	}
}

func TestUpdateTitleOnlyNoRegeneration(t *testing.T) {
	env := newTestEnv(t)
	actor := user(1, false)
	due := env.clock.now.Add(72 * time.Hour)
	view, _ := env.svc.Create(t.Context(), actor, CreateInput{
		Title: "T", DueAt: due,
		Reminders: []ReminderSpec{{Kind: domain.KindPreset, OffsetMinutes: intPtr(60)}},
	})
	title := "Новое имя"
	descr := "и описание"
	if _, err := env.svc.Update(t.Context(), actor, view.Deadline.ID,
		UpdateInput{Title: &title, Description: &descr}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(env.reminders.regenerateCalls) != 0 {
		t.Errorf("Regenerate on title-only change: %+v", env.reminders.regenerateCalls)
	}
	got, _ := env.svc.Get(t.Context(), actor, view.Deadline.ID)
	if got.Deadline.Title != title || got.Deadline.Description != descr {
		t.Errorf("after update: %+v", got.Deadline)
	}
}

func TestUpdatePermissions(t *testing.T) {
	env := newTestEnv(t)
	owner := user(1, false)
	stranger := user(2, false)
	super := user(3, true)
	due := env.clock.now.Add(72 * time.Hour)
	personal, _ := env.svc.Create(t.Context(), owner, CreateInput{Title: "T", DueAt: due})

	newTitle := "взлом"
	if _, err := env.svc.Update(t.Context(), stranger, personal.Deadline.ID,
		UpdateInput{Title: &newTitle}); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("stranger update personal = %v, want ErrForbidden", err)
	}
	if _, err := env.svc.Update(t.Context(), super, personal.Deadline.ID,
		UpdateInput{Title: &newTitle}); err != nil {
		t.Errorf("superadmin update personal = %v", err)
	}

	// Групповой: member не может, admin может.
	env.addGroup(10, nil)
	admin := user(4, false)
	member := user(5, false)
	env.members.add(10, 4, domain.RoleAdmin)
	env.members.add(10, 5, domain.RoleMember)
	groupDl, err := env.svc.Create(t.Context(), admin, CreateInput{GroupID: i64Ptr(10), Title: "G", DueAt: due})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.svc.Update(t.Context(), member, groupDl.Deadline.ID,
		UpdateInput{Title: &newTitle}); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("member update group deadline = %v, want ErrForbidden", err)
	}
	if _, err := env.svc.Update(t.Context(), admin, groupDl.Deadline.ID,
		UpdateInput{Title: &newTitle}); err != nil {
		t.Errorf("admin update group deadline = %v", err)
	}
	// Update в прошлом → валидация.
	past := env.clock.now.Add(-time.Hour)
	if _, err := env.svc.Update(t.Context(), owner, personal.Deadline.ID,
		UpdateInput{DueAt: &past}); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("update due to past = %v, want ErrValidation", err)
	}
}

// Спека §5.2 «автор/admin»: автор группового дедлайна сохраняет право записи,
// даже если перестал быть админом группы (смена старосты — штатный сценарий,
// §3.1). Права на ЧУЖИЕ дедлайны при этом остаются у админа.
func TestGroupDeadlineAuthorKeepsWriteAccess(t *testing.T) {
	env := newTestEnv(t)
	due := env.clock.now.Add(72 * time.Hour)

	author := user(4, false)
	other := user(5, false)
	env.addGroup(10, nil)
	env.members.add(10, 4, domain.RoleAdmin)
	env.members.add(10, 5, domain.RoleMember)

	created, err := env.svc.Create(t.Context(), author, CreateInput{GroupID: i64Ptr(10), Title: "G", DueAt: due})
	if err != nil {
		t.Fatal(err)
	}
	if created.Deadline.CreatedBy != author.ID {
		t.Fatalf("CreatedBy = %d, want the author %d", created.Deadline.CreatedBy, author.ID)
	}

	// Автор понижен до участника (например, после claim нового старосты).
	env.members.add(10, 4, domain.RoleMember)

	newTitle := "правка автора"
	if _, err := env.svc.Update(t.Context(), author, created.Deadline.ID,
		UpdateInput{Title: &newTitle}); err != nil {
		t.Errorf("author update own group deadline = %v, want nil (spec §5.2)", err)
	}
	if _, err := env.svc.Complete(t.Context(), author, created.Deadline.ID); err != nil {
		t.Errorf("author complete own group deadline = %v, want nil", err)
	}

	// Второй дедлайн — тот же автор, проверяем Delete отдельно (Complete уже
	// перевёл первый в done).
	second, err := env.svc.Create(t.Context(), other, CreateInput{GroupID: i64Ptr(10), Title: "G2", DueAt: due})
	if err != nil {
		// other — member, создавать групповые дедлайны ему нельзя: делаем
		// админом на время создания и возвращаем member.
		env.members.add(10, 5, domain.RoleAdmin)
		second, err = env.svc.Create(t.Context(), other, CreateInput{GroupID: i64Ptr(10), Title: "G2", DueAt: due})
		if err != nil {
			t.Fatal(err)
		}
		env.members.add(10, 5, domain.RoleMember)
	}
	if err := env.svc.Delete(t.Context(), other, second.Deadline.ID); err != nil {
		t.Errorf("author delete own group deadline = %v, want nil", err)
	}

	// ЧУЖОЙ дедлайн для участника по-прежнему закрыт: автор второго — other,
	// author к нему отношения не имеет (но он уже удалён, поэтому берём третий).
	third, err := env.svc.Create(t.Context(), other, CreateInput{GroupID: i64Ptr(10), Title: "G3", DueAt: due})
	if err != nil {
		env.members.add(10, 5, domain.RoleAdmin)
		third, err = env.svc.Create(t.Context(), other, CreateInput{GroupID: i64Ptr(10), Title: "G3", DueAt: due})
		if err != nil {
			t.Fatal(err)
		}
		env.members.add(10, 5, domain.RoleMember)
	}
	if _, err := env.svc.Update(t.Context(), author, third.Deadline.ID,
		UpdateInput{Title: &newTitle}); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("member update another's group deadline = %v, want ErrForbidden", err)
	}
}

// Персональный дедлайн: право записи по-прежнему только у владельца (автор и
// владелец здесь совпадают — тест фиксирует, что новое правило не расширило
// доступ к чужим личным дедлайнам).
func TestPersonalDeadlineWriteAccessUnchanged(t *testing.T) {
	env := newTestEnv(t)
	owner := user(1, false)
	stranger := user(2, false)
	due := env.clock.now.Add(72 * time.Hour)

	created, err := env.svc.Create(t.Context(), owner, CreateInput{Title: "P", DueAt: due})
	if err != nil {
		t.Fatal(err)
	}
	title := "чужое"
	if _, err := env.svc.Update(t.Context(), stranger, created.Deadline.ID,
		UpdateInput{Title: &title}); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("stranger update personal = %v, want ErrForbidden", err)
	}
	if err := env.svc.Delete(t.Context(), stranger, created.Deadline.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("stranger delete personal = %v, want ErrForbidden", err)
	}
}

func TestDeleteCancelsReminders(t *testing.T) {
	env := newTestEnv(t)
	actor := user(1, false)
	due := env.clock.now.Add(72 * time.Hour)
	view, _ := env.svc.Create(t.Context(), actor, CreateInput{
		Title: "T", DueAt: due,
		Reminders: []ReminderSpec{{Kind: domain.KindPreset, OffsetMinutes: intPtr(60)}},
	})
	if err := env.svc.Delete(t.Context(), actor, view.Deadline.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(env.reminders.cancelCalls) != 1 || env.reminders.cancelCalls[0] != view.Deadline.ID {
		t.Errorf("cancel calls = %v", env.reminders.cancelCalls)
	}
	if _, err := env.svc.Get(t.Context(), actor, view.Deadline.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Get after delete = %v, want ErrNotFound", err)
	}
	// Чужой удалить не может.
	view2, _ := env.svc.Create(t.Context(), actor, CreateInput{Title: "T2", DueAt: due})
	if err := env.svc.Delete(t.Context(), user(9, false), view2.Deadline.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("stranger delete = %v, want ErrForbidden", err)
	}
}

func TestCompleteCancelsPending(t *testing.T) {
	env := newTestEnv(t)
	actor := user(1, false)
	due := env.clock.now.Add(72 * time.Hour)
	view, _ := env.svc.Create(t.Context(), actor, CreateInput{
		Title: "T", DueAt: due,
		Reminders: []ReminderSpec{{Kind: domain.KindPreset, OffsetMinutes: intPtr(60)}},
	})
	got, err := env.svc.Complete(t.Context(), actor, view.Deadline.ID)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got.Deadline.Status != domain.DeadlineStatusDone {
		t.Errorf("status = %q, want done", got.Deadline.Status)
	}
	if len(env.reminders.cancelCalls) != 1 {
		t.Errorf("cancel calls = %v, want 1", env.reminders.cancelCalls)
	}
	for _, r := range got.Reminders {
		if r.Status == domain.ReminderStatusPending {
			t.Errorf("pending reminder survived complete: %+v", r)
		}
	}
}

func TestListMineScopeAll(t *testing.T) {
	env := newTestEnv(t)
	actor := user(1, false)
	env.addGroup(10, nil)
	env.addGroup(20, nil)
	env.members.add(10, 1, domain.RoleAdmin)
	env.members.add(20, 1, domain.RoleAdmin)
	now := env.clock.now

	personal, _ := env.svc.Create(t.Context(), actor, CreateInput{Title: "P", DueAt: now.Add(2 * time.Hour)})
	if _, err := env.svc.Create(t.Context(), actor, CreateInput{GroupID: i64Ptr(10), Title: "G10", DueAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, err := env.svc.Create(t.Context(), actor, CreateInput{GroupID: i64Ptr(20), Title: "G20", DueAt: now.Add(3 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	// Чужая группа, где actor не участник — не должна попасть в список.
	env.addGroup(30, nil)
	env.members.add(30, 77, domain.RoleAdmin)

	// Только личные.
	only, err := env.svc.ListMine(t.Context(), actor, ListQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(only) != 1 || only[0].ID != personal.Deadline.ID {
		t.Errorf("personal-only = %+v", only)
	}

	// scope=all: личные + групповые, сортировка due_at ASC.
	all, err := env.svc.ListMine(t.Context(), actor, ListQuery{Scope: "all"})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("scope=all = %d, want 3", len(all))
	}
	if all[0].Title != "G10" || all[1].Title != "P" || all[2].Title != "G20" {
		t.Errorf("order = %s,%s,%s, want due ASC", all[0].Title, all[1].Title, all[2].Title)
	}

	// Фильтр status.
	st := domain.DeadlineStatusDone
	if _, err := env.svc.Complete(t.Context(), actor, personal.Deadline.ID); err != nil {
		t.Fatal(err)
	}
	done, err := env.svc.ListMine(t.Context(), actor, ListQuery{Status: &st})
	if err != nil {
		t.Fatal(err)
	}
	if len(done) != 1 || done[0].Title != "P" {
		t.Errorf("status filter = %+v", done)
	}
}

func TestListGroupReadAccess(t *testing.T) {
	env := newTestEnv(t)
	admin := user(1, false)
	member := user(2, false)
	outsider := user(3, false)
	env.addGroup(10, nil)
	env.members.add(10, 1, domain.RoleAdmin)
	env.members.add(10, 2, domain.RoleMember)
	now := env.clock.now
	env.svc.Create(t.Context(), admin, CreateInput{GroupID: i64Ptr(10), Title: "G", DueAt: now.Add(time.Hour)})

	// member читает.
	if list, err := env.svc.ListGroup(t.Context(), member, 10, ListQuery{}); err != nil || len(list) != 1 {
		t.Errorf("member ListGroup = (%v, %v)", list, err)
	}
	// не-участник — 403.
	if _, err := env.svc.ListGroup(t.Context(), outsider, 10, ListQuery{}); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("outsider ListGroup = %v, want ErrForbidden", err)
	}
	// Get персонального чужого — 403.
	p, _ := env.svc.Create(t.Context(), admin, CreateInput{Title: "P", DueAt: now.Add(time.Hour)})
	if _, err := env.svc.Get(t.Context(), member, p.Deadline.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("member Get other's personal = %v, want ErrForbidden", err)
	}
}

func TestGetReturnsReminders(t *testing.T) {
	env := newTestEnv(t)
	actor := user(1, false)
	due := env.clock.now.Add(72 * time.Hour)
	view, _ := env.svc.Create(t.Context(), actor, CreateInput{
		Title: "T", DueAt: due,
		Reminders: []ReminderSpec{{Kind: domain.KindPreset, OffsetMinutes: intPtr(60)}},
	})
	got, err := env.svc.Get(t.Context(), actor, view.Deadline.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Reminders) != 1 {
		t.Errorf("reminders = %d, want 1", len(got.Reminders))
	}
	if _, err := env.svc.Get(t.Context(), actor, 999); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Get missing = %v, want ErrNotFound", err)
	}
}

// errRegenerate — инжектируемый сбой Regenerate для тестов компенсации.
var errRegenerate = errors.New("regenerate boom")

func TestUpdateRegenerateFailureCompensatesDueAt(t *testing.T) {
	env := newTestEnv(t)
	actor := user(1, false)
	due := env.clock.now.Add(72 * time.Hour)
	view, err := env.svc.Create(t.Context(), actor, CreateInput{
		Title: "Курсовая", DueAt: due,
		Reminders: []ReminderSpec{{Kind: domain.KindPreset, OffsetMinutes: intPtr(1440)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	id := view.Deadline.ID

	// Сбрасываем журнал Update и ломаем Regenerate.
	env.deadlines.updateCalls = nil
	env.reminders.regenerateErr = errRegenerate
	newDue := due.Add(24 * time.Hour)
	title := "Новое имя"
	_, err = env.svc.Update(t.Context(), actor, id,
		UpdateInput{DueAt: &newDue, Title: &title})
	if !errors.Is(err, errRegenerate) {
		t.Fatalf("Update = %v, want errRegenerate", err)
	}
	if len(env.reminders.regenerateCalls) != 1 {
		t.Fatalf("Regenerate calls = %d, want 1", len(env.reminders.regenerateCalls))
	}

	// Компенсирующий Update вызван со СТАРЫМИ due_at и title.
	if len(env.deadlines.updateCalls) != 2 {
		t.Fatalf("Update calls = %d, want 2 (apply + compensate): %+v",
			len(env.deadlines.updateCalls), env.deadlines.updateCalls)
	}
	comp := env.deadlines.updateCalls[1]
	if comp.DueAt == nil || !comp.DueAt.Equal(due) {
		t.Errorf("compensating due_at = %v, want old %v", comp.DueAt, due)
	}
	if comp.Title == nil || *comp.Title != "Курсовая" {
		t.Errorf("compensating title = %v, want old", comp.Title)
	}
	// Состояние в репо откатилось.
	got, _ := env.svc.Get(t.Context(), actor, id)
	if !got.Deadline.DueAt.Equal(due) || got.Deadline.Title != "Курсовая" {
		t.Errorf("deadline after compensation: %+v", got.Deadline)
	}
	// Компенсация успешна — ошибок в логе нет.
	if env.logs.Len() != 0 {
		t.Errorf("unexpected error log: %s", env.logs)
	}
}

func TestUpdateCompensationFailureLogsAndReturnsOriginalError(t *testing.T) {
	env := newTestEnv(t)
	actor := user(1, false)
	due := env.clock.now.Add(72 * time.Hour)
	view, err := env.svc.Create(t.Context(), actor, CreateInput{
		Title: "T", DueAt: due,
		Reminders: []ReminderSpec{{Kind: domain.KindPreset, OffsetMinutes: intPtr(60)}},
	})
	if err != nil {
		t.Fatal(err)
	}

	env.reminders.regenerateErr = errRegenerate
	// Компенсация тоже падает: первый Update (apply) проходит, второй (revert)
	// возвращает ошибку.
	compensateErr := errors.New("compensate boom")
	env.deadlines.updateErr = compensateErr
	env.deadlines.failUpdateCall = 2
	env.deadlines.updateCalls = nil

	newDue := due.Add(24 * time.Hour)
	_, err = env.svc.Update(t.Context(), actor, view.Deadline.ID, UpdateInput{DueAt: &newDue})
	if !errors.Is(err, errRegenerate) {
		t.Fatalf("Update = %v, want original errRegenerate (не compensateErr)", err)
	}
	// Сбой компенсации залогирован с id дедлайна.
	logged := env.logs.String()
	if !strings.Contains(logged, "compensation failed") ||
		!strings.Contains(logged, fmt.Sprintf("deadline_id=%d", view.Deadline.ID)) {
		t.Errorf("expected compensation-failure log with deadline id, got: %q", logged)
	}
}

func TestDeleteCancelFailureDoesNotFailRequest(t *testing.T) {
	env := newTestEnv(t)
	actor := user(1, false)
	due := env.clock.now.Add(72 * time.Hour)
	view, _ := env.svc.Create(t.Context(), actor, CreateInput{
		Title: "T", DueAt: due,
		Reminders: []ReminderSpec{{Kind: domain.KindPreset, OffsetMinutes: intPtr(60)}},
	})
	env.reminders.cancelErr = errors.New("cancel boom")
	if err := env.svc.Delete(t.Context(), actor, view.Deadline.ID); err != nil {
		t.Fatalf("Delete = %v, want nil (cancel failure logged only)", err)
	}
	if _, err := env.svc.Get(t.Context(), actor, view.Deadline.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("deadline still visible after delete: %v", err)
	}
	if !strings.Contains(env.logs.String(), "cancellation failed") {
		t.Errorf("expected cancel-failure log, got %q", env.logs)
	}
}

func TestCompleteCancelFailureDoesNotFailRequest(t *testing.T) {
	env := newTestEnv(t)
	actor := user(1, false)
	due := env.clock.now.Add(72 * time.Hour)
	view, _ := env.svc.Create(t.Context(), actor, CreateInput{
		Title: "T", DueAt: due,
		Reminders: []ReminderSpec{{Kind: domain.KindPreset, OffsetMinutes: intPtr(60)}},
	})
	env.reminders.cancelErr = errors.New("cancel boom")
	got, err := env.svc.Complete(t.Context(), actor, view.Deadline.ID)
	if err != nil {
		t.Fatalf("Complete = %v, want nil (cancel failure logged only)", err)
	}
	if got.Deadline.Status != domain.DeadlineStatusDone {
		t.Errorf("status = %q, want done", got.Deadline.Status)
	}
	if !strings.Contains(env.logs.String(), "cancellation failed") {
		t.Errorf("expected cancel-failure log, got %q", env.logs)
	}
}
