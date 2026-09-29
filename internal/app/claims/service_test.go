package claims

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/sauron/deadliner/internal/domain"
)

// --- fakes ---

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

func (c *fakeClock) advance(d time.Duration) { c.now = c.now.Add(d) }

type fakeGroupRepo struct {
	mu     sync.Mutex
	groups map[int64]*domain.Group
}

func newFakeGroupRepo() *fakeGroupRepo { return &fakeGroupRepo{groups: map[int64]*domain.Group{}} }

func (r *fakeGroupRepo) Create(ctx context.Context, g *domain.Group) error {
	if g.ID == 0 {
		g.ID = int64(len(r.groups) + 1)
	}
	cp := *g
	r.groups[g.ID] = &cp
	return nil
}

func (r *fakeGroupRepo) GetByID(ctx context.Context, id int64) (*domain.Group, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	g, ok := r.groups[id]
	if !ok {
		return nil, fmt.Errorf("%w: group id=%d", domain.ErrNotFound, id)
	}
	cp := *g
	return &cp, nil
}

func (r *fakeGroupRepo) GetBySlugNorm(ctx context.Context, slugNorm string) (*domain.Group, error) {
	for _, g := range r.groups {
		if g.SlugNorm == slugNorm {
			cp := *g
			return &cp, nil
		}
	}
	return nil, domain.ErrNotFound
}

func (r *fakeGroupRepo) SearchByPrefix(ctx context.Context, prefix string, callerID int64, limit int) ([]domain.Group, error) {
	return nil, errors.New("not used")
}
func (r *fakeGroupRepo) Update(ctx context.Context, g *domain.Group) error {
	return errors.New("not used")
}

func (r *fakeGroupRepo) SetStatus(ctx context.Context, id int64, status domain.GroupStatus) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	g, ok := r.groups[id]
	if !ok {
		return domain.ErrNotFound
	}
	g.Status = status
	return nil
}

func (r *fakeGroupRepo) SoftDelete(ctx context.Context, id int64) error {
	return errors.New("not used")
}
func (r *fakeGroupRepo) ListMine(ctx context.Context, userID int64) ([]domain.Group, error) {
	return nil, errors.New("not used")
}
func (r *fakeGroupRepo) ListPendingExpired(ctx context.Context, now time.Time, limit int) ([]domain.Group, error) {
	return nil, errors.New("not used")
}

type memKey struct{ groupID, userID int64 }

type fakeMembershipRepo struct {
	mu   sync.Mutex
	mems map[memKey]*domain.Membership
}

func newFakeMembershipRepo() *fakeMembershipRepo {
	return &fakeMembershipRepo{mems: map[memKey]*domain.Membership{}}
}

// Upsert зеркалит репо: роль существующего участника не меняется.
func (r *fakeMembershipRepo) Upsert(ctx context.Context, m *domain.Membership) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	k := memKey{m.GroupID, m.UserID}
	if _, ok := r.mems[k]; ok {
		return nil
	}
	cp := *m
	if cp.Role == "" {
		cp.Role = domain.RoleMember
	}
	r.mems[k] = &cp
	return nil
}

func (r *fakeMembershipRepo) Get(ctx context.Context, groupID, userID int64) (*domain.Membership, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	m, ok := r.mems[memKey{groupID, userID}]
	if !ok {
		return nil, fmt.Errorf("%w: membership group=%d user=%d", domain.ErrNotFound, groupID, userID)
	}
	cp := *m
	return &cp, nil
}

func (r *fakeMembershipRepo) ListByGroup(ctx context.Context, groupID int64) ([]domain.Membership, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []domain.Membership{}
	for _, m := range r.mems {
		if m.GroupID == groupID {
			out = append(out, *m)
		}
	}
	return out, nil
}

func (r *fakeMembershipRepo) ListByGroupDetailed(ctx context.Context, groupID int64) ([]domain.MembershipDetail, error) {
	return nil, errors.New("not used")
}
func (r *fakeMembershipRepo) ListByUser(ctx context.Context, userID int64) ([]domain.Membership, error) {
	return nil, errors.New("not used")
}

func (r *fakeMembershipRepo) SetRole(ctx context.Context, groupID, userID int64, role domain.Role) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	m, ok := r.mems[memKey{groupID, userID}]
	if !ok {
		return domain.ErrNotFound
	}
	m.Role = role
	return nil
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
	r.mu.Lock()
	defer r.mu.Unlock()
	k := memKey{groupID, userID}
	if _, ok := r.mems[k]; !ok {
		return fmt.Errorf("%w: membership", domain.ErrNotFound)
	}
	delete(r.mems, k)
	return nil
}
func (r *fakeMembershipRepo) CountAdmins(ctx context.Context, groupID int64) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, m := range r.mems {
		if m.GroupID == groupID && m.Role == domain.RoleAdmin {
			n++
		}
	}
	return n, nil
}

