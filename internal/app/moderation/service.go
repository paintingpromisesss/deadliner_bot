// Package moderation — use cases модерации инстанса (спека §3.3, §6.1):
// cleanup протухших pending-групп, уборка счётчиков и сессий, бан/разбан,
// promotion, удаление группы и статистика. Зависит только от domain-портов
// (правило зависимостей). Аудит: cleanup пишет actor_user_id = NULL,
// действия супер-админа — его user_id.
package moderation

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"time"

	"github.com/sauron/deadliner/internal/domain"
)

// pendingScanLimit — максимум групп за один прогон cleanup. Партия ограничена
// намеренно: джоба идёт в цикле сервиса, а не держит транзакцию над всей
// таблицей (следующий прогон доберёт остаток).
const pendingScanLimit = 500

// defaultCounterRetention — ретенция окна rate-limit-счётчика по умолчанию
// (8 суток). Обязана быть больше недельного окна лимита group_create_week
// (168ч): окно floor-ится на своё начало, поэтому живая строка недельного
// счётчика бывает почти 168 часов от роду, и ретенция ≤ 168ч удаляла бы её.
// Реальное значение — из конфига (COUNTER_RETENTION, валидируется в
// config.Load).
const defaultCounterRetention = 8 * 24 * time.Hour

const (
	// sessionGrace — грейс после expires_at, на который сессия ещё остаётся в
	// БД: GetActive и так отвергает истёкшие, здесь только уборка.
	sessionGrace = 7 * 24 * time.Hour
	// defaultCleanupInterval — интервал cleanup по умолчанию (спека: каждый час).
	defaultCleanupInterval = time.Hour
)

// Config — параметры модерации из env (спека §8).
type Config struct {
	// PendingTTL — TTL pending-группы (GROUP_PENDING_TTL_DAYS, дефолт 14 дней).
	// Для CLI-вывода; cut-off джоба берёт из claim_expires_at группы.
	PendingTTL time.Duration
	// CounterRetention — сколько живёт окно rate-limit-счётчика после его
	// начала (COUNTER_RETENTION, дефолт 192ч); обязано быть больше недельного
	// окна лимита (проверяется в config.Load).
	CounterRetention time.Duration
}

// Deps — зависимости сервиса модерации (только domain-порты).
type Deps struct {
	Groups      domain.GroupRepo
	Deadlines   domain.DeadlineRepo
	Reminders   domain.ReminderRepo
	Memberships domain.MembershipRepo
	Bindings    domain.ChatBindingRepo
	Users       domain.UserRepo
	Sessions    domain.SessionRepo
	// Maintenance — служебные таблицы без доменной сущности (счётчики,
	// сессии-как-статистика) и агрегаты для Stats.
	Maintenance domain.MaintenanceRepo
	Audit       domain.AuditRepo
	Clock       domain.Clock
	Log         *slog.Logger
	Config      Config
}

// Service — use cases модерации.
type Service struct {
	deps Deps
	cfg  Config
	log  *slog.Logger
}

func NewService(d Deps) *Service {
	if d.Log == nil {
		d.Log = slog.Default()
	}
	if d.Clock == nil {
		d.Clock = domain.SystemClock{}
	}
	if d.Config.CounterRetention <= 0 {
		// Вызывающий без конфига (тесты): значение ≤ 168ч было бы багом.
		d.Config.CounterRetention = defaultCounterRetention
	}
	return &Service{deps: d, cfg: d.Config, log: d.Log}
}

// CleanupReport — что сделал один прогон cleanup (идёт в аудит и в лог).
type CleanupReport struct {
	Groups         int `json:"groups"`
	Reminders      int `json:"reminders"`
	CountersPurged int `json:"counters_purged"`
	SessionsPurged int `json:"sessions_purged"`
}

