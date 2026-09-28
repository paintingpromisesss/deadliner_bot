package groups

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/sauron/deadliner/internal/domain"
)

// --- fakes ---

type fakeClock struct{ now time.Time }

func (c fakeClock) Now() time.Time { return c.now }

type fakeGroupRepo struct {
	groups  map[int64]*domain.Group
	nextID  int64
	byNorm  map[string]int64
	deleted map[int64]bool
}

func newFakeGroupRepo() *fakeGroupRepo {
	return &fakeGroupRepo{
		groups:  map[int64]*domain.Group{},
		byNorm:  map[string]int64{},
		deleted: map[int64]bool{},
		nextID:  1,
	}
}

func (r *fakeGroupRepo) Create(ctx context.Context, g *domain.Group) error {
	norm := domain.Normalize(g.Slug)
	if id, ok := r.byNorm[norm]; ok && !r.deleted[id] {
		return fmt.Errorf("%w: slug_norm=%s", domain.ErrConflict, norm)
	}
	g.ID = r.nextID
	r.nextID++
	g.SlugNorm = norm
	if g.CreatedAt.IsZero() {
		g.CreatedAt = time.Now().UTC()
	}
	cp := *g
	r.groups[g.ID] = &cp
	r.byNorm[norm] = g.ID
	return nil
}

func (r *fakeGroupRepo) GetByID(ctx context.Context, id int64) (*domain.Group, error) {
	g, ok := r.groups[id]
	if !ok || r.deleted[id] {
		return nil, domain.ErrNotFound
	}
	cp := *g
	return &cp, nil
}

func (r *fakeGroupRepo) GetBySlugNorm(ctx context.Context, norm string) (*domain.Group, error) {
	id, ok := r.byNorm[norm]
	if !ok || r.deleted[id] {
		return nil, domain.ErrNotFound
	}
	return r.GetByID(ctx, id)
}

