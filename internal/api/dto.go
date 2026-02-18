package api

import "time"

type CreateDeadlineRequest struct {
	Title           string    `json:"title" validate:"required, min=1, max=255"`
	Description     string    `json:"description" validate:"max=1000"`
	DeadlineAt      time.Time `json:"deadline_at" validate:"required"`
	Category        string    `json:"category" validate:"required"`
	AttachmentIDs   []uint    `json:"attachment_ids"`
	CustomReminders []string  `json:"custom_reminders"`
}

type UpdateDeadlineRequest struct {
	Title           *string    `json:"title" validate:"omitempty, min=1, max=255"`
	Description     *string    `json:"description" validate:"omitempty, max=1000"`
	DeadlineAt      *time.Time `json:"deadline_at" validate:"omitempty"`
	Status          *string    `json:"status" validate:"omitempty"`
	Category        *string    `json:"category" validate:"omitempty"`
	AttachmentIDs   *[]uint    `json:"attachment_ids"`
	CustomReminders *[]string  `json:"custom_reminders"`
}

type ListDeadlinesRequest struct {
	Page     int    `query:"page" validate:"min=1"`
	PageSize int    `query:"page_size" validate:"min=1, max=100"`
	Category string `query:"category"`
	From     string `query:"from" validate:"omitempty,datetime=2006-01-02T15:04:05Z07:00"`
	To       string `query:"to" validate:"omitempty,datetime=2006-01-02T15:04:05Z07:00"`
}

type UpdateChatSettingsRequest struct {
	TimeZone         *string   `json:"time_zone" validate:"omitempty,timezone"`
	DefaultReminders *[]string `json:"default_reminders" validate:"omitempty,dive,required"`
}
