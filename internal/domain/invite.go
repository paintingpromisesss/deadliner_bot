package domain

import "time"

type Invite struct {
	ID        int64
	GroupID   int64
	Code      string
	Role      Role
	MaxUses   int
	UsedCount int
	CreatedBy int64
	// ExpiresAt — срок жизни инвайта; NULL = бессрочный (действует, пока
	// не отозван вручную и не исчерпан лимит активаций).
	ExpiresAt time.Time
	RevokedAt *time.Time
}

// ExpiresAtValid — истёк ли инвайт: zero-срок = бессрочный (NULL в БД,
// действует, пока не отозван и не исчерпан лимит активаций).
func (i Invite) ExpiresAtValid(now time.Time) bool {
	return i.ExpiresAt.IsZero() || i.ExpiresAt.After(now)
}