func (r *fakeGroupRepo) SearchByPrefix(ctx context.Context, prefix string, callerID int64, limit int) ([]domain.Group, error) {
	out := []domain.Group{}
	p := domain.Normalize(prefix)
	for _, g := range r.groups {
		if r.deleted[g.ID] || !strings.HasPrefix(g.SlugNorm, p) {
			continue
		}
		if g.Status == domain.GroupStatusActive || (g.Status == domain.GroupStatusPending && g.CreatedBy == callerID) {
			out = append(out, *g)
		}
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (r *fakeGroupRepo) Update(ctx context.Context, g *domain.Group) error {
	cur, ok := r.groups[g.ID]
	if !ok || r.deleted[g.ID] {
		return domain.ErrNotFound
	}
	cur.Title = g.Title
	cur.Official = g.Official
	cur.DefaultPresets = g.DefaultPresets
	return nil
}

func (r *fakeGroupRepo) SetStatus(ctx context.Context, id int64, status domain.GroupStatus) error {
	g, ok := r.groups[id]
	if !ok {
		return domain.ErrNotFound
	}
	g.Status = status
	return nil
}

func (r *fakeGroupRepo) SoftDelete(ctx context.Context, id int64) error {
	if _, ok := r.groups[id]; !ok || r.deleted[id] {
		return domain.ErrNotFound
	}
	r.deleted[id] = true
	return nil
}

func (r *fakeGroupRepo) ListMine(ctx context.Context, userID int64) ([]domain.Group, error) {
	out := []domain.Group{}
	for _, g := range r.groups {
		if r.deleted[g.ID] {
			continue
		}
		for _, m := range membershipStore.listByUser(userID) {
			if m.GroupID == g.ID {
				out = append(out, *g)
			}
		}
	}
	return out, nil
}

func (r *fakeGroupRepo) ListPendingExpired(ctx context.Context, now time.Time, limit int) ([]domain.Group, error) {
	return nil, errors.New("not used")
}

type memKey struct{ groupID, userID int64 }

type fakeMembershipRepo struct {
	mems map[memKey]*domain.Membership
}

// membershipStore — общая точка доступа для fakeGroupRepo.ListMine.
var membershipStore *fakeMembershipRepo

func newFakeMembershipRepo() *fakeMembershipRepo {
	r := &fakeMembershipRepo{mems: map[memKey]*domain.Membership{}}
	membershipStore = r
	return r
}

func (r *fakeMembershipRepo) listByUser(userID int64) []domain.Membership {
	out := []domain.Membership{}
	for _, m := range r.mems {
		if m.UserID == userID {
			out = append(out, *m)
		}
	}
	return out
}

func (r *fakeMembershipRepo) Upsert(ctx context.Context, m *domain.Membership) error {
	k := memKey{m.GroupID, m.UserID}
	if _, ok := r.mems[k]; ok {
		return nil // DO NOTHING, как в настоящем репо
	}
	role := m.Role
	if role == "" {
		role = domain.RoleMember
	}
	cp := *m
	cp.Role = role
	cp.JoinedAt = time.Now().UTC()
	r.mems[k] = &cp
	return nil
}

func (r *fakeMembershipRepo) Get(ctx context.Context, groupID, userID int64) (*domain.Membership, error) {
	m, ok := r.mems[memKey{groupID, userID}]
	if !ok {
		return nil, domain.ErrNotFound
	}
	cp := *m
	return &cp, nil
}

func (r *fakeMembershipRepo) ListByGroup(ctx context.Context, groupID int64) ([]domain.Membership, error) {
	out := []domain.Membership{}
	for _, m := range r.mems {
		if m.GroupID == groupID {
			out = append(out, *m)
		}
	}
	return out, nil
}

func (r *fakeMembershipRepo) ListByGroupDetailed(ctx context.Context, groupID int64) ([]domain.MembershipDetail, error) {
	mems, err := r.ListByGroup(ctx, groupID)
	if err != nil {
		return nil, err
	}
	out := make([]domain.MembershipDetail, 0, len(mems))
	for _, m := range mems {
		out = append(out, domain.MembershipDetail{
			Membership: m,
			Username:   fmt.Sprintf("user_%d", m.UserID),
			FirstName:  "User",
		})
	}
	return out, nil
}

func (r *fakeMembershipRepo) ListByUser(ctx context.Context, userID int64) ([]domain.Membership, error) {
	return r.listByUser(userID), nil
}

func (r *fakeMembershipRepo) SetRole(ctx context.Context, groupID, userID int64, role domain.Role) error {
	m, ok := r.mems[memKey{groupID, userID}]
	if !ok {
		return domain.ErrNotFound
	}
	m.Role = role
	return nil
}

// countAdmins — локальный счётчик для условных операций (зеркалит SQL-guard).
func (r *fakeMembershipRepo) countAdmins(groupID int64) int {
	n := 0
	for _, m := range r.mems {
		if m.GroupID == groupID && m.Role == domain.RoleAdmin {
			n++
		}
	}
	return n
}

func (r *fakeMembershipRepo) DemoteIfNotLastAdmin(ctx context.Context, groupID, userID int64) error {
	m, ok := r.mems[memKey{groupID, userID}]
	if !ok {
		return domain.ErrNotFound
	}
	if m.Role != domain.RoleAdmin {
		return domain.ErrValidation
	}
	if r.countAdmins(groupID) <= 1 {
		return domain.ErrConflict
	}
	m.Role = domain.RoleMember
	return nil
}

func (r *fakeMembershipRepo) RemoveIfNotLastAdmin(ctx context.Context, groupID, userID int64) error {
	m, ok := r.mems[memKey{groupID, userID}]
	if !ok {
		return domain.ErrNotFound
	}
	if m.Role == domain.RoleAdmin && r.countAdmins(groupID) <= 1 {
		return domain.ErrConflict
	}
	delete(r.mems, memKey{groupID, userID})
	return nil
}

func (r *fakeMembershipRepo) SetDMNotify(ctx context.Context, groupID, userID int64, dm *bool) error {
	return errors.New("not used")
}

func (r *fakeMembershipRepo) Delete(ctx context.Context, groupID, userID int64) error {
	k := memKey{groupID, userID}
	if _, ok := r.mems[k]; !ok {
		return domain.ErrNotFound
	}
	delete(r.mems, k)
	return nil
}

func (r *fakeMembershipRepo) CountAdmins(ctx context.Context, groupID int64) (int, error) {
	n := 0
	for _, m := range r.mems {
		if m.GroupID == groupID && m.Role == domain.RoleAdmin {
			n++
		}
	}
	return n, nil
}

type fakeInviteRepo struct {
	invites map[string]*domain.Invite // by stored code
	nextID  int64
	clock   *fakeClock
}

func newFakeInviteRepo(clock *fakeClock) *fakeInviteRepo {
	return &fakeInviteRepo{invites: map[string]*domain.Invite{}, nextID: 1, clock: clock}
}

func (r *fakeInviteRepo) Create(ctx context.Context, inv *domain.Invite) error {
	inv.ID = r.nextID
	r.nextID++
	cp := *inv
	r.invites[inv.Code] = &cp
	return nil
}

func (r *fakeInviteRepo) GetByCode(ctx context.Context, code string) (*domain.Invite, error) {
	inv, ok := r.invites[code]
	if !ok {
		return nil, domain.ErrNotFound
	}
	cp := *inv
	return &cp, nil
}

// IncrementUsed зеркалит условный SQL репо: отозван/истёк/исчерпан → ErrConflict.
func (r *fakeInviteRepo) IncrementUsed(ctx context.Context, id int64) error {
	for _, inv := range r.invites {
		if inv.ID != id {
			continue
		}
		now := r.clock.Now()
		if inv.RevokedAt != nil || !inv.ExpiresAt.After(now) {
			return domain.ErrConflict
		}
		if inv.MaxUses >= 0 && inv.UsedCount >= inv.MaxUses {
			return domain.ErrConflict
		}
		inv.UsedCount++
		return nil
	}
	return domain.ErrNotFound
}

func (r *fakeInviteRepo) Revoke(ctx context.Context, groupID int64, code string) error {
	inv, ok := r.invites[code]
	if !ok || inv.GroupID != groupID || inv.RevokedAt != nil {
		return domain.ErrNotFound
	}
	now := time.Now().UTC()
	inv.RevokedAt = &now
	return nil
}

func (r *fakeInviteRepo) ListByGroup(ctx context.Context, groupID int64) ([]domain.Invite, error) {
	out := []domain.Invite{}
	for _, inv := range r.invites {
		if inv.GroupID == groupID {
			out = append(out, *inv)
		}
	}
	return out, nil
}

type counterKey struct {
	userID int64
	action string
	window time.Time
}

type fakeCounterRepo struct {
	counts map[counterKey]int
}

func newFakeCounterRepo() *fakeCounterRepo {
	return &fakeCounterRepo{counts: map[counterKey]int{}}
}

func (r *fakeCounterRepo) IncAndCheck(ctx context.Context, userID int64, action string, windowStart time.Time, limit int) (int, error) {
	k := counterKey{userID, action, windowStart}
	r.counts[k]++
	return r.counts[k], nil
}

type fakeBindingRepo struct{}

func (fakeBindingRepo) Create(ctx context.Context, b *domain.ChatBinding) error { return nil }
func (fakeBindingRepo) GetByGroup(ctx context.Context, groupID int64) (*domain.ChatBinding, error) {
	return nil, domain.ErrNotFound
}
func (fakeBindingRepo) GetByChat(ctx context.Context, chatID int64, threadID *int64) (*domain.ChatBinding, error) {
	return nil, domain.ErrNotFound
}
func (fakeBindingRepo) Delete(ctx context.Context, groupID int64) error { return nil }

type fakeAuditRepo struct {
	entries []*domain.AuditEntry
}

func (r *fakeAuditRepo) Write(ctx context.Context, e *domain.AuditEntry) error {
	cp := *e
	r.entries = append(r.entries, &cp)
	return nil
}

// --- harness ---

type fixture struct {
	svc      *Service
	groups   *fakeGroupRepo
	members  *fakeMembershipRepo
	invites  *fakeInviteRepo
	counters *fakeCounterRepo
	audit    *fakeAuditRepo
	clock    *fakeClock
}

func newFixture(cfg Config) *fixture {
	clock := &fakeClock{now: time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)}
	f := &fixture{
		groups:   newFakeGroupRepo(),
		members:  newFakeMembershipRepo(),
		invites:  newFakeInviteRepo(clock),
		counters: newFakeCounterRepo(),
		audit:    &fakeAuditRepo{},
		clock:    clock,
	}
	if cfg.PendingTTL == 0 {
		cfg.PendingTTL = 14 * 24 * time.Hour
	}
	if cfg.CreateDayLimit == 0 {
		cfg.CreateDayLimit = 3
	}
	if cfg.CreateWeekLimit == 0 {
		cfg.CreateWeekLimit = 5
	}
	if cfg.InviteDefaultTTL == 0 {
		cfg.InviteDefaultTTL = 7 * 24 * time.Hour
	}
	f.svc = NewService(f.groups, f.members, f.invites, f.counters, f.audit,
		fakeBindingRepo{}, cfg, f.clock, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return f
}

func user(id int64, superadmin bool) *domain.User {
	return &domain.User{ID: id, TelegramID: 1000 + id, IsSuperadmin: superadmin}
}

func sha(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// --- Create ---

func TestCreate_NormalizesSlug(t *testing.T) {
	f := newFixture(Config{})
	ctx := context.Background()

	g, err := f.svc.Create(ctx, user(1, false), " м8о-401б-23 ", "Группа")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if g.Slug != "М8О-401Б-23" {
		t.Errorf("slug = %q, want normalized М8О-401Б-23", g.Slug)
	}
}

func TestCreate_StrictValidationRejectsGarbage(t *testing.T) {
	f := newFixture(Config{})
	ctx := context.Background()

	for _, slug := range []string{"AB", " ABCDEFG!", "БЕЗЦИФР", "М8О--401"} {
		_, err := f.svc.Create(ctx, user(1, false), slug, "T")
		if !errors.Is(err, domain.ErrInvalidSlug) {
			t.Errorf("Create(%q) err = %v, want ErrInvalidSlug", slug, err)
		}
	}
}

func TestCreate_SuperadminBypassesRegex(t *testing.T) {
	f := newFixture(Config{})
	ctx := context.Background()

	g, err := f.svc.Create(ctx, user(1, true), "test-group", "Любой слаг")
	if err != nil {
		t.Fatalf("superadmin Create: %v", err)
	}
	if g.SlugNorm != "TEST-GROUP" {
		t.Errorf("slug_norm = %q, want TEST-GROUP", g.SlugNorm)
	}
}

func TestCreate_PendingStatusAndClaimExpiry(t *testing.T) {
	f := newFixture(Config{PendingTTL: 14 * 24 * time.Hour})
	ctx := context.Background()

	g, err := f.svc.Create(ctx, user(1, false), "ИКБО-33-21", "T")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if g.Status != domain.GroupStatusPending {
		t.Errorf("status = %q, want pending", g.Status)
	}
	want := f.clock.now.Add(14 * 24 * time.Hour)
	if g.ClaimExpiresAt == nil || !g.ClaimExpiresAt.Equal(want) {
		t.Errorf("claim_expires_at = %v, want %v", g.ClaimExpiresAt, want)
	}
	if g.CreatedBy != 1 {
		t.Errorf("created_by = %d, want 1", g.CreatedBy)
	}
	if len(f.audit.entries) == 0 || f.audit.entries[0].Action != "group.create" {
		t.Errorf("audit entries = %+v, want group.create", f.audit.entries)
	}
}

func TestCreate_SlugConflictPassthrough(t *testing.T) {
	f := newFixture(Config{})
	ctx := context.Background()

	if _, err := f.svc.Create(ctx, user(1, false), "ИКБО-33-21", "A"); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	_, err := f.svc.Create(ctx, user(2, false), "икбо-33-21", "B")
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("second Create err = %v, want ErrConflict", err)
	}
}

func TestCreate_CreatorGetsMemberRoleNotAdmin(t *testing.T) {
	f := newFixture(Config{})
	ctx := context.Background()

	g, err := f.svc.Create(ctx, user(1, false), "ИКБО-33-21", "T")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	m, err := f.members.Get(ctx, g.ID, 1)
	if err != nil {
		t.Fatalf("membership not created: %v", err)
	}
	if m.Role != domain.RoleMember {
		t.Errorf("creator role = %q, want member (spec §3.1: создатель НЕ админ)", m.Role)
	}
}

func TestCreate_DayLimit(t *testing.T) {
	f := newFixture(Config{CreateDayLimit: 3, CreateWeekLimit: 100})
	ctx := context.Background()

	for i, slug := range []string{"А-111", "А-222", "А-333"} {
		if _, err := f.svc.Create(ctx, user(1, false), slug, "T"); err != nil {
			t.Fatalf("Create #%d: %v", i+1, err)
		}
	}
	_, err := f.svc.Create(ctx, user(1, false), "А-444", "T")
	var rle *domain.RateLimitError
	if !errors.As(err, &rle) {
		t.Fatalf("4th Create err = %v, want RateLimitError", err)
	}
	if rle.RetryAfter <= 0 || rle.RetryAfter > 24*time.Hour {
		t.Errorf("RetryAfter = %v, want (0, 24h]", rle.RetryAfter)
	}
	if !errors.Is(err, domain.ErrRateLimit) {
		t.Errorf("RateLimitError must unwrap to ErrRateLimit")
	}
}

func TestCreate_WeekLimit(t *testing.T) {
	// Дневной лимит поднят, чтобы изолировать недельный.
	f := newFixture(Config{CreateDayLimit: 100, CreateWeekLimit: 5})
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		slug := fmt.Sprintf("ГРУППА-%d", i+1)
		if _, err := f.svc.Create(ctx, user(1, false), slug, "T"); err != nil {
			t.Fatalf("Create #%d: %v", i+1, err)
		}
	}
	_, err := f.svc.Create(ctx, user(1, false), "ГРУППА-6", "T")
	var rle *domain.RateLimitError
	if !errors.As(err, &rle) {
		t.Fatalf("6th Create err = %v, want RateLimitError", err)
	}
	if rle.RetryAfter <= 0 || rle.RetryAfter > 168*time.Hour {
		t.Errorf("RetryAfter = %v, want (0, 168h]", rle.RetryAfter)
	}
}

func TestCreate_EmptyTitleRejected(t *testing.T) {
	f := newFixture(Config{})
	if _, err := f.svc.Create(context.Background(), user(1, false), "А-111", "   "); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("err = %v, want ErrValidation", err)
	}
}

