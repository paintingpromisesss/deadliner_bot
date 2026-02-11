package repository

import (
	"context"
	"fmt"

	"github.com/paintingpromisesss/deadliner_bot/internal/models"
	"gorm.io/gorm"
)

// ReminderRepository provides CRUD access to reminders.
type ReminderRepository struct {
	db *gorm.DB
}

// NewReminderRepository creates a new ReminderRepository.
func NewReminderRepository(db *gorm.DB) (*ReminderRepository, error) {
	if db == nil {
		return nil, fmt.Errorf("db is nil")
	}
	return &ReminderRepository{db: db}, nil
}

// Create inserts a new reminder.
func (r *ReminderRepository) Create(ctx context.Context, reminder *models.Reminder) error {
	if reminder == nil {
		return fmt.Errorf("reminder is nil")
	}
	return r.db.WithContext(ctx).Create(reminder).Error
}

// GetByID returns a reminder by its ID.
func (r *ReminderRepository) GetByID(ctx context.Context, id uint) (*models.Reminder, error) {
	var reminder models.Reminder
	if err := r.db.WithContext(ctx).First(&reminder, id).Error; err != nil {
		return nil, err
	}
	return &reminder, nil
}

// List returns all reminders.
func (r *ReminderRepository) List(ctx context.Context) ([]models.Reminder, error) {
	var reminders []models.Reminder
	if err := r.db.WithContext(ctx).Find(&reminders).Error; err != nil {
		return nil, err
	}
	return reminders, nil
}

// Update saves reminder changes.
func (r *ReminderRepository) Update(ctx context.Context, reminder *models.Reminder) error {
	if reminder == nil {
		return fmt.Errorf("reminder is nil")
	}
	return r.db.WithContext(ctx).Save(reminder).Error
}

// Delete removes a reminder by ID.
func (r *ReminderRepository) Delete(ctx context.Context, id uint) error {
	return r.db.WithContext(ctx).Delete(&models.Reminder{}, id).Error
}
