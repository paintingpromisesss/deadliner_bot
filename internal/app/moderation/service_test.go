package moderation

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sauron/deadliner/internal/domain"
)

// --- fakes ---

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

type fakeGroupRepo struct {
	groups  map[int64]*domain.Group
	nextID  int64
	deleted map[int64]bool
}

func newFakeGroupRepo() *fakeGroupRepo {
	return &fakeGroupRepo{
		groups:  map[int64]*domain.Group{},
		deleted: map[int64]bool{},
		nextID:  1,
	}
}

func (r *fakeGroupRepo) add(g domain.Group) int64 {
	if g.SlugNorm == "" {
		g.SlugNorm = domain.Normalize(g.Slug)
	}
	g.ID = r.nextID
	r.nextID++
	cp := g
	r.groups[g.ID] = &cp
	return g.ID
}

func (r *fakeGroupRepo) Create(ctx context.Context, g *domain.Group) error {
	g.ID = r.add(*g)
	return nil
}

func (r *fakeGroupRepo) GetByID(ctx context.Context, id int64) (*domain.Group, error) {
	g, ok := r.groups[id]
	if !ok || r.deleted[id] {
		return nil, fmt.Errorf("%w: group id=%d", domain.ErrNotFound, id)
	}
	cp := *g
	return &cp, nil
}

func (r *fakeGroupRepo) GetBySlugNorm(ctx context.Context, norm string) (*domain.Group, error) {
	for _, g := range r.groups {
		if g.SlugNorm == norm && !r.deleted[g.ID] {
			cp := *g
			return &cp, nil
		}
	}
	return nil, fmt.Errorf("%w: group slug_norm=%q", domain.ErrNotFound, norm)
}

func (r *fakeGroupRepo) SearchByPrefix(ctx context.Context, prefix string, callerID int64, limit int) ([]domain.Group, error) {
	return nil, errors.New("not used")
}

func (r *fakeGroupRepo) Update(ctx context.Context, g *domain.Group) error {
	return errors.New("not used")
}

func (r *fakeGroupRepo) SetStatus(ctx context.Context, id int64, status domain.GroupStatus) error {
	g, ok := r.groups[id]
	if !ok {
		return fmt.Errorf("%w: group id=%d", domain.ErrNotFound, id)
	}
	g.Status = status
	return nil
}

func (r *fakeGroupRepo) SoftDelete(ctx context.Context, id int64) error {
	if _, ok := r.groups[id]; !ok || r.deleted[id] {
		return fmt.Errorf("%w: group id=%d", domain.ErrNotFound, id)
	}
	r.deleted[id] = true
	return nil
}

func (r *fakeGroupRepo) ListMine(ctx context.Context, userID int64) ([]domain.Group, error) {
	return nil, errors.New("not used")
}

