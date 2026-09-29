package repo

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sauron/deadliner/internal/domain"
)

// maintenanceRepo — служебные операции над таблицами без доменной сущности:
// статистика инстанса для /stats и уборка протухших окон rate-limit-счётчиков
// и сессий (cleanup-джоба, Task 12). Отдельный репозиторий, а не расширение
// UserRepo/SessionRepo: единственный потребитель — moderation.Service, и
// агрегаты бьют по нескольким таблицам сразу.
type maintenanceRepo struct {
	pool *pgxpool.Pool
}

func NewMaintenance(pool *pgxpool.Pool) domain.MaintenanceRepo {
	return &maintenanceRepo{pool: pool}
}

var _ domain.MaintenanceRepo = (*maintenanceRepo)(nil)

// statsQuery — один SELECT: счётчики инстанса одним запросом, чтобы /stats не
// собирал их N+1 по таблицам. Все подзапросы читают индексы по статусам/датам.
const statsQuery = `
	SELECT
	  (SELECT count(*) FROM users)                                              AS users,
	  (SELECT count(*) FROM groups WHERE deleted_at IS NULL)                    AS groups_total,
	  (SELECT count(*) FROM groups WHERE deleted_at IS NULL AND status = 'active') AS groups_active,
	  (SELECT count(*) FROM groups WHERE deleted_at IS NULL AND status = 'pending') AS groups_pending,
	  (SELECT count(*) FROM deadlines WHERE deleted_at IS NULL AND status = 'active') AS deadlines_active,
	  (SELECT count(*) FROM reminders WHERE status = 'pending')                 AS reminders_pending,
	  (SELECT count(*) FROM reminders WHERE status = 'failed')                  AS reminders_failed,
	  (SELECT count(*) FROM sessions WHERE expires_at > $1)                     AS sessions_active`

func (r *maintenanceRepo) Stats(ctx context.Context, now time.Time) (domain.Stats, error) {
	var s domain.Stats
	err := r.pool.QueryRow(ctx, statsQuery, now).Scan(
		&s.Users, &s.GroupsTotal, &s.GroupsActive, &s.GroupsPending,
		&s.DeadlinesActive, &s.RemindersPending, &s.RemindersFailed, &s.SessionsActive,
	)
	if err != nil {
		return domain.Stats{}, mapErr(err)
	}
	return s, nil
}

// PurgeCounters удаляет окна счётчиков старше olderThan. Самое длинное окно
// лимита — НЕДЕЛЬНОЕ (group_create_week, 168ч, groups.Service), а окно
// floor-ится на своё начало: живая строка недельного счётчика бывает почти
// 168 часов от роду. Поэтому olderThan обязан быть больше 168ч (конфиг
// COUNTER_RETENTION, дефолт 192ч; config.Load отвергает значение ≤ 168ч) —
// меньшая ретенция удаляла бы ЖИВУЮ строку и LIMIT_GROUP_CREATE_WEEK молча
// перестал бы срабатывать. Оба счётчика (user и chat) чистятся одной
// транзакцией: частичная уборка оставила бы рассинхрон user/chat-лимитов.
// Возвращает число строк.
func (r *maintenanceRepo) PurgeCounters(ctx context.Context, olderThan time.Time) (int64, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, mapErr(err)
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx,
		`DELETE FROM user_action_counters WHERE window_start < $1`, olderThan)
	if err != nil {
		return 0, mapErr(err)
	}
	users := tag.RowsAffected()

	tag, err = tx.Exec(ctx,
		`DELETE FROM chat_action_counters WHERE window_start < $1`, olderThan)
	if err != nil {
		return 0, mapErr(err)
	}
	chats := tag.RowsAffected()

	if err := tx.Commit(ctx); err != nil {
		return 0, mapErr(err)
	}
	return users + chats, nil
}

// PurgeExpiredSessions удаляет строки сессий, истёкшие до olderThan. Грейс
// задаёт вызывающий (cleanup берёт 7 дней): сама сессия недействительна уже в
// expires_at (GetActive сравнивает с now), грейс нужен лишь для того, чтобы
// не удалять свежие «только что истёкшие» токены — по ним ещё возможны
// диагностика и разбор инцидентов.
func (r *maintenanceRepo) PurgeExpiredSessions(ctx context.Context, olderThan time.Time) (int64, error) {
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM sessions WHERE expires_at < $1`, olderThan)
	if err != nil {
		return 0, mapErr(err)
	}
	return tag.RowsAffected(), nil
}
