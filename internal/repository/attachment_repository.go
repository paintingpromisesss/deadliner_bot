package repository

import (
	"context"
	"fmt"

	"github.com/paintingpromisesss/deadliner_bot/internal/models"
	"gorm.io/gorm"
)

// AttachmentRepository provides CRUD access to attachments.
type AttachmentRepository struct {
	db *gorm.DB
}

// NewAttachmentRepository creates a new AttachmentRepository.
func NewAttachmentRepository(db *gorm.DB) (*AttachmentRepository, error) {
	if db == nil {
		return nil, fmt.Errorf("db is nil")
	}
	return &AttachmentRepository{db: db}, nil
}

// Create inserts a new attachment.
func (r *AttachmentRepository) Create(ctx context.Context, attachment *models.Attachment) error {
	if attachment == nil {
		return fmt.Errorf("attachment is nil")
	}
	return dbFromContext(ctx, r.db).WithContext(ctx).Create(attachment).Error
}

// GetByID returns an attachment by its ID.
func (r *AttachmentRepository) GetByID(ctx context.Context, id uint) (*models.Attachment, error) {
	var attachment models.Attachment
	if err := dbFromContext(ctx, r.db).WithContext(ctx).First(&attachment, id).Error; err != nil {
		return nil, err
	}
	return &attachment, nil
}

// GetByIDAndChatID returns an attachment by ID scoped to a chat.
func (r *AttachmentRepository) GetByIDAndChatID(ctx context.Context, id uint, chatID int64) (*models.Attachment, error) {
	var attachment models.Attachment
	if err := dbFromContext(ctx, r.db).WithContext(ctx).Where("id = ? AND chat_id = ?", id, chatID).First(&attachment).Error; err != nil {
		return nil, err
	}
	return &attachment, nil
}

// List returns all attachments.
func (r *AttachmentRepository) List(ctx context.Context) ([]models.Attachment, error) {
	var attachments []models.Attachment
	if err := dbFromContext(ctx, r.db).WithContext(ctx).Find(&attachments).Error; err != nil {
		return nil, err
	}
	return attachments, nil
}

// ListByDeadlineID returns all attachments for a specific deadline.
func (r *AttachmentRepository) ListByDeadlineID(ctx context.Context, deadlineID uint) ([]models.Attachment, error) {
	var attachments []models.Attachment
	if err := dbFromContext(ctx, r.db).WithContext(ctx).Where("deadline_id = ?", deadlineID).Find(&attachments).Error; err != nil {
		return nil, err
	}
	return attachments, nil
}

// ListByChatID returns all attachments for a specific chat.
func (r *AttachmentRepository) ListByChatID(ctx context.Context, chatID int64) ([]models.Attachment, error) {
	var attachments []models.Attachment
	if err := dbFromContext(ctx, r.db).WithContext(ctx).Where("chat_id = ?", chatID).Find(&attachments).Error; err != nil {
		return nil, err
	}
	return attachments, nil
}

// Update saves attachment changes.
func (r *AttachmentRepository) Update(ctx context.Context, attachment *models.Attachment) error {
	if attachment == nil {
		return fmt.Errorf("attachment is nil")
	}
	return dbFromContext(ctx, r.db).WithContext(ctx).Save(attachment).Error
}

// Delete removes an attachment by ID.
func (r *AttachmentRepository) Delete(ctx context.Context, id uint) error {
	return dbFromContext(ctx, r.db).WithContext(ctx).Delete(&models.Attachment{}, id).Error
}

// LinkToDeadline updates deadline_id for the provided attachment IDs.
func (r *AttachmentRepository) LinkToDeadline(ctx context.Context, deadlineID uint, attachmentIDs []uint) error {
	if deadlineID == 0 {
		return fmt.Errorf("deadline id is invalid")
	}
	if len(attachmentIDs) == 0 {
		return fmt.Errorf("attachment ids are empty")
	}

	return dbFromContext(ctx, r.db).WithContext(ctx).
		Model(&models.Attachment{}).
		Where("id IN ?", attachmentIDs).
		Update("deadline_id", deadlineID).Error
}
