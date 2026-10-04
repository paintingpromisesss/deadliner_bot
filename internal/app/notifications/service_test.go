package notifications

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/sauron/deadliner/internal/domain"
)

// --- fakes ---

type fakeUserRepo struct {
	users   map[int64]*domain.User
	updates []userUpdate
}

type userUpdate struct {
	ID       int64
	TZ       string
	DMNotify bool
}

func newFakeUsers(users ...*domain.User) *fakeUserRepo {
	r := &fakeUserRepo{users: map[int64]*domain.User{}}
	for _, u := range users {
		r.users[u.ID] = u
	}
	return r
}

func (r *fakeUserRepo) GetByTelegramID(ctx context.Context, telegramID int64) (*domain.User, error) {
	for _, u := range r.users {
		if u.TelegramID == telegramID {
			cp := *u
			return &cp, nil
		}
	}
	return nil, fmt.Errorf("%w: user telegram_id=%d", domain.ErrNotFound, telegramID)
}

func (r *fakeUserRepo) GetByID(ctx context.Context, id int64) (*domain.User, error) {
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
	u, ok := r.users[id]
	if !ok {
		return fmt.Errorf("%w: user id=%d", domain.ErrNotFound, id)
	}
	u.TZ, u.DMNotifyDefault = tz, dmNotifyDefault
	r.updates = append(r.updates, userUpdate{ID: id, TZ: tz, DMNotify: dmNotifyDefault})
	return nil
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

// ListSuperadmins и UpdateProfile — части domain.UserRepo, не используемые
// сервисом уведомлений: заглушки-нули.
func (r *fakeUserRepo) ListSuperadmins(ctx context.Context) ([]domain.User, error) {
	return nil, nil
}
func (r *fakeUserRepo) UpdateProfile(ctx context.Context, id int64, firstName string) error {
	return nil
}

type memKey struct{ groupID, userID int64 }

type fakeMembershipRepo struct {
	mems     map[memKey]*domain.Membership
	setCalls []dmNotifySet
}

type dmNotifySet struct {
	GroupID  int64
	UserID   int64
	DMNotify *bool
}

func newFakeMemberships() *fakeMembershipRepo {
	return &fakeMembershipRepo{mems: map[memKey]*domain.Membership{}}
}

func (r *fakeMembershipRepo) add(groupID, userID int64, dmNotify *bool) {
	r.mems[memKey{groupID, userID}] = &domain.Membership{
		GroupID: groupID, UserID: userID, Role: domain.RoleMember, DMNotify: dmNotify,
	}
}

func (r *fakeMembershipRepo) Upsert(ctx context.Context, m *domain.Membership) error {
	return errors.New("not used")
}

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
	return errors.New("not used")
}

func (r *fakeMembershipRepo) DemoteIfNotLastAdmin(ctx context.Context, groupID, userID int64) error {
	return errors.New("not used")
}

func (r *fakeMembershipRepo) RemoveIfNotLastAdmin(ctx context.Context, groupID, userID int64) error {
	return errors.New("not used")
}

func (r *fakeMembershipRepo) SetDMNotify(ctx context.Context, groupID, userID int64, dmNotify *bool) error {
	m, ok := r.mems[memKey{groupID, userID}]
	if !ok {
		return fmt.Errorf("%w: membership group=%d user=%d", domain.ErrNotFound, groupID, userID)
	}
	m.DMNotify = dmNotify
	r.setCalls = append(r.setCalls, dmNotifySet{GroupID: groupID, UserID: userID, DMNotify: dmNotify})
	return nil
}

func (r *fakeMembershipRepo) ListDMTargets(ctx context.Context, groupID int64) ([]int64, error) {
	return nil, errors.New("not used")
}

func (r *fakeMembershipRepo) Delete(ctx context.Context, groupID, userID int64) error {
	return errors.New("not used")
}

func (r *fakeMembershipRepo) CountAdmins(ctx context.Context, groupID int64) (int, error) {
	return 0, errors.New("not used")
}

