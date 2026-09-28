package domain

import "time"

type ClaimCode struct {
	ID        int64
	GroupID   int64
	CodeHash  string
	ChatID    int64
	MessageID int64
	CreatedBy int64
	ExpiresAt time.Time
	UsedAt    *time.Time
}