// --- Search ---

func TestSearch_NormalizesAndLimits(t *testing.T) {
	f := newFixture(Config{CreateDayLimit: 100, CreateWeekLimit: 100})
	ctx := context.Background()

	for i := 0; i < 25; i++ {
		g := &domain.Group{
			Slug: fmt.Sprintf("ИТМО-%02d-1", i), Title: "T",
			Status: domain.GroupStatusActive, CreatedBy: 1,
		}
		if err := f.groups.Create(ctx, g); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	got, err := f.svc.Search(ctx, user(2, false), "итмо-0")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 10 {
		t.Errorf("prefix ИТМО-0 matched %d groups, want 10", len(got))
	}
	got, err = f.svc.Search(ctx, user(2, false), "итмо")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 20 {
		t.Errorf("Search returned %d groups, want limit 20", len(got))
	}
	// Единая форма: MyGroup с ролью ("" для не-участника).
	for _, mg := range got {
		if mg.Role != "" {
			t.Errorf("role = %q for stranger, want empty", mg.Role)
		}
	}
}

func TestSearch_CarriesCallerRole(t *testing.T) {
	f := newFixture(Config{CreateDayLimit: 100, CreateWeekLimit: 100})
	ctx := context.Background()
	g := seedGroupWithAdmin(t, f, 1, "РОЛЬ-11")

	got, err := f.svc.Search(ctx, user(1, false), "роль")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 1 || got[0].Group.ID != g.ID || got[0].Role != domain.RoleAdmin {
		t.Errorf("Search = %+v, want group %d with role admin", got, g.ID)
	}
}

// --- Invites ---

// seedGroupWithAdmin создаёт активную группу и делает actorID её админом
// (через SQL-подобный SetRole — в обход claim, который в Task 10). Группа
// активируется: redeem в pending-группу запрещён чужим (finding #3).
func seedGroupWithAdmin(t *testing.T, f *fixture, adminID int64, slug string) *domain.Group {
	t.Helper()
	ctx := context.Background()
	g, err := f.svc.Create(ctx, user(adminID, false), slug, "T")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := f.members.SetRole(ctx, g.ID, adminID, domain.RoleAdmin); err != nil {
		t.Fatalf("SetRole: %v", err)
	}
	if err := f.groups.SetStatus(ctx, g.ID, domain.GroupStatusActive); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	return g
}

func TestCreateInvite_CodeStoredAsHash(t *testing.T) {
	f := newFixture(Config{})
	ctx := context.Background()
	g := seedGroupWithAdmin(t, f, 1, "А-111")

	code, inv, err := f.svc.CreateInvite(ctx, user(1, false), g.ID, domain.RoleMember, 5, 0)
	if err != nil {
		t.Fatalf("CreateInvite: %v", err)
	}
	if len(code) != 8 {
		t.Errorf("code = %q, want 8 chars", code)
	}
	for _, r := range code {
		if !strings.ContainsRune("ABCDEFGHJKMNPQRSTUVWXYZ23456789", r) {
			t.Errorf("code %q contains ambiguous char %q", code, r)
		}
	}
	if inv.Code != sha(code) {
		t.Errorf("stored code = %q, want sha256 hex of plaintext", inv.Code)
	}
	wantExpiry := f.clock.now.Add(7 * 24 * time.Hour) // ttl=0 → InviteDefaultTTL
	if !inv.ExpiresAt.Equal(wantExpiry) {
		t.Errorf("expires_at = %v, want default TTL %v", inv.ExpiresAt, wantExpiry)
	}
	if inv.MaxUses != 5 {
		t.Errorf("max_uses = %d, want 5", inv.MaxUses)
	}
}

func TestCreateInvite_NonAdminForbidden(t *testing.T) {
	f := newFixture(Config{})
	ctx := context.Background()
	g := seedGroupWithAdmin(t, f, 1, "А-111")
	if err := f.members.Upsert(ctx, &domain.Membership{GroupID: g.ID, UserID: 2, Role: domain.RoleMember}); err != nil {
		t.Fatal(err)
	}
	_, _, err := f.svc.CreateInvite(ctx, user(2, false), g.ID, domain.RoleMember, 1, time.Hour)
	if !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
}

func TestCreateInvite_BadRole(t *testing.T) {
	f := newFixture(Config{})
	ctx := context.Background()
	g := seedGroupWithAdmin(t, f, 1, "А-111")
	_, _, err := f.svc.CreateInvite(ctx, user(1, false), g.ID, domain.Role("owner"), 1, time.Hour)
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("err = %v, want ErrValidation", err)
	}
}

