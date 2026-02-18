package repository

import (
	"context"
	"fmt"

	"github.com/paintingpromisesss/deadliner_bot/internal/models"
	"gorm.io/gorm"
)

type DeadlineRepository interface {
	Create(ctx context.Context, deadline *models.Deadline) error
	GetByID(ctx context.Context, id uint) (*models.Deadline, error)
	GetByIDAndChatID(ctx context.Context, id uint, chatID int64) (*models.Deadline, error)
	List(ctx context.Context) ([]models.Deadline, error)
	ListByChatID(ctx context.Context, chatID int64) ([]models.Deadline, error)
	Update(ctx context.Context, deadline *models.Deadline) error
	Delete(ctx context.Context, id uint) error
}

var _ DeadlineRepository = (*deadlineRepository)(nil)

// deadlineRepository is a GORM-backed DeadlineRepository implementation.
type deadlineRepository struct {
	db *gorm.DB
}

// NewDeadlineRepository creates a new DeadlineRepository.
func NewDeadlineRepository(db *gorm.DB) (DeadlineRepository, error) {
	if db == nil {
		return nil, fmt.Errorf("db is nil")
	}
	return &deadlineRepository{db: db}, nil
}

// Create inserts a new deadline.
func (r *deadlineRepository) Create(ctx context.Context, deadline *models.Deadline) error {
	if deadline == nil {
		return fmt.Errorf("deadline is nil")
	}
	return dbFromContext(ctx, r.db).WithContext(ctx).Create(deadline).Error
}

// GetByID returns a deadline by its ID.
func (r *deadlineRepository) GetByID(ctx context.Context, id uint) (*models.Deadline, error) {
	var deadline models.Deadline
	if err := dbFromContext(ctx, r.db).WithContext(ctx).First(&deadline, id).Error; err != nil {
		return nil, err
	}
	return &deadline, nil
}

// GetByIDAndChatID returns a deadline by its ID scoped to a chat.
func (r *deadlineRepository) GetByIDAndChatID(ctx context.Context, id uint, chatID int64) (*models.Deadline, error) {
	var deadline models.Deadline
	if err := dbFromContext(ctx, r.db).WithContext(ctx).Where("id = ? AND chat_id = ?", id, chatID).First(&deadline).Error; err != nil {
		return nil, err
	}
	return &deadline, nil
}

// List returns all deadlines.
func (r *deadlineRepository) List(ctx context.Context) ([]models.Deadline, error) {
	var deadlines []models.Deadline
	if err := dbFromContext(ctx, r.db).WithContext(ctx).Find(&deadlines).Error; err != nil {
		return nil, err
	}
	return deadlines, nil
}

// ListByChatID returns all deadlines for a specific chat.
func (r *deadlineRepository) ListByChatID(ctx context.Context, chatID int64) ([]models.Deadline, error) {
	var deadlines []models.Deadline
	if err := dbFromContext(ctx, r.db).WithContext(ctx).Where("chat_id = ?", chatID).Find(&deadlines).Error; err != nil {
		return nil, err
	}
	return deadlines, nil
}

// Update saves deadline changes.
func (r *deadlineRepository) Update(ctx context.Context, deadline *models.Deadline) error {
	if deadline == nil {
		return fmt.Errorf("deadline is nil")
	}

	result := dbFromContext(ctx, r.db).WithContext(ctx).
		Model(&models.Deadline{}).
		Where("id = ? AND chat_id = ?", deadline.ID, deadline.ChatID).
		Updates(map[string]interface{}{
			"title":       deadline.Title,
			"description": deadline.Description,
			"deadline_at": deadline.DeadlineAt,
			"category":    deadline.Category,
			"status":      deadline.Status,
			"created_by":  deadline.CreatedBy,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}

	return nil
}

// Delete removes a deadline by ID.
func (r *deadlineRepository) Delete(ctx context.Context, id uint) error {
	return dbFromContext(ctx, r.db).WithContext(ctx).Delete(&models.Deadline{}, id).Error
}
