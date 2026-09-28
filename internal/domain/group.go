package domain

import "time"

type GroupStatus string

const (
	GroupStatusPending  GroupStatus = "pending"
	GroupStatusActive   GroupStatus = "active"
	GroupStatusArchived GroupStatus = "archived"
)

type Role string

const (
	RoleAdmin  Role = "admin"
	RoleMember Role = "member"
)

type Group struct {
	ID             int64
	Slug           string
	SlugNorm       string
	Title          string
	Status         GroupStatus
	Official       bool
	CreatedBy      int64
	DefaultPresets []time.Duration
	ClaimExpiresAt *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
	DeletedAt      *time.Time
}

type ChatBinding struct {
	ID              int64
	GroupID         int64
	ChatID          int64
	MessageThreadID *int64
	ChatTitle       string
	BoundBy         int64
	BoundAt         time.Time
}

type Membership struct {
	GroupID  int64
	UserID   int64
	Role     Role
	DMNotify *bool
	JoinedAt time.Time
}

// MembershipDetail — membership с именными данными пользователя (JOIN),
// для списков участников без N+1 чтения users.
type MembershipDetail struct {
	Membership
	Username  string
	FirstName string
}