func TestRedeemInvite_HappyPath(t *testing.T) {
	f := newFixture(Config{})
	ctx := context.Background()
	g := seedGroupWithAdmin(t, f, 1, "А-111")
	code, _, err := f.svc.CreateInvite(ctx, user(1, false), g.ID, domain.RoleAdmin, 3, 24*time.Hour)
	if err != nil {
		t.Fatalf("CreateInvite: %v", err)
	}

	got, err := f.svc.RedeemInvite(ctx, user(2, false), code)
	if err != nil {
		t.Fatalf("RedeemInvite: %v", err)
	}
	if got.ID != g.ID {
		t.Errorf("redeemed group = %d, want %d", got.ID, g.ID)
	}
	m, err := f.members.Get(ctx, g.ID, 2)
	if err != nil {
		t.Fatalf("membership: %v", err)
	}
	if m.Role != domain.RoleAdmin {
		t.Errorf("role = %q, want admin (роль из инвайта)", m.Role)
	}
	stored, err := f.invites.GetByCode(ctx, sha(code))
	if err != nil || stored.UsedCount != 1 {
		t.Errorf("used_count = %v (err %v), want 1", stored.UsedCount, err)
	}
}

func TestRedeemInvite_ExpiredNotFound(t *testing.T) {
	f := newFixture(Config{})
	ctx := context.Background()
	g := seedGroupWithAdmin(t, f, 1, "А-111")
	code, _, err := f.svc.CreateInvite(ctx, user(1, false), g.ID, domain.RoleMember, 1, time.Hour)
	if err != nil {
		t.Fatalf("CreateInvite: %v", err)
	}
	f.clock.now = f.clock.now.Add(2 * time.Hour)

	_, err = f.svc.RedeemInvite(ctx, user(2, false), code)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expired redeem err = %v, want ErrNotFound", err)
	}
}

