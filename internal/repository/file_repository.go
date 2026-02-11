package repository

import (
	"context"
	"fmt"

	"github.com/paintingpromisesss/deadliner_bot/internal/models"
	"gorm.io/gorm"
)

// FileRepository provides CRUD access to attachments.
type FileRepository struct {
	db *gorm.DB
}

// NewFileRepository creates a new FileRepository.
func NewFileRepository(db *gorm.DB) (*FileRepository, error) {
	if db == nil {
		return nil, fmt.Errorf("db is nil")
	}
	return &FileRepository{db: db}, nil
}

// Create inserts a new attachment.
func (r *FileRepository) Create(ctx context.Context, attachment *models.Attachment) error {
	if attachment == nil {
		return fmt.Errorf("attachment is nil")
	}
	return r.db.WithContext(ctx).Create(attachment).Error
}

// GetByID returns an attachment by its ID.
func (r *FileRepository) GetByID(ctx context.Context, id uint) (*models.Attachment, error) {
	var attachment models.Attachment
	if err := r.db.WithContext(ctx).First(&attachment, id).Error; err != nil {
		return nil, err
	}
	return &attachment, nil
}

// GetByIDAndChatID returns an attachment by ID scoped to a chat.
func (r *FileRepository) GetByIDAndChatID(ctx context.Context, id uint, chatID int64) (*models.Attachment, error) {
	var attachment models.Attachment
	if err := r.db.WithContext(ctx).Where("id = ? AND chat_id = ?", id, chatID).First(&attachment).Error; err != nil {
		return nil, err
	}
	return &attachment, nil
}

// List returns all attachments.
func (r *FileRepository) List(ctx context.Context) ([]models.Attachment, error) {
	var attachments []models.Attachment
	if err := r.db.WithContext(ctx).Find(&attachments).Error; err != nil {
		return nil, err
	}
	return attachments, nil
}

// ListByChatID returns all attachments for a specific chat.
func (r *FileRepository) ListByChatID(ctx context.Context, chatID int64) ([]models.Attachment, error) {
	var attachments []models.Attachment
	if err := r.db.WithContext(ctx).Where("chat_id = ?", chatID).Find(&attachments).Error; err != nil {
		return nil, err
	}
	return attachments, nil
}

// Update saves attachment changes.
func (r *FileRepository) Update(ctx context.Context, attachment *models.Attachment) error {
	if attachment == nil {
		return fmt.Errorf("attachment is nil")
	}
	return r.db.WithContext(ctx).Save(attachment).Error
}

// Delete removes an attachment by ID.
func (r *FileRepository) Delete(ctx context.Context, id uint) error {
	return r.db.WithContext(ctx).Delete(&models.Attachment{}, id).Error
}