type fakeGroupRepo struct {
	groups map[int64]*domain.Group
	// deleted — soft-deleted группы: реальный репозиторий фильтрует
	// `deleted_at IS NULL`, поэтому GetByID и ListMine их не видят.
	deleted map[int64]bool
	// mine — состав групп участника: реальный ListMine делает JOIN
	// group_memberships, поэтому фейк не отдаёт справочник целиком.
	mine []int64
}

func newFakeGroups(groups ...*domain.Group) *fakeGroupRepo {
	r := &fakeGroupRepo{groups: map[int64]*domain.Group{}, deleted: map[int64]bool{}}
	for _, g := range groups {
		r.groups[g.ID] = g
	}
	return r
}

// softDelete помечает группу удалённой (строка в справочнике остаётся).
func (r *fakeGroupRepo) softDelete(groupID int64) { r.deleted[groupID] = true }

func (r *fakeGroupRepo) Create(ctx context.Context, g *domain.Group) error {
	return errors.New("not used")
}

func (r *fakeGroupRepo) GetByID(ctx context.Context, id int64) (*domain.Group, error) {
	g, ok := r.groups[id]
	if !ok || r.deleted[id] {
		return nil, fmt.Errorf("%w: group id=%d", domain.ErrNotFound, id)
	}
	cp := *g
	return &cp, nil
}

func (r *fakeGroupRepo) GetBySlugNorm(ctx context.Context, slugNorm string) (*domain.Group, error) {
	return nil, fmt.Errorf("%w: group slug_norm=%q", domain.ErrNotFound, slugNorm)
}

func (r *fakeGroupRepo) SearchByPrefix(ctx context.Context, prefix string, callerID int64, limit int) ([]domain.Group, error) {
	return nil, errors.New("not used")
}

func (r *fakeGroupRepo) Update(ctx context.Context, g *domain.Group) error {
	return errors.New("not used")
}

func (r *fakeGroupRepo) SetStatus(ctx context.Context, id int64, status domain.GroupStatus) error {
	return errors.New("not used")
}

func (r *fakeGroupRepo) SoftDelete(ctx context.Context, id int64) error {
	return errors.New("not used")
}

func (r *fakeGroupRepo) HardDelete(ctx context.Context, id int64) error {
	return errors.New("not used")
}

func (r *fakeGroupRepo) ListMine(ctx context.Context, userID int64) ([]domain.Group, error) {
	out := make([]domain.Group, 0, len(r.mine))
	for _, id := range r.mine {
		if g, ok := r.groups[id]; ok && !r.deleted[id] {
			out = append(out, *g)
		}
	}
	return out, nil
}

func (r *fakeGroupRepo) ListPendingExpired(ctx context.Context, now time.Time, limit int) ([]domain.Group, error) {
	return nil, errors.New("not used")
}

// ListAll — часть domain.GroupRepo, нужная только CLI `admin list-groups`;
// сервисам этих пакетов не требуется.
func (r *fakeGroupRepo) ListAll(ctx context.Context, status *domain.GroupStatus, limit int) ([]domain.Group, error) {
	return nil, nil
}

// --- harness ---

func boolPtr(v bool) *bool { return &v }

// newHarness собирает сервис на фейках: пользователь 5 (telegram 500) —
// участник группы 1 с явным override и группы 2 без override.
type svcHarness struct {
	svc    *Service
	actor  *domain.User
	users  *fakeUserRepo
	mems   *fakeMembershipRepo
	groups *fakeGroupRepo
}

func newSvcHarness(t *testing.T, dmDefault bool) *svcHarness {
	t.Helper()
	actor := &domain.User{ID: 5, TelegramID: 500, TZ: "Europe/Moscow", DMNotifyDefault: dmDefault}
	users := newFakeUsers(actor)
	mems := newFakeMemberships()
	mems.add(1, 5, nil)
	mems.add(2, 5, nil)
	groups := newFakeGroups(
		&domain.Group{ID: 1, Slug: "ИКБО-33-21", Title: "Первая"},
		&domain.Group{ID: 2, Slug: "М8О-401Б-23", Title: "Вторая"},
		&domain.Group{ID: 3, Slug: "ПМИ-11-2", Title: "Чужая"},
	)
	groups.mine = []int64{1, 2}
	return &svcHarness{
		svc: NewService(users, mems, groups), actor: actor,
		users: users, mems: mems, groups: groups,
	}
}

