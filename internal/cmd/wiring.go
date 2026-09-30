package cmd

import (
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sauron/deadliner/internal/app/auth"
	"github.com/sauron/deadliner/internal/app/claims"
	"github.com/sauron/deadliner/internal/app/deadlines"
	"github.com/sauron/deadliner/internal/app/groups"
	"github.com/sauron/deadliner/internal/app/moderation"
	"github.com/sauron/deadliner/internal/app/notifications"
	"github.com/sauron/deadliner/internal/config"
	"github.com/sauron/deadliner/internal/domain"
	"github.com/sauron/deadliner/internal/platform/repo"
)

// Значения, не вынесенные в env (спека §8 переменных для них не даёт). Они
// совпадают с дефолтами, которые сервисы применяют при нулевом конфиге;
// здесь продублированы явно, чтобы граф зависимостей serve читался целиком.
const (
	claimCooldown         = time.Minute
	claimConfirmFailLimit = 10
)

// repos — все репозитории одного пула: граф зависимостей подрежимов serve и
// admin собирается из одного места, чтобы состав адаптеров не расходился
// между ними (admin уже собирает moderation поверх ровно этих таблиц).
type repos struct {
	Users        domain.UserRepo
	Sessions     domain.SessionRepo
	Groups       domain.GroupRepo
	Memberships  domain.MembershipRepo
	Deadlines    domain.DeadlineRepo
	Reminders    domain.ReminderRepo
	Claims       domain.ClaimRepo
	Invites      domain.InviteRepo
	Counters     domain.CounterRepo
	ChatCounters domain.ChatCounterRepo
	Bindings     domain.ChatBindingRepo
	Audit        domain.AuditRepo
	Maintenance  domain.MaintenanceRepo
}

// newRepos конструирует набор репозиториев поверх пула.
func newRepos(pool *pgxpool.Pool) repos {
	return repos{
		Users:        repo.NewUsers(pool),
		Sessions:     repo.NewSessions(pool),
		Groups:       repo.NewGroups(pool),
		Memberships:  repo.NewMemberships(pool),
		Deadlines:    repo.NewDeadlines(pool),
		Reminders:    repo.NewReminders(pool),
		Claims:       repo.NewClaims(pool),
		Invites:      repo.NewInvites(pool),
		Counters:     repo.NewCounters(pool),
		ChatCounters: repo.NewChatCounters(pool),
		Bindings:     repo.NewBindings(pool),
		Audit:        repo.NewAudit(pool),
		Maintenance:  repo.NewMaintenance(pool),
	}
}

// sessionTTL — TTL сессии из конфига (дни → Duration).
func sessionTTL(cfg *config.Config) time.Duration {
	return time.Duration(cfg.App.SessionTTLDays) * 24 * time.Hour
}

// inviteTTL — дефолтный TTL инвайта из конфига (дни → Duration).
func inviteTTL(cfg *config.Config) time.Duration {
	return time.Duration(cfg.Limits.InviteDefaultTTLDays) * 24 * time.Hour
}

// newAuthService — вход по initData и сессии (спека §5.1).
func newAuthService(r repos, cfg *config.Config, clock domain.Clock) *auth.Service {
	return auth.NewService(r.Users, r.Sessions, auth.Config{
		BotToken:       cfg.Bot.Token,
		AuthDateMaxAge: cfg.App.AuthDateMaxAge,
		SessionTTL:     sessionTTL(cfg),
	}, clock)
}

// newGroupsService — группы, участники, инвайты, привязка чата (спека §3).
func newGroupsService(r repos, cfg *config.Config, clock domain.Clock, log *slog.Logger) *groups.Service {
	return groups.NewService(
		r.Groups, r.Memberships, r.Invites, r.Counters, r.Audit, r.Bindings,
		groups.Config{
			PendingTTL:       cfg.Limits.GroupPendingTTL,
			CreateDayLimit:   cfg.Limits.GroupCreateDay,
			CreateWeekLimit:  cfg.Limits.GroupCreateWeek,
			InviteDefaultTTL: inviteTTL(cfg),
		},
		clock, log,
	)
}

// newClaimsService — claim-флоу (спека §3.1). notifier — транспорт доставки
// кода в чат: в serve это telegram.ClaimsSender, в тестах — фейк.
func newClaimsService(r repos, cfg *config.Config, notifier claims.Notifier, clock domain.Clock, log *slog.Logger) *claims.Service {
	return claims.NewService(
		r.Groups, r.Memberships, r.Bindings, r.Claims,
		r.Counters, r.ChatCounters, r.Users, r.Audit, notifier,
		claims.Config{
			CodeTTL:          cfg.Limits.ClaimCodeTTL,
			RequestHourLimit: cfg.Limits.ClaimPerChatHour,
			Cooldown:         claimCooldown,
			ConfirmFailLimit: claimConfirmFailLimit,
		},
		clock, log,
	)
}

// newDeadlinesService — дедлайны и генерация reminders (спека §7.1).
func newDeadlinesService(r repos, clock domain.Clock, log *slog.Logger) *deadlines.Service {
	return deadlines.NewService(r.Deadlines, r.Reminders, r.Groups, r.Memberships, r.Audit, clock, log)
}

// newNotificationsService — настройки уведомлений (спека §5.2).
func newNotificationsService(r repos) *notifications.Service {
	return notifications.NewService(r.Users, r.Memberships, r.Groups)
}

// newModerationService — модерация инстанса: cleanup, бан, /stats (спека §3.3).
// Миграции здесь не применяются: CLI не должен менять схему (см. Admin).
func newModerationService(r repos, cfg *config.Config, clock domain.Clock, log *slog.Logger) *moderation.Service {
	return moderation.NewService(moderation.Deps{
		Groups:      r.Groups,
		Deadlines:   r.Deadlines,
		Reminders:   r.Reminders,
		Memberships: r.Memberships,
		Bindings:    r.Bindings,
		Users:       r.Users,
		Sessions:    r.Sessions,
		Maintenance: r.Maintenance,
		Audit:       r.Audit,
		Clock:       clock,
		Log:         log,
		Config: moderation.Config{
			PendingTTL:       cfg.Limits.GroupPendingTTL,
			CounterRetention: cfg.Limits.CounterRetention,
		},
	})
}