// ListPendingExpired повторяет SQL-предикат репо: status='pending',
// deleted_at IS NULL, claim_expires_at IS NOT NULL AND <= now.
func (r *fakeGroupRepo) ListPendingExpired(ctx context.Context, now time.Time, limit int) ([]domain.Group, error) {
	out := []domain.Group{}
	for _, g := range r.groups {
		if r.deleted[g.ID] || g.Status != domain.GroupStatusPending {
			continue
		}
		if g.ClaimExpiresAt == nil || g.ClaimExpiresAt.After(now) {
			continue
		}
		out = append(out, *g)
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// ListAll — часть domain.GroupRepo, нужная только CLI `admin list-groups`;
// сервисам этих пакетов не требуется.
func (r *fakeGroupRepo) ListAll(ctx context.Context, status *domain.GroupStatus, limit int) ([]domain.Group, error) {
	return nil, nil
}

type membershipKey struct{ groupID, userID int64 }

type fakeMembershipRepo struct {
	rows map[membershipKey]domain.Membership
}

func newFakeMembershipRepo() *fakeMembershipRepo {
	return &fakeMembershipRepo{rows: map[membershipKey]domain.Membership{}}
}

func (r *fakeMembershipRepo) add(groupID, userID int64, role domain.Role) {
	r.rows[membershipKey{groupID, userID}] = domain.Membership{GroupID: groupID, UserID: userID, Role: role}
}

func (r *fakeMembershipRepo) Upsert(ctx context.Context, m *domain.Membership) error {
	r.rows[membershipKey{m.GroupID, m.UserID}] = *m
	return nil
}

func (r *fakeMembershipRepo) Get(ctx context.Context, groupID, userID int64) (*domain.Membership, error) {
	m, ok := r.rows[membershipKey{groupID, userID}]
	if !ok {
		return nil, fmt.Errorf("%w: membership group=%d user=%d", domain.ErrNotFound, groupID, userID)
	}
	cp := m
	return &cp, nil
}

func (r *fakeMembershipRepo) ListByGroup(ctx context.Context, groupID int64) ([]domain.Membership, error) {
	out := []domain.Membership{}
	for _, m := range r.rows {
		if m.GroupID == groupID {
			out = append(out, m)
		}
	}
	return out, nil
}

func (r *fakeMembershipRepo) CountAdmins(ctx context.Context, groupID int64) (int, error) {
	n := 0
	for _, m := range r.rows {
		if m.GroupID == groupID && m.Role == domain.RoleAdmin {
			n++
		}
	}
	return n, nil
}

func (r *fakeMembershipRepo) ListByGroupDetailed(ctx context.Context, groupID int64) ([]domain.MembershipDetail, error) {
	return nil, errors.New("not used")
}
func (r *fakeMembershipRepo) ListByUser(ctx context.Context, userID int64) ([]domain.Membership, error) {
	return nil, errors.New("not used")
}
func (r *fakeMembershipRepo) SetRole(ctx context.Context, groupID, userID int64, role domain.Role) error {
	return errors.New("not used")
}
func (r *fakeMembershipRepo) DemoteIfNotLastAdmin(ctx context.Context, groupID, userID int64) error {
	return errors.New("not used")
}
func (r *fakeMembershipRepo) RemoveIfNotLastAdmin(ctx context.Context, groupID, userID int64) error {
	return errors.New("not used")
}
func (r *fakeMembershipRepo) SetDMNotify(ctx context.Context, groupID, userID int64, dmNotify *bool) error {
	return errors.New("not used")
}
func (r *fakeMembershipRepo) ListDMTargets(ctx context.Context, groupID int64) ([]int64, error) {
	return nil, errors.New("not used")
}
func (r *fakeMembershipRepo) Delete(ctx context.Context, groupID, userID int64) error {
	return errors.New("not used")
}

type fakeBindingRepo struct {
	byGroup map[int64]*domain.ChatBinding
}

func newFakeBindingRepo() *fakeBindingRepo {
	return &fakeBindingRepo{byGroup: map[int64]*domain.ChatBinding{}}
}

func (r *fakeBindingRepo) add(groupID, chatID int64) {
	r.byGroup[groupID] = &domain.ChatBinding{GroupID: groupID, ChatID: chatID}
}

func (r *fakeBindingRepo) Create(ctx context.Context, b *domain.ChatBinding) error {
	r.byGroup[b.GroupID] = b
	return nil
}

func (r *fakeBindingRepo) GetByGroup(ctx context.Context, groupID int64) (*domain.ChatBinding, error) {
	b, ok := r.byGroup[groupID]
	if !ok {
		return nil, fmt.Errorf("%w: binding group id=%d", domain.ErrNotFound, groupID)
	}
	cp := *b
	return &cp, nil
}

func (r *fakeBindingRepo) GetByChat(ctx context.Context, chatID int64, threadID *int64) (*domain.ChatBinding, error) {
	for _, b := range r.byGroup {
		if b.ChatID == chatID {
			cp := *b
			return &cp, nil
		}
	}
	return nil, fmt.Errorf("%w: binding chat id=%d", domain.ErrNotFound, chatID)
}

func (r *fakeBindingRepo) Delete(ctx context.Context, groupID int64) error {
	delete(r.byGroup, groupID)
	return nil
}

type fakeDeadlineRepo struct {
	byGroup map[int64][]domain.Deadline
}

func newFakeDeadlineRepo() *fakeDeadlineRepo {
	return &fakeDeadlineRepo{byGroup: map[int64][]domain.Deadline{}}
}

func (r *fakeDeadlineRepo) add(groupID int64, ids ...int64) {
	for _, id := range ids {
		gid := groupID
		r.byGroup[groupID] = append(r.byGroup[groupID],
			domain.Deadline{ID: id, GroupID: &gid, Title: "t", Status: domain.DeadlineStatusActive})
	}
}

func (r *fakeDeadlineRepo) ListByGroup(ctx context.Context, groupID int64, from, to *time.Time, status *domain.DeadlineStatus) ([]domain.Deadline, error) {
	return append([]domain.Deadline(nil), r.byGroup[groupID]...), nil
}

func (r *fakeDeadlineRepo) Create(ctx context.Context, d *domain.Deadline, reminders []domain.Reminder) error {
	return errors.New("not used")
}
func (r *fakeDeadlineRepo) GetByID(ctx context.Context, id int64) (*domain.Deadline, error) {
	return nil, errors.New("not used")
}
func (r *fakeDeadlineRepo) Update(ctx context.Context, id int64, patch domain.DeadlinePatch) error {
	return errors.New("not used")
}
func (r *fakeDeadlineRepo) SetStatus(ctx context.Context, id int64, status domain.DeadlineStatus) error {
	return errors.New("not used")
}
func (r *fakeDeadlineRepo) SoftDelete(ctx context.Context, id int64) error {
	return errors.New("not used")
}
func (r *fakeDeadlineRepo) ListByOwner(ctx context.Context, ownerID int64, from, to *time.Time, status *domain.DeadlineStatus) ([]domain.Deadline, error) {
	return nil, errors.New("not used")
}

type fakeReminderRepo struct {
	cancelled  []int64
	byDeadline map[int64][]domain.Reminder
}

// addPending заводит pending-напоминание дедлайна (для подсчёта гашений).
func (r *fakeReminderRepo) addPending(deadlineID int64, ids ...int64) {
	if r.byDeadline == nil {
		r.byDeadline = map[int64][]domain.Reminder{}
	}
	for _, id := range ids {
		r.byDeadline[deadlineID] = append(r.byDeadline[deadlineID],
			domain.Reminder{ID: id, DeadlineID: deadlineID, Status: domain.ReminderStatusPending})
	}
}

func (r *fakeReminderRepo) CancelByDeadline(ctx context.Context, deadlineID int64) error {
	r.cancelled = append(r.cancelled, deadlineID)
	return nil
}

func (r *fakeReminderRepo) CreateBatch(ctx context.Context, reminders []domain.Reminder) error {
	return errors.New("not used")
}
func (r *fakeReminderRepo) ListByDeadline(ctx context.Context, deadlineID int64) ([]domain.Reminder, error) {
	return append([]domain.Reminder(nil), r.byDeadline[deadlineID]...), nil
}
func (r *fakeReminderRepo) FetchDue(ctx context.Context, tx domain.Tx, now time.Time, limit int, workerID string) ([]domain.Reminder, error) {
	return nil, errors.New("not used")
}
func (r *fakeReminderRepo) MarkSent(ctx context.Context, id int64, workerID string, now time.Time) (bool, error) {
	return false, errors.New("not used")
}
func (r *fakeReminderRepo) MarkSentWithFanout(ctx context.Context, tx domain.Tx, reminderID int64, workerID string, now time.Time, children []domain.Reminder) (bool, error) {
	return false, errors.New("not used")
}
func (r *fakeReminderRepo) MarkFailed(ctx context.Context, id int64, workerID, errText string, retryAt time.Time, maxAttempts int) (bool, error) {
	return false, errors.New("not used")
}
func (r *fakeReminderRepo) ReleaseStale(ctx context.Context, olderThan time.Time) (int64, error) {
	return 0, errors.New("not used")
}
func (r *fakeReminderRepo) Regenerate(ctx context.Context, deadlineID int64, newReminders []domain.Reminder) (int, error) {
	return 0, errors.New("not used")
}

type fakeUserRepo struct {
	byTelegram map[int64]*domain.User
	banned     map[int64]bool
	superadmin map[int64]bool
	revoked    []int64
}

func newFakeUserRepo() *fakeUserRepo {
	return &fakeUserRepo{
		byTelegram: map[int64]*domain.User{},
		banned:     map[int64]bool{},
		superadmin: map[int64]bool{},
	}
}

func (r *fakeUserRepo) add(id, telegramID int64, superadmin bool) {
	r.byTelegram[telegramID] = &domain.User{ID: id, TelegramID: telegramID, IsSuperadmin: superadmin}
	r.superadmin[telegramID] = superadmin
}

func (r *fakeUserRepo) GetByTelegramID(ctx context.Context, telegramID int64) (*domain.User, error) {
	u, ok := r.byTelegram[telegramID]
	if !ok {
		return nil, fmt.Errorf("%w: user telegram_id=%d", domain.ErrNotFound, telegramID)
	}
	cp := *u
	cp.IsBanned = r.banned[telegramID]
	cp.IsSuperadmin = r.superadmin[telegramID]
	return &cp, nil
}

func (r *fakeUserRepo) GetByID(ctx context.Context, id int64) (*domain.User, error) {
	for tg, u := range r.byTelegram {
		if u.ID == id {
			return r.GetByTelegramID(ctx, tg)
		}
	}
	return nil, fmt.Errorf("%w: user id=%d", domain.ErrNotFound, id)
}

func (r *fakeUserRepo) UpsertByTelegram(ctx context.Context, u *domain.User) error {
	return errors.New("not used")
}
func (r *fakeUserRepo) UpdateSettings(ctx context.Context, id int64, tz string, dmNotifyDefault bool) error {
	return errors.New("not used")
}

func (r *fakeUserRepo) SetBanned(ctx context.Context, id int64, banned bool) error {
	for tg, u := range r.byTelegram {
		if u.ID == id {
			r.banned[tg] = banned
			return nil
		}
	}
	return fmt.Errorf("%w: user id=%d", domain.ErrNotFound, id)
}

func (r *fakeUserRepo) SetSuperadmin(ctx context.Context, id int64, superadmin bool) error {
	for tg, u := range r.byTelegram {
		if u.ID == id {
			r.superadmin[tg] = superadmin
			return nil
		}
	}
	return fmt.Errorf("%w: user id=%d", domain.ErrNotFound, id)
}

func (r *fakeUserRepo) MarkBotBlocked(ctx context.Context, telegramID int64, blocked bool) error {
	return errors.New("not used")
}

// ListSuperadmins и UpdateProfile — части domain.UserRepo, не используемые
// сервисом модерации: заглушки-нули.
func (r *fakeUserRepo) ListSuperadmins(ctx context.Context) ([]domain.User, error) {
	return nil, nil
}
func (r *fakeUserRepo) UpdateProfile(ctx context.Context, id int64, firstName string) error {
	return nil
}

type fakeSessionRepo struct {
	revokedFor []int64
}

func (r *fakeSessionRepo) Create(ctx context.Context, s *domain.Session) error {
	return errors.New("not used")
}
func (r *fakeSessionRepo) GetActive(ctx context.Context, tokenHash string, now time.Time) (*domain.Session, error) {
	return nil, errors.New("not used")
}
func (r *fakeSessionRepo) Touch(ctx context.Context, tokenHash string, lastSeen, expiresAt time.Time) error {
	return errors.New("not used")
}
func (r *fakeSessionRepo) Revoke(ctx context.Context, tokenHash string) error {
	return errors.New("not used")
}
func (r *fakeSessionRepo) RevokeAllForUser(ctx context.Context, userID int64) error {
	r.revokedFor = append(r.revokedFor, userID)
	return nil
}

type fakeMaintenanceRepo struct {
	stats        domain.Stats
	counterCut   time.Time
	sessionCut   time.Time
	counters     int64
	sessions     int64
	purgeCounter bool
	purgeSession bool
	err          error
}

func (r *fakeMaintenanceRepo) Stats(ctx context.Context, now time.Time) (domain.Stats, error) {
	if r.err != nil {
		return domain.Stats{}, r.err
	}
	return r.stats, nil
}

func (r *fakeMaintenanceRepo) PurgeCounters(ctx context.Context, olderThan time.Time) (int64, error) {
	r.purgeCounter = true
	r.counterCut = olderThan
	return r.counters, r.err
}

func (r *fakeMaintenanceRepo) PurgeExpiredSessions(ctx context.Context, olderThan time.Time) (int64, error) {
	r.purgeSession = true
	r.sessionCut = olderThan
	return r.sessions, r.err
}

type fakeAuditRepo struct {
	entries []*domain.AuditEntry
}

func (r *fakeAuditRepo) Write(ctx context.Context, e *domain.AuditEntry) error {
	cp := *e
	r.entries = append(r.entries, &cp)
	return nil
}

func (r *fakeAuditRepo) find(action string) *domain.AuditEntry {
	for _, e := range r.entries {
		if e.Action == action {
			return e
		}
	}
	return nil
}

// --- harness ---

type fixture struct {
	svc      *Service
	groups   *fakeGroupRepo
	members  *fakeMembershipRepo
	bindings *fakeBindingRepo
	deadline *fakeDeadlineRepo
	reminder *fakeReminderRepo
	users    *fakeUserRepo
	sessions *fakeSessionRepo
	maint    *fakeMaintenanceRepo
	audit    *fakeAuditRepo
	clock    *fakeClock
}

func newFixture() *fixture {
	clock := &fakeClock{now: time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)}
	f := &fixture{
		groups:   newFakeGroupRepo(),
		members:  newFakeMembershipRepo(),
		bindings: newFakeBindingRepo(),
		deadline: newFakeDeadlineRepo(),
		reminder: &fakeReminderRepo{},
		users:    newFakeUserRepo(),
		sessions: &fakeSessionRepo{},
		maint:    &fakeMaintenanceRepo{stats: domain.Stats{Users: 7, GroupsTotal: 3}},
		audit:    &fakeAuditRepo{},
		clock:    clock,
	}
	f.svc = NewService(Deps{
		Groups:      f.groups,
		Deadlines:   f.deadline,
		Reminders:   f.reminder,
		Memberships: f.members,
		Bindings:    f.bindings,
		Users:       f.users,
		Sessions:    f.sessions,
		Maintenance: f.maint,
		Audit:       f.audit,
		Clock:       f.clock,
		Log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		Config: Config{
			PendingTTL:       14 * 24 * time.Hour,
			CounterRetention: 8 * 24 * time.Hour,
		},
	})
	return f
}

// expiredPending заводит pending-группу с истёкшим TTL.
func (f *fixture) expiredPending(slug string) int64 {
	past := f.clock.now.Add(-time.Hour)
	return f.groups.add(domain.Group{
		Slug: slug, Status: domain.GroupStatusPending, ClaimExpiresAt: &past,
	})
}

func superadmin() *domain.User { return &domain.User{ID: 1, TelegramID: 100, IsSuperadmin: true} }

// --- CleanupExpiredPending ---

func TestCleanupDeletesOnlyExpiredPendingWithoutBindingOrAdmin(t *testing.T) {
	f := newFixture()

	doomed := f.expiredPending("ИКБО-33-21")
	bound := f.expiredPending("ИКБО-34-21")
	adminned := f.expiredPending("ИКБО-35-21")
	future := f.clock.now.Add(time.Hour)
	alive := f.groups.add(domain.Group{
		Slug: "ИКБО-36-21", Status: domain.GroupStatusPending, ClaimExpiresAt: &future,
	})
	past := f.clock.now.Add(-time.Hour)
	active := f.groups.add(domain.Group{
		Slug: "ИКБО-37-21", Status: domain.GroupStatusActive, ClaimExpiresAt: &past,
	})
	noDeadline := f.groups.add(domain.Group{
		Slug: "ИКБО-38-21", Status: domain.GroupStatusPending, ClaimExpiresAt: nil,
	})

	f.bindings.add(bound, -100500)
	f.members.add(adminned, 42, domain.RoleAdmin)
	// Обычный участник — не админ: группа всё ещё «ничья», её удаляем.
	plain := f.expiredPending("ИКБО-39-21")
	f.members.add(plain, 43, domain.RoleMember)

	report, err := f.svc.CleanupExpiredPending(context.Background())
	if err != nil {
		t.Fatalf("CleanupExpiredPending: %v", err)
	}

	if report.Groups != 2 {
		t.Errorf("report.Groups = %d, want 2 (doomed + plain)", report.Groups)
	}
	for _, id := range []int64{doomed, plain} {
		if !f.groups.deleted[id] {
			t.Errorf("group %d: not deleted, want soft-deleted", id)
		}
	}
	for name, id := range map[string]int64{
		"bound (has chat binding)": bound,
		"adminned (has admin)":     adminned,
		"alive (TTL not expired)":  alive,
		"active (not pending)":     active,
		"noDeadline (no TTL)":      noDeadline,
	} {
		if f.groups.deleted[id] {
			t.Errorf("%s: group %d was deleted, want kept", name, id)
		}
	}

	e := f.audit.find("cleanup.run")
	if e == nil {
		t.Fatal("no cleanup.run audit entry")
	}
	if e.ActorUserID != nil {
		t.Errorf("cleanup actor = %v, want nil (system job)", *e.ActorUserID)
	}
	if got := e.Meta["groups"]; got != 2 {
		t.Errorf("audit meta groups = %v, want 2", got)
	}
}

// Нет привязки И нет админа — оба условия обязательны (checked individually
// above); здесь — что «нет привязки» само по себе недостаточно.
func TestCleanupKeepsExpiredPendingWithAdminEvenWithoutBinding(t *testing.T) {
	f := newFixture()
	id := f.expiredPending("М8О-401Б-23")
	f.members.add(id, 5, domain.RoleAdmin)

	if _, err := f.svc.CleanupExpiredPending(context.Background()); err != nil {
		t.Fatalf("CleanupExpiredPending: %v", err)
	}
	if f.groups.deleted[id] {
		t.Error("expired pending group with an admin must be kept")
	}
}

// Нет админа, но есть привязка чата — группа уже «в бою», TTL не применяется.
func TestCleanupKeepsExpiredPendingWithBindingEvenWithoutAdmin(t *testing.T) {
	f := newFixture()
	id := f.expiredPending("М8О-402Б-23")
	f.bindings.add(id, -100600)

	if _, err := f.svc.CleanupExpiredPending(context.Background()); err != nil {
		t.Fatalf("CleanupExpiredPending: %v", err)
	}
	if f.groups.deleted[id] {
		t.Error("expired pending group with a chat binding must be kept")
	}
}

// У удаляемой группы гасятся pending-напоминания всех её дедлайнов; у
// оставленной — нет.
func TestCleanupCancelsRemindersOfDeletedGroupsOnly(t *testing.T) {
	f := newFixture()
	doomed := f.expiredPending("ИКБО-41-21")
	bound := f.expiredPending("ИКБО-42-21")
	f.bindings.add(bound, -100700)
	f.deadline.add(doomed, 11, 12)
	f.deadline.add(bound, 21)
	f.reminder.addPending(11, 111)
	f.reminder.addPending(12, 121, 122)
	f.reminder.addPending(21, 211)

	report, err := f.svc.CleanupExpiredPending(context.Background())
	if err != nil {
		t.Fatalf("CleanupExpiredPending: %v", err)
	}
	// CancelByDeadline вызывается на каждый дедлайн удаляемой группы (у
	// оставленной — нет): под гашение попали 11 и 12, но не 21.
	if len(f.reminder.cancelled) != 2 || f.reminder.cancelled[0] != 11 || f.reminder.cancelled[1] != 12 {
		t.Errorf("cancelled = %v, want [11 12]", f.reminder.cancelled)
	}
	if report.Reminders != 3 {
		t.Errorf("report.Reminders = %d, want 3 (pending reminders of 11 and 12)", report.Reminders)
	}
}

// Служебная часть cleanup: старые окна счётчиков (ретенция из конфига) и
// протухшие сессии (грейс 7 дней).
func TestCleanupPurgesCountersAndSessions(t *testing.T) {
	f := newFixture()
	f.maint.counters = 5
	f.maint.sessions = 3

	report, err := f.svc.CleanupExpiredPending(context.Background())
	if err != nil {
		t.Fatalf("CleanupExpiredPending: %v", err)
	}
	if !f.maint.purgeCounter || !f.maint.purgeSession {
		t.Fatal("purges were not called")
	}
	// Cut-off берётся из Конфига, а не из константы: оператор, повышающий
	// окно лимита, обязан суметь повысить и ретенцию.
	if want := f.clock.now.Add(-f.svc.cfg.CounterRetention); !f.maint.counterCut.Equal(want) {
		t.Errorf("counter cut = %v, want %v", f.maint.counterCut, want)
	}
	if want := f.clock.now.Add(-sessionGrace); !f.maint.sessionCut.Equal(want) {
		t.Errorf("session cut = %v, want %v", f.maint.sessionCut, want)
	}
	if report.CountersPurged != 5 || report.SessionsPurged != 3 {
		t.Errorf("report = %+v, want counters=5 sessions=3", report)
	}
	if e := f.audit.find("cleanup.run"); e == nil {
		t.Fatal("no cleanup.run audit entry")
	} else if e.Meta["counters_purged"] != 5 {
		t.Errorf("audit meta counters_purged = %v, want 5", e.Meta["counters_purged"])
	}
}

// Ретенция по умолчанию обязана быть СТРОГО больше самого длинного окна
// rate-limit-счётчика (недельный group_create_week, 168ч): окно floor-ится на
// своё начало, поэтому живая строка недельного счётчика бывает почти 168ч от
// роду. Регрессия: 48ч удаляло её и LIMIT_GROUP_CREATE_WEEK молча не срабатывал.
func TestDefaultCounterRetentionExceedsWeekWindow(t *testing.T) {
	const weekWindow = 168 * time.Hour
	if defaultCounterRetention <= weekWindow {
		t.Fatalf("defaultCounterRetention = %v, must exceed the weekly window %v",
			defaultCounterRetention, weekWindow)
	}
	if defaultCounterRetention != 8*24*time.Hour {
		t.Errorf("defaultCounterRetention = %v, want 192h", defaultCounterRetention)
	}

	// Нулевое значение у вызывающего без конфига подменяется дефолтом, а не 0:
	// retention=0 удалял бы все окна немедленно.
	f := newFixture()
	f.svc = NewService(Deps{
		Groups: f.groups, Deadlines: f.deadline, Reminders: f.reminder,
		Memberships: f.members, Bindings: f.bindings, Users: f.users,
		Sessions: f.sessions, Maintenance: f.maint, Audit: f.audit,
		Clock: f.clock, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Config: Config{PendingTTL: 14 * 24 * time.Hour}, // CounterRetention не задан
	})
	if _, err := f.svc.CleanupExpiredPending(context.Background()); err != nil {
		t.Fatalf("CleanupExpiredPending: %v", err)
	}
	if want := f.clock.now.Add(-defaultCounterRetention); !f.maint.counterCut.Equal(want) {
		t.Errorf("counter cut = %v, want the default %v", f.maint.counterCut, want)
	}
}

// Ничего не подошло — нулевой отчёт, без ошибок и без записи аудита о
// вымышленной работе (аудит пишется всегда: джоба запускалась).
func TestCleanupEmptyRun(t *testing.T) {
	f := newFixture()
	report, err := f.svc.CleanupExpiredPending(context.Background())
	if err != nil {
		t.Fatalf("CleanupExpiredPending: %v", err)
	}
	if report.Groups != 0 || report.Reminders != 0 || report.CountersPurged != 0 || report.SessionsPurged != 0 {
		t.Errorf("report = %+v, want zeroes", report)
	}
}

// --- superadmin operations ---

func TestPromoteSuperadmin(t *testing.T) {
	f := newFixture()
	f.users.add(7, 555, false)

	if err := f.svc.PromoteSuperadmin(context.Background(), superadmin(), 555); err != nil {
		t.Fatalf("PromoteSuperadmin: %v", err)
	}
	if !f.users.superadmin[555] {
		t.Error("user 555 was not promoted")
	}
	if e := f.audit.find("user.promote"); e == nil {
		t.Error("no user.promote audit entry")
	} else if e.TargetID == nil || *e.TargetID != 7 {
		t.Errorf("audit target = %v, want 7", e.TargetID)
	}
}

func TestSuperadminMethodsRequireSuperadminActor(t *testing.T) {
	f := newFixture()
	f.users.add(7, 555, false)
	plain := &domain.User{ID: 2, TelegramID: 200}

	ctx := context.Background()
	if err := f.svc.PromoteSuperadmin(ctx, plain, 555); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("PromoteSuperadmin(plain) = %v, want ErrForbidden", err)
	}
	if err := f.svc.BanUser(ctx, plain, 555); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("BanUser(plain) = %v, want ErrForbidden", err)
	}
	if err := f.svc.UnbanUser(ctx, plain, 555); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("UnbanUser(plain) = %v, want ErrForbidden", err)
	}
	if _, err := f.svc.DeleteGroup(ctx, plain, "ИКБО-33-21"); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("DeleteGroup(plain) = %v, want ErrForbidden", err)
	}
	if _, err := f.svc.Stats(ctx, plain); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("Stats(plain) = %v, want ErrForbidden", err)
	}
	if err := f.svc.PromoteSuperadmin(ctx, nil, 555); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("PromoteSuperadmin(nil) = %v, want ErrForbidden", err)
	}
	if f.users.superadmin[555] {
		t.Error("a non-superadmin actor must not promote")
	}
}