// CleanupExpiredPending — автоудаление протухших pending-групп (спека §3.3:
// 14 дней без привязки чата И без админа) плюс уборка счётчиков и сессий.
// Критерий проверяется по каждой группе: claim_expires_at — не единственное
// условие, привязка чата или админ «спасают» группу (кандидаты —
// ListPendingExpired). Сначала гасим напоминания, затем soft-delete группы —
// иначе напоминания останутся у невидимой группы. Ошибка по отдельной группе
// не прерывает прогон.
func (s *Service) CleanupExpiredPending(ctx context.Context) (CleanupReport, error) {
	now := s.deps.Clock.Now()
	var report CleanupReport

	candidates, err := s.deps.Groups.ListPendingExpired(ctx, now, pendingScanLimit)
	if err != nil {
		return report, fmt.Errorf("moderation: list expired pending groups: %w", err)
	}

	for i := range candidates {
		g := candidates[i]
		keep, err := s.hasBindingOrAdmin(ctx, g.ID)
		if err != nil {
			s.log.Warn("cleanup: group inspection failed",
				slog.Int64("group_id", g.ID), slog.String("error", err.Error()))
			continue
		}
		if keep {
			continue
		}

		cancelled, err := s.cancelGroupReminders(ctx, g.ID)
		if err != nil {
			s.log.Warn("cleanup: reminder cancel failed",
				slog.Int64("group_id", g.ID), slog.String("error", err.Error()))
			continue
		}
		if err := s.deps.Groups.SoftDelete(ctx, g.ID); err != nil {
			s.log.Warn("cleanup: soft delete failed",
				slog.Int64("group_id", g.ID), slog.String("error", err.Error()))
			continue
		}
		report.Groups++
		// Reminders — число pending-напоминаний, увиденных перед гашением
		// (справочно: при гонке с воркером может быть на единицу больше
		// фактически погашенных).
		report.Reminders += cancelled
		s.log.Info("cleanup: pending group deleted",
			slog.Int64("group_id", g.ID), slog.String("slug", g.Slug))
	}

	// Служебная уборка: окна счётчиков и протухшие сессии. Сбой уборки не
	// отменяет уже удалённые группы и не роняет прогон. Ретенция счётчиков —
	// из конфига (COUNTER_RETENTION) и обязана быть больше самого длинного
	// окна лимита.
	counters, err := s.deps.Maintenance.PurgeCounters(ctx, now.Add(-s.cfg.CounterRetention))
	if err != nil {
		s.log.Warn("cleanup: counter purge failed", slog.String("error", err.Error()))
	} else {
		report.CountersPurged = int(counters)
	}
	sessions, err := s.deps.Maintenance.PurgeExpiredSessions(ctx, now.Add(-sessionGrace))
	if err != nil {
		s.log.Warn("cleanup: session purge failed", slog.String("error", err.Error()))
	} else {
		report.SessionsPurged = int(sessions)
	}

	s.writeAudit(ctx, nil, "cleanup.run", "instance", nil, map[string]any{
		"groups":          report.Groups,
		"reminders":       report.Reminders,
		"counters_purged": report.CountersPurged,
		"sessions_purged": report.SessionsPurged,
	})
	return report, nil
}

// hasBindingOrAdmin — «спасающие» условия TTL: у группы есть привязанный чат
// либо хотя бы один админ. Любое из них означает, что группа живая и TTL
// применять нельзя.
func (s *Service) hasBindingOrAdmin(ctx context.Context, groupID int64) (bool, error) {
	if _, err := s.deps.Bindings.GetByGroup(ctx, groupID); err == nil {
		return true, nil
	} else if !errors.Is(err, domain.ErrNotFound) {
		return false, err
	}

	admins, err := s.deps.Memberships.CountAdmins(ctx, groupID)
	if err != nil {
		return false, err
	}
	return admins > 0, nil
}