type fakeBindingRepo struct {
	bindings map[int64]*domain.ChatBinding
}

func newFakeBindingRepo() *fakeBindingRepo {
	return &fakeBindingRepo{bindings: map[int64]*domain.ChatBinding{}}
}

func (r *fakeBindingRepo) Create(ctx context.Context, b *domain.ChatBinding) error {
	if _, ok := r.bindings[b.GroupID]; ok {
		return domain.ErrConflict
	}
	cp := *b
	r.bindings[b.GroupID] = &cp
	return nil
}

func (r *fakeBindingRepo) GetByGroup(ctx context.Context, groupID int64) (*domain.ChatBinding, error) {
	b, ok := r.bindings[groupID]
	if !ok {
		return nil, fmt.Errorf("%w: binding group_id=%d", domain.ErrNotFound, groupID)
	}
	cp := *b
	return &cp, nil
}

func (r *fakeBindingRepo) GetByChat(ctx context.Context, chatID int64, threadID *int64) (*domain.ChatBinding, error) {
	return nil, domain.ErrNotFound
}
func (r *fakeBindingRepo) Delete(ctx context.Context, groupID int64) error {
	delete(r.bindings, groupID)
	return nil
}

// fakeClaimRepo моделирует условный SQL репо claims: MarkUsed гасит только
// неиспользованный код, а GetActiveByGroup фильтрует expires_at > now.
// fakeClaimRepo потокобезопасен (мьютекс): гонка двух Confirm в
// TestConfirmRaceExactlyOneWins выполняется в двух горутинах на общем фейке.
type fakeClaimRepo struct {
	mu      sync.Mutex
	codes   map[int64]*domain.ClaimCode
	nextID  int64
	clock   *fakeClock
	events  *[]string
	creates int
}

func newFakeClaimRepo(clock *fakeClock, events *[]string) *fakeClaimRepo {
	return &fakeClaimRepo{codes: map[int64]*domain.ClaimCode{}, nextID: 1, clock: clock, events: events}
}

func (r *fakeClaimRepo) Create(ctx context.Context, c *domain.ClaimCode) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	c.ID = r.nextID
	r.nextID++
	cp := *c
	r.codes[c.ID] = &cp
	r.creates++
	*r.events = append(*r.events, "claim.insert")
	return nil
}

func (r *fakeClaimRepo) GetActiveByGroup(ctx context.Context, groupID int64, now time.Time) (*domain.ClaimCode, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var newest *domain.ClaimCode
	for _, c := range r.codes {
		if c.GroupID != groupID || c.UsedAt != nil || !c.ExpiresAt.After(now) {
			continue
		}
		if newest == nil || c.ID > newest.ID {
			newest = c
		}
	}
	if newest == nil {
		return nil, fmt.Errorf("%w: active claim code group_id=%d", domain.ErrNotFound, groupID)
	}
	cp := *newest
	return &cp, nil
}

func (r *fakeClaimRepo) ListActiveByGroup(ctx context.Context, groupID int64, now time.Time) ([]domain.ClaimCode, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []domain.ClaimCode{}
	for _, c := range r.codes {
		if c.GroupID == groupID && c.UsedAt == nil && c.ExpiresAt.After(now) {
			out = append(out, *c)
		}
	}
	return out, nil
}

// MarkUsed зеркалит условный UPDATE репо: под мьютексом проверка «уже
// погашен» и запись used_at атомарны, поэтому гонка даёт ровно одного
// победителя.
func (r *fakeClaimRepo) MarkUsed(ctx context.Context, id int64, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.codes[id]
	if !ok || c.UsedAt != nil {
		return fmt.Errorf("%w: claim code id=%d already used", domain.ErrNotFound, id)
	}
	used := now
	c.UsedAt = &used
	return nil
}

func (r *fakeClaimRepo) RevokeActiveByGroup(ctx context.Context, groupID int64, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range r.codes {
		if c.GroupID == groupID && c.UsedAt == nil {
			used := now
			c.UsedAt = &used
		}
	}
	return nil
}

type counterKey struct {
	userID int64
	action string
	window time.Time
}

type fakeCounterRepo struct{ counts map[counterKey]int }