// Бан гасит все сессии пользователя немедленно (спека §3.3 + §5.1: opaque-токен
// выбран именно ради отзыва) и пишет аудит.
func TestBanUserRevokesSessionsAndAudits(t *testing.T) {
	f := newFixture()
	f.users.add(7, 555, false)

	if err := f.svc.BanUser(context.Background(), superadmin(), 555); err != nil {
		t.Fatalf("BanUser: %v", err)
	}
	if !f.users.banned[555] {
		t.Error("user 555 was not banned")
	}
	if len(f.sessions.revokedFor) != 1 || f.sessions.revokedFor[0] != 7 {
		t.Errorf("revoked sessions for = %v, want [7]", f.sessions.revokedFor)
	}
	e := f.audit.find("user.ban")
	if e == nil {
		t.Fatal("no user.ban audit entry")
	}
	if e.TargetID == nil || *e.TargetID != 7 {
		t.Errorf("audit target = %v, want 7", e.TargetID)
	}
	if e.Meta["telegram_id"] != int64(555) {
		t.Errorf("audit meta telegram_id = %v, want 555", e.Meta["telegram_id"])
	}
}

func TestBanUserRejectsSelfAndSuperadminTarget(t *testing.T) {
	f := newFixture()
	f.users.add(1, 100, true) // сам вызывающий
	f.users.add(2, 200, true) // другой супер-админ
	actor := superadmin()

	if err := f.svc.BanUser(context.Background(), actor, 100); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("BanUser(self) = %v, want ErrForbidden", err)
	}
	if err := f.svc.BanUser(context.Background(), actor, 200); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("BanUser(superadmin) = %v, want ErrForbidden", err)
	}
	if f.users.banned[100] || f.users.banned[200] {
		t.Error("self/superadmin ban must not be applied")
	}
	if len(f.sessions.revokedFor) != 0 {
		t.Errorf("revoked = %v, want none", f.sessions.revokedFor)
	}
}