// cancelGroupReminders гасит pending-напоминания всех дедлайнов группы и
// возвращает их число: ListByGroup, затем CancelByDeadline на каждый
// (булк-метода «по группе» в порту нет, списки короткие). Удалённых дедлайнов
// ListByGroup не возвращает — их напоминания уже погашены.
func (s *Service) cancelGroupReminders(ctx context.Context, groupID int64) (int, error) {
	ds, err := s.deps.Deadlines.ListByGroup(ctx, groupID, nil, nil, nil)
	if err != nil {
		return 0, err
	}
	cancelled := 0
	for _, d := range ds {
		rems, err := s.deps.Reminders.ListByDeadline(ctx, d.ID)
		if err != nil {
			return cancelled, err
		}
		for _, r := range rems {
			if r.Status == domain.ReminderStatusPending {
				cancelled++
			}
		}
		if err := s.deps.Reminders.CancelByDeadline(ctx, d.ID); err != nil {
			return cancelled, err
		}
	}
	return cancelled, nil
}

// PromoteSuperadmin выдаёт пользователю (по telegram_id) права супер-админа.
func (s *Service) PromoteSuperadmin(ctx context.Context, actor *domain.User, telegramID int64) error {
	if err := s.requireSuperadmin(actor); err != nil {
		return err
	}
	u, err := s.deps.Users.GetByTelegramID(ctx, telegramID)
	if err != nil {
		return err
	}
	if u.IsSuperadmin {
		return nil // идемпотентно: повторный promote ничего не меняет
	}
	if err := s.deps.Users.SetSuperadmin(ctx, u.ID, true); err != nil {
		return err
	}
	s.writeAudit(ctx, actor, "user.promote", "user", &u.ID, map[string]any{"telegram_id": telegramID})
	return nil
}

// BanUser банит пользователя (спека §3.3) и немедленно гасит все его сессии
// — ради этого и выбран opaque-токен вместо JWT (спека §5.1). Себя и другого
// супер-админа банить нельзя (иначе ошибка в консоли отрезает управление
// инстансом): сначала снимите права, потом баньте.
func (s *Service) BanUser(ctx context.Context, actor *domain.User, telegramID int64) error {
	if err := s.requireSuperadmin(actor); err != nil {
		return err
	}
	u, err := s.deps.Users.GetByTelegramID(ctx, telegramID)
	if err != nil {
		return err
	}
	if actor.ID != 0 && u.ID == actor.ID {
		return fmt.Errorf("%w: cannot ban yourself (telegram_id=%d)", domain.ErrForbidden, telegramID)
	}
	if u.IsSuperadmin {
		return fmt.Errorf("%w: cannot ban a superadmin (telegram_id=%d)", domain.ErrForbidden, telegramID)
	}
	if err := s.deps.Users.SetBanned(ctx, u.ID, true); err != nil {
		return err
	}
	if err := s.deps.Sessions.RevokeAllForUser(ctx, u.ID); err != nil {
		// Флаг бана уже стоит: middleware всё равно отвергнет запросы, но
		// сессии остались — пишем error, чтобы это не потерялось в логах.
		s.log.Error("moderation: revoke sessions after ban failed",
			slog.Int64("user_id", u.ID), slog.String("error", err.Error()))
		return err
	}
	s.writeAudit(ctx, actor, "user.ban", "user", &u.ID, map[string]any{"telegram_id": telegramID})
	return nil
}

// UnbanUser снимает бан. Сессии не восстанавливаются: вход заново через
// initData.
func (s *Service) UnbanUser(ctx context.Context, actor *domain.User, telegramID int64) error {
	if err := s.requireSuperadmin(actor); err != nil {
		return err
	}
	u, err := s.deps.Users.GetByTelegramID(ctx, telegramID)
	if err != nil {
		return err
	}
	if err := s.deps.Users.SetBanned(ctx, u.ID, false); err != nil {
		return err
	}
	s.writeAudit(ctx, actor, "user.unban", "user", &u.ID, map[string]any{"telegram_id": telegramID})
	return nil
}