func newFakeCounterRepo() *fakeCounterRepo {
	return &fakeCounterRepo{counts: map[counterKey]int{}}
}

func (r *fakeCounterRepo) IncAndCheck(ctx context.Context, userID int64, action string, windowStart time.Time, limit int) (int, error) {
	k := counterKey{userID, action, windowStart}
	r.counts[k]++
	return r.counts[k], nil
}

type fakeUserRepo struct {
	mu    sync.Mutex
	users map[int64]*domain.User
}

func newFakeUserRepo() *fakeUserRepo { return &fakeUserRepo{users: map[int64]*domain.User{}} }

func (r *fakeUserRepo) add(id, telegramID int64) {
	r.users[id] = &domain.User{ID: id, TelegramID: telegramID}
}

func (r *fakeUserRepo) GetByTelegramID(ctx context.Context, telegramID int64) (*domain.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, u := range r.users {
		if u.TelegramID == telegramID {
			cp := *u
			return &cp, nil
		}
	}
	return nil, domain.ErrNotFound
}

func (r *fakeUserRepo) GetByID(ctx context.Context, id int64) (*domain.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.users[id]
	if !ok {
		return nil, fmt.Errorf("%w: user id=%d", domain.ErrNotFound, id)
	}
	cp := *u
	return &cp, nil
}
func (r *fakeUserRepo) UpsertByTelegram(ctx context.Context, u *domain.User) error {
	return errors.New("not used")
}
func (r *fakeUserRepo) UpdateSettings(ctx context.Context, id int64, tz string, dmNotifyDefault bool) error {
	return errors.New("not used")
}
func (r *fakeUserRepo) SetBanned(ctx context.Context, id int64, banned bool) error {
	return errors.New("not used")
}
func (r *fakeUserRepo) SetSuperadmin(ctx context.Context, id int64, superadmin bool) error {
	return errors.New("not used")
}
func (r *fakeUserRepo) MarkBotBlocked(ctx context.Context, telegramID int64, blocked bool) error {
	return errors.New("not used")
}

type fakeAuditRepo struct {
	mu      sync.Mutex
	entries []*domain.AuditEntry
}

func (r *fakeAuditRepo) Write(ctx context.Context, e *domain.AuditEntry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *e
	r.entries = append(r.entries, &cp)
	return nil
}

// fakeNotifier записывает порядок вызовов в общий журнал и умеет падать.
type fakeNotifier struct {
	mu      sync.Mutex
	events  *[]string
	chatErr error
	sent    []sentMessage
	dms     []int64
}

type sentMessage struct {
	chatID   int64
	threadID int64
	text     string
}

func (n *fakeNotifier) SendToChat(ctx context.Context, chatID, threadID int64, text string) (int64, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	*n.events = append(*n.events, "chat.send")
	if n.chatErr != nil {
		return 0, n.chatErr
	}
	n.sent = append(n.sent, sentMessage{chatID: chatID, threadID: threadID, text: text})
	return int64(len(n.sent) + 100), nil
}

func (n *fakeNotifier) SendToUser(ctx context.Context, userID int64, text string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	*n.events = append(*n.events, "dm.send")
	n.dms = append(n.dms, userID)
	return nil
}

// --- harness ---

type fixture struct {
	svc      *Service
	groups   *fakeGroupRepo
	members  *fakeMembershipRepo
	bindings *fakeBindingRepo
	claims   *fakeClaimRepo
	counters *fakeCounterRepo
	users    *fakeUserRepo
	audit    *fakeAuditRepo
	notifier *fakeNotifier
	clock    *fakeClock
	events   *[]string
	groupID  int64
}