func TestRedeemInvite_RevokedNotFound(t *testing.T) {
	f := newFixture(Config{})
	ctx := context.Background()
	g := seedGroupWithAdmin(t, f, 1, "А-111")
	code, _, err := f.svc.CreateInvite(ctx, user(1, false), g.ID, domain.RoleMember, 1, time.Hour)
	if err != nil {
		t.Fatalf("CreateInvite: %v", err)
	}
	if err := f.svc.RevokeInvite(ctx, user(1, false), g.ID, code); err != nil {
		t.Fatalf("RevokeInvite: %v", err)
	}
	_, err = f.svc.RedeemInvite(ctx, user(2, false), code)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("revoked redeem err = %v, want ErrNotFound (не утечка существования)", err)
	}
}

func TestRedeemInvite_MaxUsesConflict(t *testing.T) {
	f := newFixture(Config{})
	ctx := context.Background()
	g := seedGroupWithAdmin(t, f, 1, "А-111")
	code, _, err := f.svc.CreateInvite(ctx, user(1, false), g.ID, domain.RoleMember, 1, time.Hour)
	if err != nil {
		t.Fatalf("CreateInvite: %v", err)
	}
	if _, err := f.svc.RedeemInvite(ctx, user(2, false), code); err != nil {
		t.Fatalf("first redeem: %v", err)
	}
	_, err = f.svc.RedeemInvite(ctx, user(3, false), code)
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("exhausted redeem err = %v, want ErrConflict", err)
	}
}

