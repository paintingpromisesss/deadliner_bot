package models

import "time"

// Reminder represents a scheduled notification for a deadline.
type Reminder struct {
	ID         uint      `json:"id" gorm:"primaryKey"`
	DeadlineID uint      `json:"deadline_id"`
	RemindAt   time.Time `json:"remind_at"`
	IsSent     bool      `json:"is_sent"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