// newFixture собирает service на фейках: pending-группа с слагом, создатель
// (member) и optional привязка чата -100500.
func newFixture(t *testing.T, withBinding bool) *fixture {
	t.Helper()
	events := &[]string{}
	clock := &fakeClock{now: time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)}

	f := &fixture{
		groups:   newFakeGroupRepo(),
		members:  newFakeMembershipRepo(),
		bindings: newFakeBindingRepo(),
		counters: newFakeCounterRepo(),
		users:    newFakeUserRepo(),
		audit:    &fakeAuditRepo{},
		clock:    clock,
		events:   events,
	}
	f.claims = newFakeClaimRepo(clock, events)
	f.notifier = &fakeNotifier{events: events}

	creatorTelegram := int64(5000)
	f.users.add(1, creatorTelegram)
	g := &domain.Group{
		Slug: "ИКБО-33-21", SlugNorm: "ИКБО-33-21", Title: "Моя группа",
		Status: domain.GroupStatusPending, CreatedBy: 1,
	}
	if err := f.groups.Create(context.Background(), g); err != nil {
		t.Fatalf("seed group: %v", err)
	}
	f.groupID = g.ID
	// Создатель — member (спека §3.1: создатель НЕ админ). Второй участник —
	// обычный member: claim-флоу требует участия в группе (403 иначе).
	for _, uid := range []int64{1, 2} {
		if err := f.members.Upsert(context.Background(), &domain.Membership{
			GroupID: g.ID, UserID: uid, Role: domain.RoleMember,
		}); err != nil {
			t.Fatalf("seed membership: %v", err)
		}
	}

	if withBinding {
		if err := f.bindings.Create(context.Background(), &domain.ChatBinding{
			GroupID: g.ID, ChatID: -100500, ChatTitle: "Чат", BoundBy: 1,
		}); err != nil {
			t.Fatalf("seed binding: %v", err)
		}
	}

	f.svc = NewService(f.groups, f.members, f.bindings, f.claims, f.counters,
		f.users, f.audit, f.notifier, Config{
			CodeTTL:          10 * time.Minute,
			RequestHourLimit: 3,
			Cooldown:         time.Minute,
		}, clock, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return f
}

func actor(id int64) *domain.User { return &domain.User{ID: id, TelegramID: 1000 + id} }

func rateLimited(t *testing.T, err error) *domain.RateLimitError {
	t.Helper()
	var rl *domain.RateLimitError
	if !errors.As(err, &rl) {
		t.Fatalf("err = %v, want RateLimitError", err)
	}
	return rl
}

// --- StartClaim ---

// Не-участник группы не может запросить код (403): иначе любой
// авторизованный пользователь спамил бы кодом в чужой привязанный чат.
func TestStartClaimRequiresMembership(t *testing.T) {
	f := newFixture(t, true)

	_, err := f.svc.StartClaim(context.Background(), actor(99), f.groupID)
	if !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("StartClaim(non-member) = %v, want ErrForbidden", err)
	}
	if len(f.notifier.sent) != 0 || f.claims.creates != 0 {
		t.Errorf("no chat message / no row expected for a non-member")
	}
}

// Создатель группы может запросить код, даже выйдя из membership.
func TestStartClaimCreatorAllowedAfterLeaving(t *testing.T) {
	f := newFixture(t, true)
	ctx := context.Background()

	if err := f.members.Delete(ctx, f.groupID, 1); err != nil {
		t.Fatalf("delete membership: %v", err)
	}
	if _, err := f.svc.StartClaim(ctx, actor(1), f.groupID); err != nil {
		t.Fatalf("StartClaim(creator) = %v, want success", err)
	}
}

// Группа без привязанного чата: код публиковать некуда → ErrConflict
// с маркером ErrNoBinding (HTTP 409 + подсказка про /bind_group).
func TestStartClaimWithoutBinding(t *testing.T) {
	f := newFixture(t, false)

	_, err := f.svc.StartClaim(context.Background(), actor(2), f.groupID)
	if !errors.Is(err, domain.ErrConflict) || !errors.Is(err, ErrNoBinding) {
		t.Fatalf("StartClaim without binding = %v, want ErrConflict+ErrNoBinding", err)
	}
	if len(f.notifier.sent) != 0 || f.claims.creates != 0 {
		t.Errorf("no chat message / no row expected, got %d sends, %d rows",
			len(f.notifier.sent), f.claims.creates)
	}
}