func TestRedeemInvite_MemberIdempotent(t *testing.T) {
	f := newFixture(Config{})
	ctx := context.Background()
	g := seedGroupWithAdmin(t, f, 1, "А-111")
	code, _, err := f.svc.CreateInvite(ctx, user(1, false), g.ID, domain.RoleMember, 5, time.Hour)
	if err != nil {
		t.Fatalf("CreateInvite: %v", err)
	}
	if _, err := f.svc.RedeemInvite(ctx, user(2, false), code); err != nil {
		t.Fatalf("first redeem: %v", err)
	}
	if _, err := f.svc.RedeemInvite(ctx, user(2, false), code); err != nil {
		t.Fatalf("re-redeem by member err = %v, want idempotent success", err)
	}
	stored, err := f.invites.GetByCode(ctx, sha(code))
	if err != nil {
		t.Fatal(err)
	}
	if stored.UsedCount != 1 {
		t.Errorf("used_count = %d, want 1 (повторный redeem не инкрементит)", stored.UsedCount)
	}
}

// --- Redeem: атомарность и статус группы ---

// TestRedeemInvite_ExhaustedAtRepoLevel — used_count уже на максимуме:
// атомарный IncrementUsed (fake моделирует условный SQL) возвращает
// ErrConflict, и membership НЕ создаётся.
func TestRedeemInvite_ExhaustedAtRepoLevel(t *testing.T) {
	f := newFixture(Config{})
	ctx := context.Background()
	g := seedGroupWithAdmin(t, f, 1, "А-111")
	code, _, err := f.svc.CreateInvite(ctx, user(1, false), g.ID, domain.RoleMember, 1, time.Hour)
	if err != nil {
		t.Fatalf("CreateInvite: %v", err)
	}
	// Исчерпываем лимит «в обход» redeem — как если бы гонка уже случилась.
	stored, err := f.invites.GetByCode(ctx, sha(code))
	if err != nil {
		t.Fatal(err)
	}
	for _, inv := range f.invites.invites {
		if inv.ID == stored.ID {
			inv.UsedCount = inv.MaxUses
		}
	}
	_, err = f.svc.RedeemInvite(ctx, user(2, false), code)
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("exhausted redeem err = %v, want ErrConflict from atomic increment", err)
	}
	if _, err := f.members.Get(ctx, g.ID, 2); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("membership created despite conflict: %v", err)
	}
}

func TestRedeemInvite_PendingGroupNotFound(t *testing.T) {
	f := newFixture(Config{})
	ctx := context.Background()
	// Группа остаётся pending (без активации) — чужой участник войти не может.
	g, err := f.svc.Create(ctx, user(1, false), "П-111", "T")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := f.members.SetRole(ctx, g.ID, 1, domain.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	code, _, err := f.svc.CreateInvite(ctx, user(1, false), g.ID, domain.RoleMember, 1, time.Hour)
	if err != nil {
		t.Fatalf("CreateInvite: %v", err)
	}
	_, err = f.svc.RedeemInvite(ctx, user(2, false), code)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("pending redeem err = %v, want ErrNotFound (без утечки существования)", err)
	}
	if _, err := f.members.Get(ctx, g.ID, 2); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("stranger joined pending group: %v", err)
	}
	// Создатель — может.
	if _, err := f.svc.RedeemInvite(ctx, user(1, false), code); err != nil {
		t.Fatalf("creator redeem in pending group: %v", err)
	}
}

// --- CreateInvite: границы ---

func TestCreateInvite_Bounds(t *testing.T) {
	f := newFixture(Config{})
	ctx := context.Background()
	g := seedGroupWithAdmin(t, f, 1, "А-111")

	for _, maxUses := range []int{0, -2} {
		_, _, err := f.svc.CreateInvite(ctx, user(1, false), g.ID, domain.RoleMember, maxUses, time.Hour)
		if !errors.Is(err, domain.ErrValidation) {
			t.Errorf("max_uses=%d err = %v, want ErrValidation", maxUses, err)
		}
	}
	// -1 = без лимита — валидно.
	if _, _, err := f.svc.CreateInvite(ctx, user(1, false), g.ID, domain.RoleMember, -1, time.Hour); err != nil {
		t.Errorf("max_uses=-1 err = %v, want nil (unlimited)", err)
	}
	if _, _, err := f.svc.CreateInvite(ctx, user(1, false), g.ID, domain.RoleMember, 1, 91*24*time.Hour); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("ttl=91d err = %v, want ErrValidation", err)
	}
}