// DeleteGroup — удаление группы по слагу (soft delete, спека §6.1). Слаг
// приходит от человека, поэтому нормализуется так же, как при создании и
// привязке; напоминания группы гасятся вместе с группой.
func (s *Service) DeleteGroup(ctx context.Context, actor *domain.User, slug string) (*domain.Group, error) {
	if err := s.requireSuperadmin(actor); err != nil {
		return nil, err
	}
	g, err := s.deps.Groups.GetBySlugNorm(ctx, domain.Normalize(slug))
	if err != nil {
		return nil, err
	}
	if _, err := s.cancelGroupReminders(ctx, g.ID); err != nil {
		return nil, err
	}
	if err := s.deps.Groups.SoftDelete(ctx, g.ID); err != nil {
		return nil, err
	}
	s.writeAudit(ctx, actor, "group.delete", "group", &g.ID, map[string]any{
		"slug": g.Slug, "source": "superadmin",
	})
	return g, nil
}

// Stats — счётчики инстанса для /stats и CLI (спека §6.1, §7.2 п.4).
// Читается одной реализацией MaintenanceRepo — без N+1.
func (s *Service) Stats(ctx context.Context, actor *domain.User) (domain.Stats, error) {
	if err := s.requireSuperadmin(actor); err != nil {
		return domain.Stats{}, err
	}
	return s.deps.Maintenance.Stats(ctx, s.deps.Clock.Now())
}

// PendingTTL — TTL pending-групп из конфига (CLI печатает его в `admin stats`
// и в справке cleanup; сама джоба опирается на claim_expires_at строки).
func (s *Service) PendingTTL() time.Duration { return s.cfg.PendingTTL }

// listGroupsLimit — максимум групп в одном ответе `admin list-groups`
// (CLI печатает таблицу в терминал; при превышении — предупреждение об
// усечении). Экспортирован как ListGroupsLimit: предупреждение обязано
// называть ровно то число, которым ограничен вывод.
const listGroupsLimit = 500

// ListGroupsLimit — публичное имя лимита `admin list-groups` (см. выше).
const ListGroupsLimit = listGroupsLimit

// GroupSummary — строка `admin list-groups` (спека §2): слаг, название,
// статус, число участников и время создания. Отдельная проекция, а не
// domain.Group: CLI нужны ровно эти пять полей, а число участников требует
// обращения к memberships.
type GroupSummary struct {
	Slug         string
	Title        string
	Status       domain.GroupStatus
	MembersCount int
	CreatedAt    time.Time
}

// ListGroups — все неудалённые группы с числом участников (CLI
// `admin list-groups`, спека §2). Видны и pending-группы: они не попадают ни
// в один ListMine, а супер-админу нужны именно они (разбор конфликтов слагов
// и брошенных заявок). Возвращает строки и признак «список усечён лимитом».
func (s *Service) ListGroups(ctx context.Context, actor *domain.User, status *domain.GroupStatus, limit int) ([]GroupSummary, bool, error) {
	if err := s.requireSuperadmin(actor); err != nil {
		return nil, false, err
	}
	if limit <= 0 {
		limit = listGroupsLimit
	}
	groups, err := s.deps.Groups.ListAll(ctx, status, limit+1)
	if err != nil {
		return nil, false, err
	}
	truncated := false
	if len(groups) > limit {
		groups = groups[:limit]
		truncated = true
	}

	out := make([]GroupSummary, 0, len(groups))
	for _, g := range groups {
		mems, err := s.deps.Memberships.ListByGroup(ctx, g.ID)
		if err != nil {
			return nil, false, fmt.Errorf("moderation: list members of group id=%d: %w", g.ID, err)
		}
		out = append(out, GroupSummary{
			Slug:         g.Slug,
			Title:        g.Title,
			Status:       g.Status,
			MembersCount: len(mems),
			CreatedAt:    g.CreatedAt,
		})
	}
	return out, truncated, nil
}