func TestBanUserUnknownTelegramID(t *testing.T) {
	f := newFixture()
	if err := f.svc.BanUser(context.Background(), superadmin(), 999); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("BanUser(unknown) = %v, want ErrNotFound", err)
	}
	if len(f.sessions.revokedFor) != 0 {
		t.Errorf("revoked = %v, want none", f.sessions.revokedFor)
	}
}

func TestUnbanUser(t *testing.T) {
	f := newFixture()
	f.users.add(7, 555, false)
	f.users.banned[555] = true

	if err := f.svc.UnbanUser(context.Background(), superadmin(), 555); err != nil {
		t.Fatalf("UnbanUser: %v", err)
	}
	if f.users.banned[555] {
		t.Error("user 555 is still banned")
	}
	if f.audit.find("user.unban") == nil {
		t.Error("no user.unban audit entry")
	}
}

// delete-group принимает слаг в любом регистре/наборе пробелов: нормализация
// та же, что у GroupRepo (unique index на slug_norm).
func TestDeleteGroupNormalizesSlug(t *testing.T) {
	f := newFixture()
	past := f.clock.now.Add(-time.Hour)
	id := f.groups.add(domain.Group{
		Slug: "ИКБО-33-21", SlugNorm: "ИКБО-33-21",
		Status: domain.GroupStatusActive, ClaimExpiresAt: &past,
	})
	f.deadline.add(id, 31)

	g, err := f.svc.DeleteGroup(context.Background(), superadmin(), "  икбо-33-21 ")
	if err != nil {
		t.Fatalf("DeleteGroup: %v", err)
	}
	if g == nil || g.ID != id {
		t.Fatalf("DeleteGroup returned %+v, want group %d", g, id)
	}
	if !f.groups.deleted[id] {
		t.Error("group was not soft-deleted")
	}
	if len(f.reminder.cancelled) != 1 || f.reminder.cancelled[0] != 31 {
		t.Errorf("cancelled = %v, want [31]", f.reminder.cancelled)
	}
	e := f.audit.find("group.delete")
	if e == nil {
		t.Fatal("no group.delete audit entry")
	}
	if e.Meta["slug"] != "ИКБО-33-21" {
		t.Errorf("audit meta slug = %v, want ИКБО-33-21", e.Meta["slug"])
	}
}