// Happy path: код уходит в чат группы (в топик форума, если он есть), строка
// пишется с message_id, expires_at = now + CLAIM_CODE_TTL.
func TestStartClaimHappyPath(t *testing.T) {
	f := newFixture(t, true)
	thread := int64(77)
	f.bindings.bindings[f.groupID].MessageThreadID = &thread

	res, err := f.svc.StartClaim(context.Background(), actor(2), f.groupID)
	if err != nil {
		t.Fatalf("StartClaim: %v", err)
	}
	wantExpires := f.clock.now.Add(10 * time.Minute)
	if !res.ExpiresAt.Equal(wantExpires) {
		t.Errorf("expires_at = %v, want %v", res.ExpiresAt, wantExpires)
	}
	if res.ChatID != -100500 {
		t.Errorf("chat_id = %d, want -100500", res.ChatID)
	}
	if len(f.notifier.sent) != 1 {
		t.Fatalf("chat sends = %d, want 1", len(f.notifier.sent))
	}
	if f.notifier.sent[0].chatID != -100500 || f.notifier.sent[0].threadID != 77 {
		t.Errorf("send = %+v, want chat -100500 thread 77", f.notifier.sent[0])
	}
	list, err := f.claims.ListActiveByGroup(context.Background(), f.groupID, f.clock.now)
	if err != nil {
		t.Fatalf("ListActiveByGroup: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("active codes = %d, want 1", len(list))
	}
	if list[0].MessageID == 0 || list[0].CodeHash == "" || list[0].ChatID != -100500 {
		t.Errorf("stored code = %+v", list[0])
	}
	if list[0].CreatedBy != 2 {
		t.Errorf("created_by = %d, want 2", list[0].CreatedBy)
	}
	// Хэш — SHA-256 от 6-значного кода: сам код в БД не лежит.
	if len(list[0].CodeHash) != 64 {
		t.Errorf("code_hash = %q, want 64 hex chars", list[0].CodeHash)
	}
}

// Порядок операций: сначала публикация в чат, затем запись строки (message_id
// неизвестен до отправки; упавшая отправка не должна оставлять «активный» код).
func TestStartClaimPostsBeforeInsert(t *testing.T) {
	f := newFixture(t, true)

	if _, err := f.svc.StartClaim(context.Background(), actor(2), f.groupID); err != nil {
		t.Fatalf("StartClaim: %v", err)
	}
	got := *f.events
	want := []string{"chat.send", "claim.insert"}
	if len(got) < 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("events = %v, want %v first", got, want)
	}
}

// Сбой отправки в чат: наружу ошибка, строки в БД нет (иначе «активный» код,
// которого никто не видел).
func TestStartClaimSendFailureLeavesNoRow(t *testing.T) {
	f := newFixture(t, true)
	f.notifier.chatErr = errors.New("bot was kicked")

	_, err := f.svc.StartClaim(context.Background(), actor(2), f.groupID)
	if !errors.Is(err, ErrCodeSendFailed) || !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("StartClaim with failing send = %v, want ErrConflict+ErrCodeSendFailed", err)
	}
	if f.claims.creates != 0 {
		t.Errorf("claim rows created = %d, want 0", f.claims.creates)
	}
}

// 4-й запрос кода в течение часа → RateLimitError. Cooldown обходится сдвигом
// минут (окно cooldown = 1 минута).
func TestStartClaimHourLimit(t *testing.T) {
	f := newFixture(t, true)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if _, err := f.svc.StartClaim(ctx, actor(2), f.groupID); err != nil {
			t.Fatalf("request %d: %v", i+1, err)
		}
		f.clock.advance(time.Minute)
	}
	_, err := f.svc.StartClaim(ctx, actor(2), f.groupID)
	rl := rateLimited(t, err)
	if rl.RetryAfter <= 0 || rl.RetryAfter > time.Hour {
		t.Errorf("retry_after = %v, want within (0, 1h]", rl.RetryAfter)
	}
	if f.claims.creates != 3 {
		t.Errorf("claim rows = %d, want 3 (4th rejected)", f.claims.creates)
	}
}

// Cooldown: второй запрос того же пользователя в пределах минуты → отказ.
func TestStartClaimCooldown(t *testing.T) {
	f := newFixture(t, true)
	ctx := context.Background()

	if _, err := f.svc.StartClaim(ctx, actor(2), f.groupID); err != nil {
		t.Fatalf("first StartClaim: %v", err)
	}
	f.clock.advance(30 * time.Second)
	_, err := f.svc.StartClaim(ctx, actor(2), f.groupID)
	rl := rateLimited(t, err)
	if rl.RetryAfter <= 0 || rl.RetryAfter > time.Minute {
		t.Errorf("retry_after = %v, want within (0, 1m]", rl.RetryAfter)
	}

	// Следующая минута — снова можно.
	f.clock.advance(30 * time.Second)
	if _, err := f.svc.StartClaim(ctx, actor(2), f.groupID); err != nil {
		t.Fatalf("StartClaim after cooldown: %v", err)
	}
}

