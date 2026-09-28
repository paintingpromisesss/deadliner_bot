package domain

import (
	"fmt"
	"time"
)

type DeadlineStatus string

const (
	DeadlineStatusActive   DeadlineStatus = "active"
	DeadlineStatusDone     DeadlineStatus = "done"
	DeadlineStatusArchived DeadlineStatus = "archived"
)

type Deadline struct {
	ID          int64
	GroupID     *int64
	OwnerUserID *int64
	Title       string
	Description string
	DueAt       time.Time
	TZ          string
	CreatedBy   int64
	Status      DeadlineStatus
	CreatedAt   time.Time
	UpdatedAt   time.Time
	DeletedAt   *time.Time
}

// ValidateOwnership enforces the DB CHECK: exactly one of GroupID (group
// deadline) or OwnerUserID (personal) must be set.
func (d Deadline) ValidateOwnership() error {
	switch {
	case d.GroupID != nil && d.OwnerUserID != nil:
		return fmt.Errorf("%w: group_id and owner_user_id are mutually exclusive", ErrValidation)
	case d.GroupID == nil && d.OwnerUserID == nil:
		return fmt.Errorf("%w: either group_id or owner_user_id is required", ErrValidation)
	}
	return nil
}
