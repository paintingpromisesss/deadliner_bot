package domain

import (
	"context"
	"time"
)

// Row mirrors pgx.Row; platform adapters satisfy it without domain importing pgx.
type Row interface {
	Scan(dest ...any) error
}

// Tx is the minimal transactional surface the domain ports may rely on.
type Tx interface {
	Exec(ctx context.Context, sql string, args ...any) error
	QueryRow(ctx context.Context, sql string, args ...any) Row
}

type Clock interface {
	Now() time.Time
}

type Notifier interface {
	SendToChat(ctx context.Context, chatID, threadID int64, text string) error
	SendToUser(ctx context.Context, userID int64, text string) error
}

// SlugProvider is the extension point for a future university API (spec §2.2).
type SlugProvider interface {
	Validate(slug string) error
	Suggest(ctx context.Context, prefix string) ([]string, error)
}

type UserRepo interface {
	GetByTelegramID(ctx context.Context, telegramID int64) (*User, error)
	GetByID(ctx context.Context, id int64) (*User, error)
	UpsertByTelegram(ctx context.Context, u *User) error
	UpdateSettings(ctx context.Context, id int64, tz string, dmNotifyDefault bool) error
	SetBanned(ctx context.Context, id int64, banned bool) error
	SetSuperadmin(ctx context.Context, id int64, superadmin bool) error
	MarkBotBlocked(ctx context.Context, telegramID int64, blocked bool) error
}

type GroupRepo interface {
	Create(ctx context.Context, g *Group) error
	GetByID(ctx context.Context, id int64) (*Group, error)
	GetBySlugNorm(ctx context.Context, slugNorm string) (*Group, error)
	// SearchByPrefix returns non-deleted groups whose slug_norm starts with
	// prefix: active groups plus pending groups created by callerID (spec §6.4).
	SearchByPrefix(ctx context.Context, prefix string, callerID int64, limit int) ([]Group, error)
	Update(ctx context.Context, g *Group) error
	SetStatus(ctx context.Context, id int64, status GroupStatus) error
	SoftDelete(ctx context.Context, id int64) error
	// ListMine returns non-deleted groups the user is a member of (via
	// group_memberships), ordered by slug_norm.
	ListMine(ctx context.Context, userID int64) ([]Group, error)
	ListPendingExpired(ctx context.Context, now time.Time, limit int) ([]Group, error)
}

type MembershipRepo interface {
	Upsert(ctx context.Context, m *Membership) error
	Get(ctx context.Context, groupID, userID int64) (*Membership, error)
	ListByGroup(ctx context.Context, groupID int64) ([]Membership, error)
	// ListByGroupDetailed — список участников с username/first_name одним JOIN.
	ListByGroupDetailed(ctx context.Context, groupID int64) ([]MembershipDetail, error)
	ListByUser(ctx context.Context, userID int64) ([]Membership, error)
	SetRole(ctx context.Context, groupID, userID int64, role Role) error
	// DemoteIfNotLastAdmin понижает админа до member одним условным UPDATE:
	// ErrNotFound если строки нет/она не admin, ErrConflict если это последний
	// админ группы (группа не должна остаться без админа).
	DemoteIfNotLastAdmin(ctx context.Context, groupID, userID int64) error
	// RemoveIfNotLastAdmin удаляет membership одним условным UPDATE:
	// ErrNotFound если строки нет, ErrConflict если это последний админ.
	RemoveIfNotLastAdmin(ctx context.Context, groupID, userID int64) error
	SetDMNotify(ctx context.Context, groupID, userID int64, dmNotify *bool) error
	// ListDMTargets — users.id участников группы с эффективным dm_notify
	// (COALESCE(memberships.dm_notify, users.dm_notify_default)), без
	// bot_blocked — цели dm_dup fan-out (спека §7.3). Упорядочено по user_id.
	ListDMTargets(ctx context.Context, groupID int64) ([]int64, error)
	Delete(ctx context.Context, groupID, userID int64) error
	CountAdmins(ctx context.Context, groupID int64) (int, error)
}