// requireSuperadmin — единственная проверка прав модерации (спека §3: CLI и
// бот-команды). nil-актор (аноним) — тоже отказ.
func (s *Service) requireSuperadmin(actor *domain.User) error {
	if actor == nil || !actor.IsSuperadmin {
		return fmt.Errorf("%w: moderation requires superadmin", domain.ErrForbidden)
	}
	return nil
}

// writeAudit — best-effort запись в audit_log: сбой аудита не роняет уже
// согласованную операцию, но остаётся предупреждением в логе. actor == nil
// (cleanup-джоба) и actor.ID == 0 (SystemActor CLI) дают actor_user_id = NULL —
// семантика системного действия.
func (s *Service) writeAudit(ctx context.Context, actor *domain.User, action, targetType string, targetID *int64, meta map[string]any) {
	var actorID *int64
	if actor != nil && actor.ID != 0 {
		actorID = &actor.ID
	}
	e := &domain.AuditEntry{
		ActorUserID: actorID,
		Action:      action,
		TargetType:  targetType,
		TargetID:    targetID,
		Meta:        meta,
	}
	if err := s.deps.Audit.Write(ctx, e); err != nil {
		s.log.Warn("audit write failed",
			slog.String("action", action),
			slog.String("error", err.Error()),
		)
	}
}

// SystemActor — актор CLI-пути: команда запускается оператором на сервере,
// строки в users у неё нет (ID 0); writeAudit пишет actor_user_id = NULL.
var SystemActor = &domain.User{IsSuperadmin: true}

// Cleaner — поверхность cleanup для планировщика (реализуется *Service).
type Cleaner interface {
	CleanupExpiredPending(ctx context.Context) (CleanupReport, error)
}

// StartCleanupLoop — часовая петля cleanup (спека §3.3, §12 п.7): первый
// прогон сразу на старте, дальше — interval. Блокирующая: serve гоняет её в
// горутине и закрывает по ctx.Done. Ошибка прогона логируется и не завершает
// петлю; паника ловится в runCleanupOnce, иначе она убила бы процесс serve.
func StartCleanupLoop(ctx context.Context, svc Cleaner, interval time.Duration, log *slog.Logger) {
	if log == nil {
		log = slog.Default()
	}
	interval = cleanupInterval(interval)
	log.Info("cleanup: loop started", slog.Duration("interval", interval))

	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Info("cleanup: loop stopped")
			return
		case <-timer.C:
		}

		if ctx.Err() != nil {
			return
		}
		runCleanupOnce(ctx, svc, log)
		timer.Reset(interval)
	}
}

// runCleanupOnce — один прогон с защитой от паники: паника в репозитории
// не должна ронять процесс serve. Ошибка и паника логируются; петля
// продолжает работу по расписанию.
func runCleanupOnce(ctx context.Context, svc Cleaner, log *slog.Logger) {
	defer func() {
		if r := recover(); r != nil {
			log.Error("cleanup: run panicked",
				slog.Any("panic", r),
				slog.String("stack", string(debug.Stack())),
			)
		}
	}()

	report, err := svc.CleanupExpiredPending(ctx)
	if err != nil {
		if ctx.Err() == nil {
			log.Error("cleanup: run failed", slog.String("error", err.Error()))
		}
		return
	}
	if report != (CleanupReport{}) {
		log.Info("cleanup: done",
			slog.Int("groups", report.Groups),
			slog.Int("reminders", report.Reminders),
			slog.Int("counters_purged", report.CountersPurged),
			slog.Int("sessions_purged", report.SessionsPurged),
		)
	}
}

// cleanupInterval — ноль/отрицательный интервал означает busy-loop
// (timer.Reset(0) крутится без пауз), поэтому подставляется дефолт.
func cleanupInterval(d time.Duration) time.Duration {
	if d <= 0 {
		return defaultCleanupInterval
	}
	return d
}