func TestRedeemInvite_UnlimitedUses(t *testing.T) {
	f := newFixture(Config{})
	ctx := context.Background()
	g := seedGroupWithAdmin(t, f, 1, "А-111")
	code, _, err := f.svc.CreateInvite(ctx, user(1, false), g.ID, domain.RoleMember, -1, time.Hour)
	if err != nil {
		t.Fatalf("CreateInvite: %v", err)
	}
	for i := int64(2); i <= 5; i++ {
		if _, err := f.svc.RedeemInvite(ctx, user(i, false), code); err != nil {
			t.Fatalf("redeem #%d: %v", i-1, err)
		}
	}
}

// --- Membership admin ops ---

func TestSetRole_LastAdminGuard(t *testing.T) {
	f := newFixture(Config{})
	ctx := context.Background()
	g := seedGroupWithAdmin(t, f, 1, "А-111")

	// Единственный админ не может понизить себя.
	err := f.svc.SetRole(ctx, user(1, false), g.ID, 1, domain.RoleMember)
	if !errors.Is(err, domain.ErrConflict) || !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("demote last admin err = %v, want ErrConflict+ErrLastAdmin", err)
	}

	// С двумя админами понижение разрешено.
	if err := f.members.Upsert(ctx, &domain.Membership{GroupID: g.ID, UserID: 2, Role: domain.RoleMember}); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.SetRole(ctx, user(1, false), g.ID, 2, domain.RoleAdmin); err != nil {
		t.Fatalf("promote: %v", err)
	}
	if err := f.svc.SetRole(ctx, user(1, false), g.ID, 1, domain.RoleMember); err != nil {
		t.Fatalf("demote with second admin: %v", err)
	}
}

func TestSetRole_SequentialDemotesGuardLastAdmin(t *testing.T) {
	f := newFixture(Config{})
	ctx := context.Background()
	g := seedGroupWithAdmin(t, f, 1, "А-111")
	for _, uid := range []int64{2, 3} {
		if err := f.members.Upsert(ctx, &domain.Membership{GroupID: g.ID, UserID: uid, Role: domain.RoleAdmin}); err != nil {
			t.Fatal(err)
		}
	}
	// Три админа: первые две демotion'ы проходят, третья (последний админ) — конфликт.
	if err := f.svc.SetRole(ctx, user(1, false), g.ID, 2, domain.RoleMember); err != nil {
		t.Fatalf("first demote: %v", err)
	}
	if err := f.svc.SetRole(ctx, user(1, false), g.ID, 3, domain.RoleMember); err != nil {
		t.Fatalf("second demote: %v", err)
	}
	err := f.svc.SetRole(ctx, user(1, false), g.ID, 1, domain.RoleMember)
	if !errors.Is(err, domain.ErrConflict) || !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("third (last-admin) demote err = %v, want ErrConflict+ErrLastAdmin", err)
	}
}

func TestRemoveMember_SuperadminCannotRemoveLastAdmin(t *testing.T) {
	f := newFixture(Config{})
	ctx := context.Background()
	g := seedGroupWithAdmin(t, f, 1, "А-111")

	// Единственный админ: кик запрещён даже superadmin — группу нельзя «осиротить»
	// (вместо этого группу можно удалить через Delete).
	err := f.svc.RemoveMember(ctx, user(9, true), g.ID, 1)
	if !errors.Is(err, domain.ErrConflict) || !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("superadmin removing last admin err = %v, want ErrConflict+ErrLastAdmin", err)
	}
	if _, err := f.members.Get(ctx, g.ID, 1); err != nil {
		t.Errorf("last admin membership gone: %v", err)
	}
}

func TestSetRole_NonAdminForbidden(t *testing.T) {
	f := newFixture(Config{})
	ctx := context.Background()
	g := seedGroupWithAdmin(t, f, 1, "А-111")
	if err := f.members.Upsert(ctx, &domain.Membership{GroupID: g.ID, UserID: 2, Role: domain.RoleMember}); err != nil {
		t.Fatal(err)
	}
	err := f.svc.SetRole(ctx, user(2, false), g.ID, 2, domain.RoleAdmin)
	if !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
}

func TestRemoveMember_AdminCannotKickAdmin(t *testing.T) {
	f := newFixture(Config{})
	ctx := context.Background()
	g := seedGroupWithAdmin(t, f, 1, "А-111")
	if err := f.members.Upsert(ctx, &domain.Membership{GroupID: g.ID, UserID: 2, Role: domain.RoleAdmin}); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.RemoveMember(ctx, user(1, false), g.ID, 2); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("admin kicking admin err = %v, want ErrForbidden", err)
	}
	// Superadmin может.
	if err := f.svc.RemoveMember(ctx, user(1, true), g.ID, 2); err != nil {
		t.Fatalf("superadmin kick: %v", err)
	}
}

