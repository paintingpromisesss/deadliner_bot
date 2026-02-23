package repository

import (
	"context"
	"fmt"

	"github.com/paintingpromisesss/deadliner_bot/internal/models"
	"gorm.io/gorm"
)

type ReminderRepository interface {
	Create(ctx context.Context, reminder *models.Reminder) error
	CreateBatch(ctx context.Context, reminders []models.Reminder) error
	GetByID(ctx context.Context, id uint) (*models.Reminder, error)
	GetByIDAndChatID(ctx context.Context, id uint, chatID int64) (*models.Reminder, error)
	List(ctx context.Context) ([]models.Reminder, error)
	ListByChatID(ctx context.Context, chatID int64) ([]models.Reminder, error)
	DeleteByDeadlineIDAndChatID(ctx context.Context, deadlineID uint, chatID int64) error
	Update(ctx context.Context, reminder *models.Reminder) error
	Delete(ctx context.Context, id uint) error
}

var _ ReminderRepository = (*reminderRepository)(nil)

// reminderRepository is a GORM-backed ReminderRepository implementation.
type reminderRepository struct {
	db *gorm.DB
}

// NewReminderRepository creates a new ReminderRepository.
func NewReminderRepository(db *gorm.DB) (ReminderRepository, error) {
	if db == nil {
		return nil, fmt.Errorf("db is nil")
	}
	return &reminderRepository{db: db}, nil
}

// Create inserts a new reminder.
func (r *reminderRepository) Create(ctx context.Context, reminder *models.Reminder) error {
	if reminder == nil {
		return fmt.Errorf("reminder is nil")
	}
	return dbFromContext(ctx, r.db).WithContext(ctx).Create(reminder).Error
}

// CreateBatch inserts reminders using database from context transaction if present.
func (r *reminderRepository) CreateBatch(ctx context.Context, reminders []models.Reminder) error {
	if len(reminders) == 0 {
		return nil
	}

	return dbFromContext(ctx, r.db).WithContext(ctx).Create(&reminders).Error
}

// GetByID returns a reminder by its ID.
func (r *reminderRepository) GetByID(ctx context.Context, id uint) (*models.Reminder, error) {
	var reminder models.Reminder
	if err := dbFromContext(ctx, r.db).WithContext(ctx).First(&reminder, id).Error; err != nil {
		return nil, err
	}
	return &reminder, nil
}

// GetByIDAndChatID returns a reminder by ID scoped to a chat.
func (r *reminderRepository) GetByIDAndChatID(ctx context.Context, id uint, chatID int64) (*models.Reminder, error) {
	var reminder models.Reminder
	if err := dbFromContext(ctx, r.db).WithContext(ctx).Where("id = ? AND chat_id = ?", id, chatID).First(&reminder).Error; err != nil {
		return nil, err
	}
	return &reminder, nil
}

// List returns all reminders.
func (r *reminderRepository) List(ctx context.Context) ([]models.Reminder, error) {
	var reminders []models.Reminder
	if err := dbFromContext(ctx, r.db).WithContext(ctx).Find(&reminders).Error; err != nil {
		return nil, err
	}
	return reminders, nil
}

// ListByChatID returns all reminders for a specific chat.
func (r *reminderRepository) ListByChatID(ctx context.Context, chatID int64) ([]models.Reminder, error) {
	var reminders []models.Reminder
	if err := dbFromContext(ctx, r.db).WithContext(ctx).Where("chat_id = ?", chatID).Find(&reminders).Error; err != nil {
		return nil, err
	}
	return reminders, nil
}

// DeleteByDeadlineIDAndChatID removes reminders by deadline scoped to chat.
func (r *reminderRepository) DeleteByDeadlineIDAndChatID(ctx context.Context, deadlineID uint, chatID int64) error {
	return dbFromContext(ctx, r.db).WithContext(ctx).
		Where("deadline_id = ? AND chat_id = ?", deadlineID, chatID).
		Delete(&models.Reminder{}).Error
}

// Update saves reminder changes.
func (r *reminderRepository) Update(ctx context.Context, reminder *models.Reminder) error {
	if reminder == nil {
		return fmt.Errorf("reminder is nil")
	}

	result := dbFromContext(ctx, r.db).WithContext(ctx).
		Model(&models.Reminder{}).
		Where("id = ? AND chat_id = ?", reminder.ID, reminder.ChatID).
		Updates(map[string]interface{}{
			"deadline_id": reminder.DeadlineID,
			"remind_at":   reminder.RemindAt,
			"is_sent":     reminder.IsSent,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}

	return nil
}

// Delete removes a reminder by ID.
func (r *reminderRepository) Delete(ctx context.Context, id uint) error {
	return dbFromContext(ctx, r.db).WithContext(ctx).Delete(&models.Reminder{}, id).Error
}