// Второй StartClaim гасит предыдущий активный код: действует ровно один.
func TestStartClaimRevokesPreviousCode(t *testing.T) {
	f := newFixture(t, true)
	ctx := context.Background()

	if _, err := f.svc.StartClaim(ctx, actor(2), f.groupID); err != nil {
		t.Fatalf("first StartClaim: %v", err)
	}
	before, err := f.claims.GetActiveByGroup(ctx, f.groupID, f.clock.now)
	if err != nil {
		t.Fatalf("GetActiveByGroup: %v", err)
	}

	f.clock.advance(time.Minute)
	if _, err := f.svc.StartClaim(ctx, actor(2), f.groupID); err != nil {
		t.Fatalf("second StartClaim: %v", err)
	}
	active, err := f.claims.ListActiveByGroup(ctx, f.groupID, f.clock.now)
	if err != nil {
		t.Fatalf("ListActiveByGroup: %v", err)
	}
	if len(active) != 1 {
		t.Fatalf("active codes = %d, want 1 (previous revoked)", len(active))
	}
	if active[0].ID == before.ID {
		t.Errorf("active code id = %d, want the new one (old=%d)", active[0].ID, before.ID)
	}
	// Отозванный код непригоден для Confirm, даже если его plaintext известен.
	if _, err := f.svc.Confirm(ctx, actor(3), f.groupID, "000000"); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("Confirm(old code) = %v, want ErrForbidden (code revoked)", err)
	}
}

// Смена старосты (у группы есть админ): claim разрешён, админ получает ЛС.
func TestStartClaimNotifiesExistingAdmins(t *testing.T) {
	f := newFixture(t, true)
	ctx := context.Background()

	// Админ группы — user 1 (telegram 5000), запрашивает user 2.
	if err := f.members.SetRole(ctx, f.groupID, 1, domain.RoleAdmin); err != nil {
		t.Fatalf("set admin: %v", err)
	}

	if _, err := f.svc.StartClaim(ctx, actor(2), f.groupID); err != nil {
		t.Fatalf("StartClaim: %v", err)
	}
	if len(f.notifier.dms) != 1 || f.notifier.dms[0] != 5000 {
		t.Errorf("admin DMs = %v, want [5000]", f.notifier.dms)
	}
	// Сам запросивший уведомление не получает.
	for _, id := range f.notifier.dms {
		if id == actor(2).TelegramID {
			t.Errorf("requester notified himself: %v", f.notifier.dms)
		}
	}
	if len(f.audit.entries) != 1 || f.audit.entries[0].Action != "claim.start" {
		t.Errorf("audit = %+v, want single claim.start", f.audit.entries)
	}
}

// --- Confirm ---

// confirmFixture готовит активный код и возвращает его plaintext.
func confirmFixture(t *testing.T, f *fixture) string {
	t.Helper()
	code := "012345"
	now := f.clock.now
	if err := f.claims.Create(context.Background(), &domain.ClaimCode{
		GroupID: f.groupID, CodeHash: HashCode(code), ChatID: -100500, MessageID: 42,
		CreatedBy: 1, ExpiresAt: now.Add(10 * time.Minute),
	}); err != nil {
		t.Fatalf("seed claim code: %v", err)
	}
	return code
}

// Верный код: роль admin, группа active, код погашен, действующие админы
// уведомлены, вызывающий мог не быть участником до этого.
func TestConfirmSuccess(t *testing.T) {
	f := newFixture(t, true)
	ctx := context.Background()
	code := confirmFixture(t, f)
	// Действующий админ «до» — user 1.
	if err := f.members.SetRole(ctx, f.groupID, 1, domain.RoleAdmin); err != nil {
		t.Fatalf("set admin: %v", err)
	}

	g, err := f.svc.Confirm(ctx, actor(2), f.groupID, code)
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if g.Status != domain.GroupStatusActive {
		t.Errorf("group status = %q, want active", g.Status)
	}
	m, err := f.members.Get(ctx, f.groupID, 2)
	if err != nil {
		t.Fatalf("membership of claimer: %v", err)
	}
	if m.Role != domain.RoleAdmin {
		t.Errorf("role = %q, want admin", m.Role)
	}
	if _, err := f.claims.GetActiveByGroup(ctx, f.groupID, f.clock.now); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("GetActiveByGroup after Confirm = %v, want ErrNotFound", err)
	}
	// Действующий админ (5000) — уведомление о смене старосты; сам
	// подтвердивший (actor(2).TelegramID = 1002) — claim.success.
	if len(f.notifier.dms) != 2 || f.notifier.dms[0] != 5000 || f.notifier.dms[1] != actor(2).TelegramID {
		t.Errorf("DMs = %v, want [5000 %d]", f.notifier.dms, actor(2).TelegramID)
	}
	var confirmed bool
	for _, e := range f.audit.entries {
		if e.Action == "claim.confirm" {
			confirmed = true
		}
	}
	if !confirmed {
		t.Errorf("audit has no claim.confirm: %+v", f.audit.entries)
	}
}