func TestLeave_LastAdminConflict(t *testing.T) {
	f := newFixture(Config{})
	ctx := context.Background()
	g := seedGroupWithAdmin(t, f, 1, "А-111")

	err := f.svc.Leave(ctx, user(1, false), g.ID)
	if !errors.Is(err, domain.ErrConflict) || !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("last admin Leave err = %v, want ErrConflict+ErrLastAdmin", err)
	}

	// Обычный участник выходит без ограничений.
	if err := f.members.Upsert(ctx, &domain.Membership{GroupID: g.ID, UserID: 2, Role: domain.RoleMember}); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Leave(ctx, user(2, false), g.ID); err != nil {
		t.Fatalf("member Leave: %v", err)
	}
	if _, err := f.members.Get(ctx, g.ID, 2); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("membership after Leave: %v, want gone", err)
	}
}

// --- Get / Update / Delete / ListMine ---

func TestGet_PendingVisibleOnlyToCreator(t *testing.T) {
	f := newFixture(Config{})
	ctx := context.Background()
	g, err := f.svc.Create(ctx, user(1, false), "А-111", "T")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := f.svc.Get(ctx, user(2, false), g.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("pending Get by stranger err = %v, want ErrNotFound", err)
	}
	view, err := f.svc.Get(ctx, user(1, false), g.ID)
	if err != nil {
		t.Fatalf("Get by creator: %v", err)
	}
	if view.Role != domain.RoleMember {
		t.Errorf("role = %q, want member", view.Role)
	}
	if view.MembersCount != 1 {
		t.Errorf("members_count = %d, want 1", view.MembersCount)
	}
	if view.Binding != nil {
		t.Errorf("binding = %+v, want nil", view.Binding)
	}
}

func TestUpdate_PresetsValidationAndAdminOnly(t *testing.T) {
	f := newFixture(Config{})
	ctx := context.Background()
	g := seedGroupWithAdmin(t, f, 1, "А-111")
	if err := f.members.Upsert(ctx, &domain.Membership{GroupID: g.ID, UserID: 2, Role: domain.RoleMember}); err != nil {
		t.Fatal(err)
	}

	title := "Новое имя"
	if _, err := f.svc.Update(ctx, user(2, false), g.ID, &title, nil); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("member Update err = %v, want ErrForbidden", err)
	}

	bad := []time.Duration{time.Minute} // < 5 минут
	if _, err := f.svc.Update(ctx, user(1, false), g.ID, nil, &bad); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("presets <5min err = %v, want ErrValidation", err)
	}
	tooMany := make([]time.Duration, 11)
	for i := range tooMany {
		tooMany[i] = 10 * time.Minute
	}
	if _, err := f.svc.Update(ctx, user(1, false), g.ID, nil, &tooMany); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("11 presets err = %v, want ErrValidation", err)
	}

	ok := []time.Duration{5 * time.Minute, 24 * time.Hour}
	updated, err := f.svc.Update(ctx, user(1, false), g.ID, &title, &ok)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Title != title || len(updated.DefaultPresets) != 2 {
		t.Errorf("updated = %+v", updated)
	}
}

func TestDelete_AdminOrSuperadmin(t *testing.T) {
	f := newFixture(Config{})
	ctx := context.Background()
	g := seedGroupWithAdmin(t, f, 1, "А-111")
	if err := f.members.Upsert(ctx, &domain.Membership{GroupID: g.ID, UserID: 2, Role: domain.RoleMember}); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Delete(ctx, user(2, false), g.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("member Delete err = %v, want ErrForbidden", err)
	}
	if err := f.svc.Delete(ctx, user(3, true), g.ID); err != nil {
		t.Fatalf("superadmin Delete: %v", err)
	}
	if _, err := f.groups.GetByID(ctx, g.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("group after Delete: %v, want soft-deleted", err)
	}
}

func TestListMine_WithRoles(t *testing.T) {
	f := newFixture(Config{CreateDayLimit: 10, CreateWeekLimit: 10})
	ctx := context.Background()
	g1, err := f.svc.Create(ctx, user(1, false), "А-111", "T1")
	if err != nil {
		t.Fatal(err)
	}
	g2, err := f.svc.Create(ctx, user(2, false), "Б-222", "T2")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.members.Upsert(ctx, &domain.Membership{GroupID: g2.ID, UserID: 1, Role: domain.RoleMember}); err != nil {
		t.Fatal(err)
	}
	if err := f.members.SetRole(ctx, g1.ID, 1, domain.RoleAdmin); err != nil {
		t.Fatal(err)
	}

	mine, err := f.svc.ListMine(ctx, user(1, false))
	if err != nil {
		t.Fatalf("ListMine: %v", err)
	}
	if len(mine) != 2 {
		t.Fatalf("ListMine = %d groups, want 2", len(mine))
	}
	roles := map[int64]domain.Role{}
	for _, mg := range mine {
		roles[mg.Group.ID] = mg.Role
	}
	if roles[g1.ID] != domain.RoleAdmin || roles[g2.ID] != domain.RoleMember {
		t.Errorf("roles = %v", roles)
	}
}
