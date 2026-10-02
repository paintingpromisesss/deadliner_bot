package groups

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/sauron/deadliner/internal/domain"
	"github.com/sauron/deadliner/internal/i18n"
)

// --- fakes ---

type fakeSuperadminRepo struct {
	admins []domain.User
	err    error
}

func (r *fakeSuperadminRepo) GetByTelegramID(ctx context.Context, telegramID int64) (*domain.User, error) {
	return nil, domain.ErrNotFound
}
func (r *fakeSuperadminRepo) GetByID(ctx context.Context, id int64) (*domain.User, error) {
	return nil, domain.ErrNotFound
}
func (r *fakeSuperadminRepo) UpsertByTelegram(ctx context.Context, u *domain.User) error { return nil }
func (r *fakeSuperadminRepo) UpdateSettings(ctx context.Context, id int64, tz string, dm bool) error {
	return nil
}
func (r *fakeSuperadminRepo) UpdateProfile(ctx context.Context, id int64, firstName string) error {
	return nil
}
func (r *fakeSuperadminRepo) SetBanned(ctx context.Context, id int64, banned bool) error { return nil }
func (r *fakeSuperadminRepo) SetSuperadmin(ctx context.Context, id int64, sa bool) error { return nil }
func (r *fakeSuperadminRepo) MarkBotBlocked(ctx context.Context, telegramID int64, blocked bool) error {
	return nil
}
func (r *fakeSuperadminRepo) ListSuperadmins(ctx context.Context) ([]domain.User, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.admins, nil
}

// fakeNotifier — журнал ЛС-отправок жалобы.
type fakeNotifier struct {
	sent  []sentDM
	fail  map[int64]error
	calls int
}

type sentDM struct {
	userID int64
	text   string
}

func (n *fakeNotifier) SendToUser(ctx context.Context, userID int64, text string) error {
	n.calls++
	if err := n.fail[userID]; err != nil {
		return err
	}
	n.sent = append(n.sent, sentDM{userID: userID, text: text})
	return nil
}

// --- harness ---

// newReportFixture — фикстура сервиса с подключёнными жалобами: реальный
// нотификатор-фейк, список супер-админов и (для полноты картины) local-провайдер
// слага на дефолтной регулярке.
type reportFixture struct {
	*fixture
	admins   *fakeSuperadminRepo
	notifier *fakeNotifier
}

func newReportFixture(admins []domain.User) *reportFixture {
	f := newFixture(Config{})
	repo := &fakeSuperadminRepo{admins: admins}
	nfy := &fakeNotifier{fail: map[int64]error{}}
	f.svc.WithOptions(Options{Notifier: nfy, Users: repo})
	return &reportFixture{fixture: f, admins: repo, notifier: nfy}
}

// actor с заполненным профилем: имя подставляется в текст жалобы.
func namedUser(id int64, firstName string) *domain.User {
	u := user(id, false)
	u.FirstName = firstName
	u.TelegramID = 1000 + id
	return u
}

func superadmin(id, telegramID int64) domain.User {
	return domain.User{ID: id, TelegramID: telegramID, IsSuperadmin: true, FirstName: "Root"}
}

// --- ReportSlug ---

// Админ группы шлёт жалобу — ЛС уходит ВСЕМ супер-админам, слаг и отправитель
// попадают в текст, аудит пишется (спека §3.3).
func TestReportSlug_NotifiesEverySuperadmin(t *testing.T) {
	if err := i18n.Load(i18n.Locales); err != nil {
		t.Fatalf("i18n: %v", err)
	}
	f := newReportFixture([]domain.User{superadmin(10, 10010), superadmin(11, 10011)})
	ctx := context.Background()

	g, err := f.svc.Create(ctx, namedUser(1, "Иван"), "ИКБО-33-21", "Моя группа")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.members.SetRole(ctx, g.ID, 1, domain.RoleAdmin); err != nil {
		t.Fatal(err)
	}

	got, err := f.svc.ReportSlug(ctx, namedUser(1, "Иван"), " икбо-33-21 ")
	if err != nil {
		t.Fatalf("ReportSlug: %v", err)
	}
	if got.ID != g.ID {
		t.Errorf("report target = %d, want group %d", got.ID, g.ID)
	}

	if len(f.notifier.sent) != 2 {
		t.Fatalf("delivered = %d, want 2 (one DM per superadmin): %+v", len(f.notifier.sent), f.notifier.sent)
	}
	want := map[int64]bool{10010: true, 10011: true}
	for _, dm := range f.notifier.sent {
		if !want[dm.userID] {
			t.Errorf("unexpected recipient %d", dm.userID)
		}
		delete(want, dm.userID)
		if !contains(dm.text, "ИКБО-33-21") {
			t.Errorf("text %q does not mention the slug", dm.text)
		}
		if !contains(dm.text, "Иван") {
			t.Errorf("text %q does not mention the reporter", dm.text)
		}
	}
	if len(want) != 0 {
		t.Errorf("superadmins without a DM: %v", want)
	}

	// Аудит: spam-жалоба обязана оставлять след (иначе злоупотребления не
	// восстановить по логам).
	var found bool
	for _, e := range f.audit.entries {
		if e.Action == "slug.report" && e.TargetID != nil && *e.TargetID == g.ID {
			found = true
			if e.ActorUserID == nil || *e.ActorUserID != 1 {
				t.Errorf("audit actor = %v, want the reporter", e.ActorUserID)
			}
		}
	}
	if !found {
		t.Error("no slug.report audit entry was written")
	}
}