// Роль member → admin: Upsert роль не меняет, поэтому Confirm обязан SetRole.
func TestConfirmPromotesExistingMember(t *testing.T) {
	f := newFixture(t, true)
	ctx := context.Background()
	code := confirmFixture(t, f)
	if err := f.members.Upsert(ctx, &domain.Membership{
		GroupID: f.groupID, UserID: 2, Role: domain.RoleMember,
	}); err != nil {
		t.Fatalf("seed member: %v", err)
	}

	if _, err := f.svc.Confirm(ctx, actor(2), f.groupID, code); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	m, _ := f.members.Get(ctx, f.groupID, 2)
	if m.Role != domain.RoleAdmin {
		t.Errorf("role = %q, want admin", m.Role)
	}
}

// Смена старосты в active-группе: статус остаётся active (не сбрасывается).
func TestConfirmKeepsActiveStatus(t *testing.T) {
	f := newFixture(t, true)
	ctx := context.Background()
	if err := f.groups.SetStatus(ctx, f.groupID, domain.GroupStatusActive); err != nil {
		t.Fatalf("activate: %v", err)
	}
	code := confirmFixture(t, f)

	g, err := f.svc.Confirm(ctx, actor(2), f.groupID, code)
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if g.Status != domain.GroupStatusActive {
		t.Errorf("status = %q, want active", g.Status)
	}
}

// Неверный код → ErrForbidden, код НЕ сгорает (пользователь может ошибиться
// и ввести правильный со второй попытки).
func TestConfirmWrongCode(t *testing.T) {
	f := newFixture(t, true)
	ctx := context.Background()
	confirmFixture(t, f)

	_, err := f.svc.Confirm(ctx, actor(2), f.groupID, "999999")
	if !errors.Is(err, domain.ErrForbidden) || !errors.Is(err, ErrWrongCode) {
		t.Fatalf("Confirm(wrong code) = %v, want ErrForbidden+ErrWrongCode", err)
	}
	if _, err := f.claims.GetActiveByGroup(ctx, f.groupID, f.clock.now); err != nil {
		t.Errorf("code should stay active after wrong attempt: %v", err)
	}
	if m, err := f.members.Get(ctx, f.groupID, 2); err == nil && m.Role != domain.RoleMember {
		t.Errorf("role changed on a wrong code: %q", m.Role)
	}
	// Третий пользователь (не участник) роль на неверном коде не получает.
	if _, err := f.members.Get(ctx, f.groupID, 3); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("non-member must not gain a membership on a wrong code")
	}
}

// Истёкший код неотличим от отсутствующего → ErrNotFound.
func TestConfirmExpiredCode(t *testing.T) {
	f := newFixture(t, true)
	ctx := context.Background()
	code := "654321"
	if err := f.claims.Create(ctx, &domain.ClaimCode{
		GroupID: f.groupID, CodeHash: HashCode(code), ChatID: -100500, MessageID: 1,
		CreatedBy: 1, ExpiresAt: f.clock.now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	f.clock.advance(2 * time.Minute)

	_, err := f.svc.Confirm(ctx, actor(2), f.groupID, code)
	if !errors.Is(err, domain.ErrNotFound) || !errors.Is(err, ErrCodeNotFound) {
		t.Fatalf("Confirm(expired) = %v, want ErrNotFound+ErrCodeNotFound", err)
	}
}

// Повторный Confirm уже использованного кода → ErrNotFound.
func TestConfirmUsedCode(t *testing.T) {
	f := newFixture(t, true)
	ctx := context.Background()
	code := confirmFixture(t, f)

	if _, err := f.svc.Confirm(ctx, actor(2), f.groupID, code); err != nil {
		t.Fatalf("first Confirm: %v", err)
	}
	_, err := f.svc.Confirm(ctx, actor(3), f.groupID, code)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("second Confirm = %v, want ErrNotFound", err)
	}
	// Роль второго вызывающего не появилась.
	if _, err := f.members.Get(ctx, f.groupID, 3); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("second claimer must not become a member")
	}
}

