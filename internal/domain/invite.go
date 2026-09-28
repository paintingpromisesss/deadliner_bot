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
	ExpiresAt time.Time
	RevokedAt *time.Time
}