type ChatBindingRepo interface {
	Create(ctx context.Context, b *ChatBinding) error
	GetByGroup(ctx context.Context, groupID int64) (*ChatBinding, error)
	GetByChat(ctx context.Context, chatID int64, threadID *int64) (*ChatBinding, error)
	Delete(ctx context.Context, groupID int64) error
}

type DeadlineRepo interface {
	// Create пишет deadline и его reminders в ОДНОЙ транзакции (спека §7.1).
	// Reminder-строки получают deadline_id созданного дедлайна. Вставка
	// СТРОГАЯ: дубликат на unique-индексе → ErrConflict и откат всей
	// транзакции (deadline не остаётся в БД). Заполняет d.ID и ID/DeadlineID
	// каждого reminder.
	Create(ctx context.Context, d *Deadline, reminders []Reminder) error
	GetByID(ctx context.Context, id int64) (*Deadline, error)
	// Update меняет только явно заданные поля patch (не всю сущность).
	Update(ctx context.Context, id int64, patch DeadlinePatch) error
	SetStatus(ctx context.Context, id int64, status DeadlineStatus) error
	SoftDelete(ctx context.Context, id int64) error
	ListByGroup(ctx context.Context, groupID int64, from, to *time.Time, status *DeadlineStatus) ([]Deadline, error)
	ListByOwner(ctx context.Context, ownerID int64, from, to *time.Time, status *DeadlineStatus) ([]Deadline, error)
}

// DeadlinePatch — явные поля для UPDATE дедлайна: nil = «не трогать».
type DeadlinePatch struct {
	Title       *string
	Description *string
	DueAt       *time.Time
	TZ          *string
}

type ReminderRepo interface {
	CreateBatch(ctx context.Context, reminders []Reminder) error
	// ListByDeadline — все reminders дедлайна (любой статус), по fire_at.
	ListByDeadline(ctx context.Context, deadlineID int64) ([]Reminder, error)
	// FetchDue блокирует due-pending строки в tx (FOR UPDATE SKIP LOCKED) и
	// помечает их locked_by/locked_at (спека §7.2). Реализация tx ДОЛЖНА также
	// удовлетворять Query(ctx, sql, args...) (pgx.Rows, error) — порт Tx
	// объявляет только Exec/QueryRow, репо утверждает tx к этому расширению и
	// возвращает ошибку, если его нет (см. адаптер в repo-тестах).
	FetchDue(ctx context.Context, tx Tx, now time.Time, limit int, workerID string) ([]Reminder, error)
	// MarkSent — UPDATE … WHERE status='pending' AND locked_by=workerID;
	// ok=false если строку уже отправили/забрал другой воркер (идемпотентность
	// доставки, спека §7.2).
	MarkSent(ctx context.Context, id int64, workerID string, now time.Time) (bool, error)
	// MarkSentWithFanout атомарно в tx: MarkSent родителя + вставка
	// dm_dup-детей (ON CONFLICT DO NOTHING, спека §7.3). ok=false — лок
	// потерян/строка уже sent: дети НЕ создаются, fan-out подавлен.
	MarkSentWithFanout(ctx context.Context, tx Tx, reminderID int64, workerID string, now time.Time, children []Reminder) (ok bool, err error)
	// MarkFailed инкрементирует attempts и пишет last_error; если попыток
	// осталось — status='pending', fire_at=retryAt (fire_at переиспользуется
	// как время ретрая: отдельной колонки retry_at в схеме нет), иначе
	// status='failed'. failed=true если reminder помечен failed.
	MarkFailed(ctx context.Context, id int64, workerID, errText string, retryAt time.Time, maxAttempts int) (failed bool, err error)
	// ReleaseStale снимает локи старше olderThan (pending, locked_at <
	// olderThan): locked_by/locked_at=NULL, attempts+=1; возвращает число строк.
	ReleaseStale(ctx context.Context, olderThan time.Time) (int64, error)
	CancelByDeadline(ctx context.Context, deadlineID int64) error
	// Regenerate в ОДНОЙ транзакции: pending → cancelled, затем новые
	// reminders. Unique-индексы не учитывают status, поэтому вместо вставки
	// поверх cancelled строка детерминированно «воскрешается»: preset/
	// custom_offset — старейшая по id cancelled-строка с тем же (kind,
	// offset), если новый fire_at не занят другой строкой (иначе остаётся
	// cancelled); custom_at — cancelled-строка с точно тем же fire_at.
	// Свежие вставки — ON CONFLICT DO NOTHING (гонки/повторы терпимы);
	// sent/failed не трогаются. Возвращает число строк, ставших pending.
	// КОНТРАКТ: Regenerate — отдельная транзакция от deadlines.Update, поэтому
	// вызывающий use case при сбое ОБЯЗАН компенсировать уже применённый
	// патч дедлайна (см. deadlines.Service.Update).
	Regenerate(ctx context.Context, deadlineID int64, newReminders []Reminder) (inserted int, err error)
}

