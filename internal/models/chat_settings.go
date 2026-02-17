package models

import (
	"time"
)

// ChatSettings stores per-chat configuration for bot behavior.
type ChatSettings struct {
	ID               uint     `json:"id" gorm:"primaryKey"`
	ChatID           int64    `json:"chat_id" gorm:"uniqueIndex"`
	DeadlineTopicID  int      `json:"deadline_topic_id"`
	TimeZone         string   `json:"time_zone"`
	DefaultReminders []string `json:"default_reminders" gorm:"type:jsonb;serializer:json"`
	UpdatedBy        int64    `json:"updated_by"`

	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	SetupNeeded bool      `json:"setup_needed"`
}
