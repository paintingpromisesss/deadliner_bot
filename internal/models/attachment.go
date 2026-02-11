package models

import "time"

// Attachment represents a file linked to a deadline.
type Attachment struct {
	ID         uint   `json:"id" gorm:"primaryKey"`
	DeadlineID uint   `json:"deadline_id"`
	ChatID     int64  `json:"chat_id" gorm:"index"`
	FileID     string `json:"file_id"`
	FileName   string `json:"file_name"`
	FileType   string `json:"file_type"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
