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
	ID              uint       `json:"id" validate:"required"`
	Title           *string    `json:"title" validate:"omitempty, min=1, max=255"`
	Description     *string    `json:"description" validate:"omitempty, max=1000"`
	DeadlineAt      *time.Time `json:"deadline_at" validate:"omitempty"`
	Category        *string    `json:"category" validate:"omitempty"`
	AttachmentIDs   *[]uint    `json:"attachment_ids"`
	CustomReminders *[]string  `json:"custom_reminders"`
}

type DeleteDeadlineRequest struct {
	ID uint `json:"id" validate:"required"`
}