func TestDeleteGroupUnknownSlug(t *testing.T) {
	f := newFixture()
	if _, err := f.svc.DeleteGroup(context.Background(), superadmin(), "НЕТ-1-1"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("DeleteGroup(unknown) = %v, want ErrNotFound", err)
	}
}

// SystemActor (CLI) проходит проверку прав, но в аудите остаётся NULL-актором:
// строки users у оператора нет, а «пользователь 0» нарушил бы FK.
func TestSystemActorAuditsAsNull(t *testing.T) {
	f := newFixture()
	f.users.add(7, 555, false)

	if err := f.svc.PromoteSuperadmin(context.Background(), SystemActor, 555); err != nil {
		t.Fatalf("PromoteSuperadmin(SystemActor): %v", err)
	}
	e := f.audit.find("user.promote")
	if e == nil {
		t.Fatal("no user.promote audit entry")
	}
	if e.ActorUserID != nil {
		t.Errorf("actor = %v, want nil for the CLI system actor", *e.ActorUserID)
	}
}

func TestStatsRequiresSuperadminAndPassesThrough(t *testing.T) {
	f := newFixture()
	f.maint.stats = domain.Stats{
		Users: 10, GroupsTotal: 4, GroupsActive: 2, GroupsPending: 2,
		DeadlinesActive: 9, RemindersPending: 3, RemindersFailed: 1, SessionsActive: 6,
	}
	got, err := f.svc.Stats(context.Background(), superadmin())
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if got != f.maint.stats {
		t.Errorf("Stats = %+v, want %+v", got, f.maint.stats)
	}
}

