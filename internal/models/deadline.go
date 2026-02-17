package models

import "time"

// Deadline represents a single study deadline entry.
type Deadline struct {
	ID          uint      `json:"id" gorm:"primaryKey"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	DeadlineAt  time.Time `json:"deadline_at"`
	Category    string    `json:"category"`
	ChatID      int64     `json:"chat_id"`
	CreatedBy   int64     `json:"created_by"`

	Attachments []Attachment `json:"attachments" gorm:"constraint:OnDelete:CASCADE"`
	Reminders   []Reminder   `json:"reminders" gorm:"constraint:OnDelete:CASCADE"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