// --- GET: матрица эффективных значений ---

// Спека §9 (экран 6): эффективное значение = COALESCE(membership.dm_notify,
// users.dm_notify_default), override = membership.dm_notify IS NOT NULL.
func TestGetEffectiveMatrix(t *testing.T) {
	cases := []struct {
		name        string
		dmDefault   bool
		override    *bool
		wantNotify  bool
		wantOverrid bool
	}{
		{"default false, no override", false, nil, false, false},
		{"default false, override true", false, boolPtr(true), true, true},
		{"default true, override false", true, boolPtr(false), false, true},
		{"default true, null override inherits", true, nil, true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hs := newSvcHarness(t, c.dmDefault)
			hs.mems.mems[memKey{1, 5}].DMNotify = c.override

			got, err := hs.svc.Get(context.Background(), hs.actor, nil)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if got.DMNotifyDefault != c.dmDefault {
				t.Errorf("dm_notify_default = %v, want %v", got.DMNotifyDefault, c.dmDefault)
			}
			if len(got.Groups) != 2 {
				t.Fatalf("groups = %+v, want 2 (only the actor's memberships)", got.Groups)
			}
			g1 := got.Groups[0]
			if g1.GroupID != 1 {
				t.Fatalf("first group = %d, want 1", g1.GroupID)
			}
			if g1.DMNotify != c.wantNotify {
				t.Errorf("group dm_notify = %v, want %v", g1.DMNotify, c.wantNotify)
			}
			if g1.Override != c.wantOverrid {
				t.Errorf("group override = %v, want %v", g1.Override, c.wantOverrid)
			}
			if g1.Slug == "" || g1.Title == "" {
				t.Errorf("group %+v lacks slug/title", g1)
			}
			// Группа 3 — не своя: в списке её быть не должно.
			for _, g := range got.Groups {
				if g.GroupID == 3 {
					t.Errorf("non-member group leaked into the list: %+v", g)
				}
			}
		})
	}
}

// Пустой список групп — не nil: клиент ждёт массив, а не null.
func TestGetWithoutGroups(t *testing.T) {
	hs := newSvcHarness(t, false)
	hs.mems.mems = map[memKey]*domain.Membership{}
	hs.groups.mine = nil

	got, err := hs.svc.Get(context.Background(), hs.actor, nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Groups == nil || len(got.Groups) != 0 {
		t.Errorf("groups = %#v, want an empty non-nil slice", got.Groups)
	}
}

// group_id задан → ровно одна группа.
func TestGetSingleGroup(t *testing.T) {
	hs := newSvcHarness(t, true)
	hs.mems.mems[memKey{2, 5}].DMNotify = boolPtr(false)

	gid := int64(2)
	got, err := hs.svc.Get(context.Background(), hs.actor, &gid)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.Groups) != 1 || got.Groups[0].GroupID != 2 {
		t.Fatalf("groups = %+v, want exactly group 2", got.Groups)
	}
	if got.Groups[0].DMNotify || !got.Groups[0].Override {
		t.Errorf("group = %+v, want dm_notify=false override=true", got.Groups[0])
	}
	if got.Groups[0].Slug != "М8О-401Б-23" {
		t.Errorf("slug = %q", got.Groups[0].Slug)
	}
}