// --- CleanupLoop ---

type countingCleaner struct {
	mu    sync.Mutex
	calls int
	done  chan struct{}
	err   error
}

func (c *countingCleaner) CleanupExpiredPending(ctx context.Context) (CleanupReport, error) {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	if c.done != nil {
		select {
		case c.done <- struct{}{}:
		default:
		}
	}
	return CleanupReport{}, c.err
}

func (c *countingCleaner) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func TestStartCleanupLoopRunsAndStopsOnCancel(t *testing.T) {
	cleaner := &countingCleaner{done: make(chan struct{}, 1)}
	ctx, cancel := context.WithCancel(context.Background())

	returned := make(chan struct{})
	go func() {
		StartCleanupLoop(ctx, cleaner, 5*time.Millisecond, slog.New(slog.NewTextHandler(io.Discard, nil)))
		close(returned)
	}()

	select {
	case <-cleaner.done:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("cleanup loop did not fire within 2s")
	}

	// Петля продолжает работать, пока ctx жив.
	time.Sleep(30 * time.Millisecond)
	if cleaner.count() < 2 {
		t.Errorf("calls = %d, want the loop to keep ticking", cleaner.count())
	}

	cancel()
	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("cleanup loop did not return after ctx cancel")
	}
}

// Ошибка прогона не роняет петлю: следующий тик всё равно выполнится.
func TestStartCleanupLoopSurvivesErrors(t *testing.T) {
	cleaner := &countingCleaner{done: make(chan struct{}, 1), err: errors.New("boom")}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go StartCleanupLoop(ctx, cleaner, 5*time.Millisecond, slog.New(slog.NewTextHandler(io.Discard, nil)))

	select {
	case <-cleaner.done:
	case <-time.After(2 * time.Second):
		t.Fatal("cleanup loop did not fire")
	}
	time.Sleep(30 * time.Millisecond)
	if cleaner.count() < 2 {
		t.Errorf("calls = %d, want the loop to survive an error", cleaner.count())
	}
}

