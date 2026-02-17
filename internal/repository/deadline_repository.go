package repository

import (
	"context"
	"fmt"

	"github.com/paintingpromisesss/deadliner_bot/internal/models"
	"gorm.io/gorm"
)

// DeadlineRepository provides CRUD access to deadlines.
type DeadlineRepository struct {
	db *gorm.DB
}

// NewDeadlineRepository creates a new DeadlineRepository.
func NewDeadlineRepository(db *gorm.DB) (*DeadlineRepository, error) {
	if db == nil {
		return nil, fmt.Errorf("db is nil")
	}
	return &DeadlineRepository{db: db}, nil
}

// Create inserts a new deadline.
func (r *DeadlineRepository) Create(ctx context.Context, deadline *models.Deadline) error {
	if deadline == nil {
		return fmt.Errorf("deadline is nil")
	}
	return dbFromContext(ctx, r.db).WithContext(ctx).Create(deadline).Error
}

// GetByID returns a deadline by its ID.
func (r *DeadlineRepository) GetByID(ctx context.Context, id uint) (*models.Deadline, error) {
	var deadline models.Deadline
	if err := dbFromContext(ctx, r.db).WithContext(ctx).First(&deadline, id).Error; err != nil {
		return nil, err
	}
	return &deadline, nil
}

// GetByIDAndChatID returns a deadline by its ID scoped to a chat.
func (r *DeadlineRepository) GetByIDAndChatID(ctx context.Context, id uint, chatID int64) (*models.Deadline, error) {
	var deadline models.Deadline
	if err := dbFromContext(ctx, r.db).WithContext(ctx).Where("id = ? AND chat_id = ?", id, chatID).First(&deadline).Error; err != nil {
		return nil, err
	}
	return &deadline, nil
}

// List returns all deadlines.
func (r *DeadlineRepository) List(ctx context.Context) ([]models.Deadline, error) {
	var deadlines []models.Deadline
	if err := dbFromContext(ctx, r.db).WithContext(ctx).Find(&deadlines).Error; err != nil {
		return nil, err
	}
	return deadlines, nil
}

// ListByChatID returns all deadlines for a specific chat.
func (r *DeadlineRepository) ListByChatID(ctx context.Context, chatID int64) ([]models.Deadline, error) {
	var deadlines []models.Deadline
	if err := dbFromContext(ctx, r.db).WithContext(ctx).Where("chat_id = ?", chatID).Find(&deadlines).Error; err != nil {
		return nil, err
	}
	return deadlines, nil
}

// Update saves deadline changes.
func (r *DeadlineRepository) Update(ctx context.Context, deadline *models.Deadline) error {
	if deadline == nil {
		return fmt.Errorf("deadline is nil")
	}
	return dbFromContext(ctx, r.db).WithContext(ctx).Save(deadline).Error
}

// Delete removes a deadline by ID.
func (r *DeadlineRepository) Delete(ctx context.Context, id uint) error {
	return dbFromContext(ctx, r.db).WithContext(ctx).Delete(&models.Deadline{}, id).Error
}