// Не участник → ErrNotFound (не 403: существование чужих групп не раскрываем).
func TestGetSingleGroupNotMember(t *testing.T) {
	hs := newSvcHarness(t, false)
	gid := int64(3)

	_, err := hs.svc.Get(context.Background(), hs.actor, &gid)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestGetNilActorForbidden(t *testing.T) {
	hs := newSvcHarness(t, false)
	if _, err := hs.svc.Get(context.Background(), nil, nil); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
}

// --- PATCH ---

// Без group_id меняется global-дефолт; tz актора сохраняется (UpdateSettings
// пишет обе колонки одним UPDATE).
func TestUpdateDefaultPersists(t *testing.T) {
	hs := newSvcHarness(t, false)

	got, err := hs.svc.Update(context.Background(), hs.actor, UpdateInput{
		DMNotify: boolPtr(true), HasDMNotify: true,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(hs.users.updates) != 1 {
		t.Fatalf("updates = %+v, want one", hs.users.updates)
	}
	if u := hs.users.updates[0]; u.ID != 5 || u.TZ != "Europe/Moscow" || !u.DMNotify {
		t.Errorf("update = %+v, want {5 Europe/Moscow true}", u)
	}
	if !got.DMNotifyDefault {
		t.Errorf("dm_notify_default = %v, want true in the response", got.DMNotifyDefault)
	}
	if hs.users.users[5].DMNotifyDefault != true {
		t.Errorf("stored default = %v, want true", hs.users.users[5].DMNotifyDefault)
	}
	// Дефолт меняет эффективное значение групп без override.
	if len(got.Groups) != 2 || !got.Groups[0].DMNotify || got.Groups[0].Override {
		t.Errorf("groups = %+v, want both effective values flipped to true", got.Groups)
	}
}

// С group_id задаётся явный override.
func TestUpdateGroupOverrideSets(t *testing.T) {
	hs := newSvcHarness(t, false)
	gid := int64(1)

	got, err := hs.svc.Update(context.Background(), hs.actor, UpdateInput{
		GroupID: &gid, DMNotify: boolPtr(true), HasDMNotify: true,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(hs.mems.setCalls) != 1 {
		t.Fatalf("set calls = %+v, want one", hs.mems.setCalls)
	}
	if c := hs.mems.setCalls[0]; c.GroupID != 1 || c.UserID != 5 || c.DMNotify == nil || !*c.DMNotify {
		t.Errorf("set call = %+v, want group 1 user 5 true", c)
	}
	if g := got.Groups[0]; len(got.Groups) != 2 || g.GroupID != 1 || !g.DMNotify || !g.Override {
		t.Errorf("groups = %+v, want group 1 with dm_notify=true override=true", got.Groups)
	}
	// Дефолт не тронут.
	if len(hs.users.updates) != 0 {
		t.Errorf("users updated = %+v, want none", hs.users.updates)
	}
}

// PATCH возвращает те же настройки, что и GET: клиенту нужен актуальный
// effective-набор всех групп (экран настроек целиком), а не только патч.
func TestUpdateReturnsFullSettings(t *testing.T) {
	hs := newSvcHarness(t, true)
	gid := int64(1)
	hs.mems.mems[memKey{1, 5}].DMNotify = boolPtr(false)

	got, err := hs.svc.Update(context.Background(), hs.actor, UpdateInput{
		GroupID: &gid, DMNotify: nil, HasDMNotify: true,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(got.Groups) != 2 {
		t.Fatalf("groups = %+v, want both of the actor's groups", got.Groups)
	}
	if g := got.Groups[0]; g.GroupID != 1 || !g.DMNotify || g.Override {
		t.Errorf("patched group = %+v, want inherited dm_notify=true override=false", g)
	}
	if g := got.Groups[1]; g.GroupID != 2 || !g.DMNotify || g.Override {
		t.Errorf("untouched group = %+v, want inherited true", g)
	}
}

// dm_notify: null с group_id снимает override (наследование дефолта).
func TestUpdateGroupOverrideClears(t *testing.T) {
	hs := newSvcHarness(t, true)
	hs.mems.mems[memKey{1, 5}].DMNotify = boolPtr(false)
	gid := int64(1)

	got, err := hs.svc.Update(context.Background(), hs.actor, UpdateInput{
		GroupID: &gid, DMNotify: nil, HasDMNotify: true,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(hs.mems.setCalls) != 1 || hs.mems.setCalls[0].DMNotify != nil {
		t.Fatalf("set calls = %+v, want one with nil", hs.mems.setCalls)
	}
	if hs.mems.mems[memKey{1, 5}].DMNotify != nil {
		t.Errorf("stored override = %v, want nil", hs.mems.mems[memKey{1, 5}].DMNotify)
	}
	// Эффективное значение вернулось к дефолту, override снят.
	if len(got.Groups) != 2 || !got.Groups[0].DMNotify || got.Groups[0].Override {
		t.Errorf("groups = %+v, want inherited dm_notify=true override=false", got.Groups)
	}
}

// Не участник → ErrNotFound, членство не пишется.
func TestUpdateGroupNotMember(t *testing.T) {
	hs := newSvcHarness(t, false)
	gid := int64(3)

	_, err := hs.svc.Update(context.Background(), hs.actor, UpdateInput{
		GroupID: &gid, DMNotify: boolPtr(true), HasDMNotify: true,
	})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if len(hs.mems.setCalls) != 0 {
		t.Errorf("set calls = %+v, want none", hs.mems.setCalls)
	}
}

// Soft-deleted группа с уцелевшим членством: PATCH обязан отвечать так же, как
// GET ?group_id= (ErrNotFound), а не молча писать в невидимую группу.
func TestUpdateSoftDeletedGroup(t *testing.T) {
	hs := newSvcHarness(t, false)
	hs.groups.softDelete(1)
	gid := int64(1)

	// GET уже отдаёт ErrNotFound для этой группы.
	if _, err := hs.svc.Get(context.Background(), hs.actor, &gid); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Get err = %v, want ErrNotFound", err)
	}

	_, err := hs.svc.Update(context.Background(), hs.actor, UpdateInput{
		GroupID: &gid, DMNotify: boolPtr(true), HasDMNotify: true,
	})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Update err = %v, want ErrNotFound", err)
	}
	if len(hs.mems.setCalls) != 0 {
		t.Errorf("set calls = %+v, want none for a soft-deleted group", hs.mems.setCalls)
	}
	if hs.mems.mems[memKey{1, 5}].DMNotify != nil {
		t.Errorf("override = %v, want untouched for a soft-deleted group",
			hs.mems.mems[memKey{1, 5}].DMNotify)
	}
}

// Удалённая группа исчезает и из списка GET.
func TestGetSkipsSoftDeletedGroups(t *testing.T) {
	hs := newSvcHarness(t, false)
	hs.groups.softDelete(1)

	got, err := hs.svc.Get(context.Background(), hs.actor, nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.Groups) != 1 || got.Groups[0].GroupID != 2 {
		t.Errorf("groups = %+v, want only the live group 2", got.Groups)
	}
}

// Оба поля отсутствуют → ErrValidation (пустой патч).
func TestUpdateBothFieldsAbsent(t *testing.T) {
	hs := newSvcHarness(t, false)

	_, err := hs.svc.Update(context.Background(), hs.actor, UpdateInput{})
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("err = %v, want ErrValidation", err)
	}
	if len(hs.users.updates) != 0 || len(hs.mems.setCalls) != 0 {
		t.Errorf("no write must happen on an empty patch")
	}
}

// group_id присутствует, dm_notify — нет: патчить нечего.
func TestUpdateGroupWithoutDMNotify(t *testing.T) {
	hs := newSvcHarness(t, false)
	gid := int64(1)

	_, err := hs.svc.Update(context.Background(), hs.actor, UpdateInput{GroupID: &gid})
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("err = %v, want ErrValidation", err)
	}
}

// dm_notify: null без group_id недопустим: users.dm_notify_default NOT NULL.
func TestUpdateDefaultNullRejected(t *testing.T) {
	hs := newSvcHarness(t, false)

	_, err := hs.svc.Update(context.Background(), hs.actor, UpdateInput{DMNotify: nil, HasDMNotify: true})
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("err = %v, want ErrValidation", err)
	}
	if len(hs.users.updates) != 0 {
		t.Errorf("users updated = %+v, want none", hs.users.updates)
	}
}