// Точный порядок подстановок в шаблоне: слаг / id группы / отправитель.
// Пиннится ВСЯ отрендеренная строка целиком, а не наличие подстрок: именно
// проверка «в тексте есть слаг и есть имя» пропустила перестановку аргументов,
// из-за которой супер-админы читали «Группа: id=Иван / Отправитель: 1».
func TestReportSlug_RendersTemplateArgsInOrder(t *testing.T) {
	if err := i18n.Load(i18n.Locales); err != nil {
		t.Fatalf("i18n: %v", err)
	}
	f := newReportFixture([]domain.User{superadmin(10, 10010)})
	ctx := context.Background()

	g, err := f.svc.Create(ctx, namedUser(1, "Иван"), "ИКБО-33-21", "Моя группа")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.members.SetRole(ctx, g.ID, 1, domain.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if g.ID != 1 {
		t.Fatalf("fixture precondition: group id = %d, want 1 (ожидаемый текст ниже завязан на него)", g.ID)
	}

	if _, err := f.svc.ReportSlug(ctx, namedUser(1, "Иван"), "ИКБО-33-21"); err != nil {
		t.Fatalf("ReportSlug: %v", err)
	}
	if len(f.notifier.sent) != 1 {
		t.Fatalf("delivered = %d, want 1", len(f.notifier.sent))
	}

	want := "⚠️ Жалоба на слаг ИКБО-33-21\n" +
		"Группа: id=1\n" +
		"Отправитель: Иван\n" +
		"\n" +
		"Разрешение конфликта — вручную: удалите лишнюю группу " +
		"(/delete_group «слаг») или передайте админство. " +
		"Слаг освобождается только удалением группы."
	if got := f.notifier.sent[0].text; got != want {
		t.Errorf("slug report text mismatch\n got: %q\nwant: %q", got, want)
	}
}

// Не-админ группы жаловаться не может: супер-админам не уходит ничего.
func TestReportSlug_NonAdminIsForbidden(t *testing.T) {
	f := newReportFixture([]domain.User{superadmin(10, 10010)})
	ctx := context.Background()

	g, err := f.svc.Create(ctx, namedUser(1, "Иван"), "ИКБО-33-21", "Моя группа")
	if err != nil {
		t.Fatal(err)
	}
	// Второй участник — member.
	if err := f.members.Upsert(ctx, &domain.Membership{
		GroupID: g.ID, UserID: 2, Role: domain.RoleMember,
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := f.svc.ReportSlug(ctx, namedUser(2, "Пётр"), "ИКБО-33-21"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("ReportSlug(member) = %v, want ErrForbidden", err)
	}
	if f.notifier.calls != 0 {
		t.Errorf("notifier calls = %d, want 0 for a non-admin", f.notifier.calls)
	}
}

// Незнакомый слаг — ErrNotFound и молчание: ответ не должен раскрывать
// существование слага.
func TestReportSlug_UnknownSlugIsNotFound(t *testing.T) {
	f := newReportFixture([]domain.User{superadmin(10, 10010)})
	ctx := context.Background()

	_, err := f.svc.ReportSlug(ctx, namedUser(1, "Иван"), "НЕТ-ТАК-1")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("ReportSlug(unknown) = %v, want ErrNotFound", err)
	}
	if f.notifier.calls != 0 {
		t.Errorf("notifier calls = %d, want 0", f.notifier.calls)
	}
}

// Pending-группу тоже можно защищать жалобой: конфликт слага возникает ДО
// активации, и админ pending-группы — законный заявитель.
func TestReportSlug_WorksForPendingGroup(t *testing.T) {
	f := newReportFixture([]domain.User{superadmin(10, 10010)})
	ctx := context.Background()

	g, err := f.svc.Create(ctx, namedUser(1, "Иван"), "ИКБО-33-21", "Pending")
	if err != nil {
		t.Fatal(err)
	}
	if g.Status != domain.GroupStatusPending {
		t.Fatalf("fixture precondition: status = %s, want pending", g.Status)
	}
	if err := f.members.SetRole(ctx, g.ID, 1, domain.RoleAdmin); err != nil {
		t.Fatal(err)
	}

	if _, err := f.svc.ReportSlug(ctx, namedUser(1, "Иван"), "ИКБО-33-21"); err != nil {
		t.Fatalf("ReportSlug(pending) = %v, want nil", err)
	}
	if len(f.notifier.sent) != 1 {
		t.Errorf("delivered = %d, want 1", len(f.notifier.sent))
	}
}

// Сбой ЛС одному супер-админу не отменяет остальных (best-effort, §7.3):
// жалоба «дошла» всем, кому смогла, и это видно в счётчике аудита.
func TestReportSlug_DeliveryFailureDoesNotStopFanout(t *testing.T) {
	f := newReportFixture([]domain.User{superadmin(10, 10010), superadmin(11, 10011)})
	ctx := context.Background()
	f.notifier.fail[10010] = fmt.Errorf("%w: blocked", domain.ErrBotBlocked)

	g, err := f.svc.Create(ctx, namedUser(1, "Иван"), "ИКБО-33-21", "T")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.members.SetRole(ctx, g.ID, 1, domain.RoleAdmin); err != nil {
		t.Fatal(err)
	}

	if _, err := f.svc.ReportSlug(ctx, namedUser(1, "Иван"), "ИКБО-33-21"); err != nil {
		t.Fatalf("ReportSlug: %v (delivery failures must not fail the use case)", err)
	}
	if len(f.notifier.sent) != 1 || f.notifier.sent[0].userID != 10011 {
		t.Errorf("delivered to %+v, want exactly the reachable superadmin 10011", f.notifier.sent)
	}
}

// bot_blocked у супер-админа: отправка не тратится вовсе (§7.3).
func TestReportSlug_SkipsBlockedSuperadmin(t *testing.T) {
	blocked := superadmin(10, 10010)
	blocked.BotBlocked = true
	f := newReportFixture([]domain.User{blocked, superadmin(11, 10011)})
	ctx := context.Background()

	g, err := f.svc.Create(ctx, namedUser(1, "Иван"), "ИКБО-33-21", "T")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.members.SetRole(ctx, g.ID, 1, domain.RoleAdmin); err != nil {
		t.Fatal(err)
	}

	if _, err := f.svc.ReportSlug(ctx, namedUser(1, "Иван"), "ИКБО-33-21"); err != nil {
		t.Fatalf("ReportSlug: %v", err)
	}
	if f.notifier.calls != 1 {
		t.Errorf("send attempts = %d, want 1 (the blocked superadmin is skipped)", f.notifier.calls)
	}
	if len(f.notifier.sent) != 1 || f.notifier.sent[0].userID != 10011 {
		t.Errorf("delivered to %+v, want only 10011", f.notifier.sent)
	}
}

// Без подключённого транспорта жалоба недоступна: не паникуем, отдаём отказ.
func TestReportSlug_WithoutWiringIsForbidden(t *testing.T) {
	f := newFixture(Config{})
	ctx := context.Background()

	if _, err := f.svc.Create(ctx, namedUser(1, "Иван"), "ИКБО-33-21", "T"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.ReportSlug(ctx, namedUser(1, "Иван"), "ИКБО-33-21"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("ReportSlug(unwired) = %v, want ErrForbidden", err)
	}
}

// Сбой чтения адресатов — ошибка use case (не молчаливый «ноль отправок»).
func TestReportSlug_SuperadminLookupFails(t *testing.T) {
	f := newReportFixture([]domain.User{superadmin(10, 10010)})
	ctx := context.Background()
	f.admins.err = fmt.Errorf("db down")

	g, err := f.svc.Create(ctx, namedUser(1, "Иван"), "ИКБО-33-21", "T")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.members.SetRole(ctx, g.ID, 1, domain.RoleAdmin); err != nil {
		t.Fatal(err)
	}

	if _, err := f.svc.ReportSlug(ctx, namedUser(1, "Иван"), "ИКБО-33-21"); err == nil {
		t.Fatal("ReportSlug with a failing lookup = nil, want error")
	}
}

func contains(haystack, needle string) bool {
	return strings.Contains(haystack, needle)
}