type InviteRepo interface {
	Create(ctx context.Context, inv *Invite) error
	GetByCode(ctx context.Context, code string) (*Invite, error)
	// IncrementUsed атомарно расходует одно использование: ErrConflict если
	// инвайт отозван/истёк/исчерпан (условие — в SQL, гонки исключены).
	IncrementUsed(ctx context.Context, id int64) error
	Revoke(ctx context.Context, groupID int64, code string) error
	ListByGroup(ctx context.Context, groupID int64) ([]Invite, error)
}

type ClaimRepo interface {
	Create(ctx context.Context, c *ClaimCode) error
	// GetActiveByGroup — самый свежий действующий код группы: used_at IS NULL
	// и expires_at > now; ErrNotFound, если активного кода нет (истёкший и
	// отсутствующий неразличимы — спека §3.1, не раскрываем существование).
	GetActiveByGroup(ctx context.Context, groupID int64, now time.Time) (*ClaimCode, error)
	// ListActiveByGroup — все действующие коды группы, по id.
	ListActiveByGroup(ctx context.Context, groupID int64, now time.Time) ([]ClaimCode, error)
	// MarkUsed условно гасит код (WHERE used_at IS NULL): ErrNotFound, если
	// строку уже погасили/отозвали. Гонка двух Confirm разрешается здесь —
	// успех получает ровно один вызов.
	MarkUsed(ctx context.Context, id int64, now time.Time) error
	// RevokeActiveByGroup гасит все действующие коды группы (used_at = now).
	// Отдельного маркера revoked в схеме нет (Task 10 ruling): «отозван» и
	// «использован» помечены одинаково — used_at NOT NULL = неактивен.
	RevokeActiveByGroup(ctx context.Context, groupID int64, now time.Time) error
}

type CounterRepo interface {
	IncAndCheck(ctx context.Context, userID int64, action string, windowStart time.Time, limit int) (int, error)
}

// ChatCounterRepo — счётчики, привязанные к ЧАТУ, а не к пользователю
// (спека §3.3: «лимит claim-кодов — 3/час на ЧАТ»). Отдельная таблица
// chat_action_counters без FK: чат существует только как Telegram chat_id и
// может быть ещё не привязан к группе. Семантика IncAndCheck — как у
// CounterRepo: возвращает счётчик ПОСЛЕ инкремента, политика (count > limit)
// остаётся в app-слое.
type ChatCounterRepo interface {
	IncAndCheck(ctx context.Context, chatID int64, action string, windowStart time.Time, limit int) (int, error)
}

type Session struct {
	TokenHash string
	UserID    int64
	ExpiresAt time.Time
	CreatedAt time.Time
	LastSeen  time.Time
}

type SessionRepo interface {
	Create(ctx context.Context, s *Session) error
	GetActive(ctx context.Context, tokenHash string, now time.Time) (*Session, error)
	Touch(ctx context.Context, tokenHash string, lastSeen, expiresAt time.Time) error
	Revoke(ctx context.Context, tokenHash string) error
	RevokeAllForUser(ctx context.Context, userID int64) error
}

type AuditEntry struct {
	ID          int64
	ActorUserID *int64
	Action      string
	TargetType  string
	TargetID    *int64
	Meta        map[string]any
	CreatedAt   time.Time
}

type AuditRepo interface {
	Write(ctx context.Context, e *AuditEntry) error
}