// Гонка двух Confirm: побеждает ровно один (условный MarkUsed в репо).
func TestConfirmRaceExactlyOneWins(t *testing.T) {
	f := newFixture(t, true)
	ctx := context.Background()
	code := confirmFixture(t, f)

	// Обе горутины читают один и тот же активный код (как при настоящей гонке),
	// затем соревнуются на MarkUsed: фейк повторяет условный UPDATE.
	type result struct{ err error }
	results := make(chan result, 2)
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		go func(userID int64) {
			<-start
			_, err := f.svc.Confirm(ctx, actor(userID), f.groupID, code)
			results <- result{err}
		}(int64(2 + i))
	}
	close(start)

	ok, notFound := 0, 0
	for i := 0; i < 2; i++ {
		r := <-results
		switch {
		case r.err == nil:
			ok++
		case errors.Is(r.err, domain.ErrNotFound):
			notFound++
		default:
			t.Fatalf("unexpected error: %v", r.err)
		}
	}
	if ok != 1 || notFound != 1 {
		t.Fatalf("race results: ok=%d notFound=%d, want exactly one winner", ok, notFound)
	}
}

// Отозванный (или ранее использованный) код не даёт роли: ErrNotFound.
func TestConfirmAfterRevoke(t *testing.T) {
	f := newFixture(t, true)
	ctx := context.Background()
	code := confirmFixture(t, f)

	if err := f.claims.RevokeActiveByGroup(ctx, f.groupID, f.clock.now); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	_, err := f.svc.Confirm(ctx, actor(2), f.groupID, code)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Confirm after revoke = %v, want ErrNotFound", err)
	}
}

// --- Revoke ---

// Только админ группы (или superadmin) может отозвать код.
func TestRevokeRequiresAdmin(t *testing.T) {
	f := newFixture(t, true)
	ctx := context.Background()

	// Создатель группы — member (спека §3.1), не админ: отказ.
	if err := f.claims.RevokeActiveByGroup(ctx, f.groupID, f.clock.now); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	confirmFixture(t, f)
	err := f.svc.Revoke(ctx, actor(1), f.groupID)
	if !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("Revoke(member) = %v, want ErrForbidden", err)
	}
	if _, err := f.claims.GetActiveByGroup(ctx, f.groupID, f.clock.now); err != nil {
		t.Errorf("code must survive a forbidden revoke: %v", err)
	}

	// Админ — успех, код гаснет, аудит записан.
	if err := f.members.SetRole(ctx, f.groupID, 1, domain.RoleAdmin); err != nil {
		t.Fatalf("set admin: %v", err)
	}
	if err := f.svc.Revoke(ctx, actor(1), f.groupID); err != nil {
		t.Fatalf("Revoke(admin): %v", err)
	}
	if _, err := f.claims.GetActiveByGroup(ctx, f.groupID, f.clock.now); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("code still active after revoke: %v", err)
	}
}

// Нет активного кода → ErrNotFound (отзывать нечего).
func TestRevokeWithoutActiveCode(t *testing.T) {
	f := newFixture(t, true)
	ctx := context.Background()
	if err := f.members.SetRole(ctx, f.groupID, 1, domain.RoleAdmin); err != nil {
		t.Fatalf("set admin: %v", err)
	}
	if err := f.svc.Revoke(ctx, actor(1), f.groupID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Revoke without active code = %v, want ErrNotFound", err)
	}
}

// Superadmin может отозвать код чужой группы.
func TestRevokeBySuperadmin(t *testing.T) {
	f := newFixture(t, true)
	confirmFixture(t, f)

	su := actor(99)
	su.IsSuperadmin = true
	if err := f.svc.Revoke(context.Background(), su, f.groupID); err != nil {
		t.Fatalf("Revoke(superadmin): %v", err)
	}
}

// --- helpers ---

func TestHashCodeStable(t *testing.T) {
	// Стабильность и различимость: 64 hex-символа SHA-256, разные коды — разные
	// хэши (в БД лежит только хэш, спека §4).
	if len(HashCode("000000")) != 64 {
		t.Errorf("hash length = %d, want 64", len(HashCode("000000")))
	}
	if HashCode("000000") != HashCode("000000") {
		t.Errorf("hash is not deterministic")
	}
	if HashCode("000000") == HashCode("000001") {
		t.Errorf("distinct codes must hash differently")
	}
}

// GenerateCode: всегда 6 цифр, ведущие нули сохраняются.
func TestGenerateCodeFormat(t *testing.T) {
	for i := 0; i < 200; i++ {
		code, err := GenerateCode()
		if err != nil {
			t.Fatalf("GenerateCode: %v", err)
		}
		if len(code) != 6 {
			t.Fatalf("code = %q, want 6 digits", code)
		}
		for _, r := range code {
			if r < '0' || r > '9' {
				t.Fatalf("code = %q contains non-digit", code)
			}
		}
	}
}
