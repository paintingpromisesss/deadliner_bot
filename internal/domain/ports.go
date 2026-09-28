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
	SetDMNotify(ctx context.Context, groupID, userID int64, dmNotify *bool) error
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
	Create(ctx context.Context, d *Deadline) error
	GetByID(ctx context.Context, id int64) (*Deadline, error)
	Update(ctx context.Context, d *Deadline) error
	SetStatus(ctx context.Context, id int64, status DeadlineStatus) error
	SoftDelete(ctx context.Context, id int64) error
	ListByGroup(ctx context.Context, groupID int64, from, to *time.Time, status *DeadlineStatus) ([]Deadline, error)
	ListByOwner(ctx context.Context, ownerID int64, from, to *time.Time, status *DeadlineStatus) ([]Deadline, error)
}

type ReminderRepo interface {
	CreateBatch(ctx context.Context, reminders []Reminder) error
	FetchDue(ctx context.Context, tx Tx, now time.Time, limit int, workerID string) ([]Reminder, error)
	MarkSent(ctx context.Context, id int64, now time.Time) error
	MarkFailed(ctx context.Context, id int64, errMsg string, retryAt time.Time) error
	ReleaseStale(ctx context.Context, olderThan time.Time) (int64, error)
	CancelByDeadline(ctx context.Context, deadlineID int64) error
	Regenerate(ctx context.Context, deadlineID int64, presets []time.Duration, now time.Time) error
}

type InviteRepo interface {
	Create(ctx context.Context, inv *Invite) error
	GetByCode(ctx context.Context, code string) (*Invite, error)
	IncrementUsed(ctx context.Context, id int64) error
	Revoke(ctx context.Context, groupID int64, code string) error
	ListByGroup(ctx context.Context, groupID int64) ([]Invite, error)
}

type ClaimRepo interface {
	Create(ctx context.Context, c *ClaimCode) error
	GetActiveByGroup(ctx context.Context, groupID int64) (*ClaimCode, error)
	MarkUsed(ctx context.Context, id int64, now time.Time) error
	Revoke(ctx context.Context, id int64) error
}

type CounterRepo interface {
	IncAndCheck(ctx context.Context, userID int64, action string, windowStart time.Time, limit int) (int, error)
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