// panicCleaner паникует на первом прогоне и считает последующие.
type panicCleaner struct {
	mu     sync.Mutex
	calls  int
	panics int
	done   chan struct{}
}

func (c *panicCleaner) CleanupExpiredPending(ctx context.Context) (CleanupReport, error) {
	c.mu.Lock()
	c.calls++
	first := c.calls == 1
	if first {
		c.panics++
	}
	c.mu.Unlock()
	if first {
		panic("repo exploded")
	}
	select {
	case c.done <- struct{}{}:
	default:
	}
	return CleanupReport{}, nil
}

// Паника в прогоне перехватывается: петля логирует её и продолжает работу —
// служебная джоба не должна ронять процесс serve (бота и API).
func TestStartCleanupLoopRecoversFromPanic(t *testing.T) {
	cleaner := &panicCleaner{done: make(chan struct{}, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var buf strings.Builder
	returned := make(chan struct{})
	go func() {
		StartCleanupLoop(ctx, cleaner, 5*time.Millisecond,
			slog.New(slog.NewTextHandler(&buf, nil)))
		close(returned)
	}()

	select {
	case <-cleaner.done:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("cleanup loop did not continue after a panic")
	}
	if !strings.Contains(buf.String(), "panicked") {
		t.Errorf("panic was not logged: %s", buf.String())
	}
	cancel()
	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("cleanup loop did not return after ctx cancel")
	}
}

// Интервал по умолчанию: ноль/отрицательный → defaultInterval (1h), а не
// busy-loop (иначе джоба выела бы CPU).
func TestDefaultCleanupInterval(t *testing.T) {
	if got := cleanupInterval(0); got != defaultCleanupInterval {
		t.Errorf("cleanupInterval(0) = %v, want %v", got, defaultCleanupInterval)
	}
	if got := cleanupInterval(-time.Second); got != defaultCleanupInterval {
		t.Errorf("cleanupInterval(-1s) = %v, want %v", got, defaultCleanupInterval)
	}
	if got := cleanupInterval(15 * time.Minute); got != 15*time.Minute {
		t.Errorf("cleanupInterval(15m) = %v, want 15m", got)
	}
}
