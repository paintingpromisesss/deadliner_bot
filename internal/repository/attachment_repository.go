package repository

import (
	"context"
	"fmt"

	"github.com/paintingpromisesss/deadliner_bot/internal/models"
	"gorm.io/gorm"
)

type AttachmentRepository interface {
	Create(ctx context.Context, attachment *models.Attachment) error
	GetByID(ctx context.Context, id uint) (*models.Attachment, error)
	GetByIDAndChatID(ctx context.Context, id uint, chatID int64) (*models.Attachment, error)
	List(ctx context.Context) ([]models.Attachment, error)
	ListByDeadlineID(ctx context.Context, deadlineID uint) ([]models.Attachment, error)
	ListByChatID(ctx context.Context, chatID int64) ([]models.Attachment, error)
	Update(ctx context.Context, attachment *models.Attachment) error
	Delete(ctx context.Context, id uint) error
	LinkToDeadline(ctx context.Context, deadlineID uint, attachmentIDs []uint) error
}

var _ AttachmentRepository = (*attachmentRepository)(nil)

// attachmentRepository is a GORM-backed AttachmentRepository implementation.
type attachmentRepository struct {
	db *gorm.DB
}

// NewAttachmentRepository creates a new AttachmentRepository.
func NewAttachmentRepository(db *gorm.DB) (AttachmentRepository, error) {
	if db == nil {
		return nil, fmt.Errorf("db is nil")
	}
	return &attachmentRepository{db: db}, nil
}

// Create inserts a new attachment.
func (r *attachmentRepository) Create(ctx context.Context, attachment *models.Attachment) error {
	if attachment == nil {
		return fmt.Errorf("attachment is nil")
	}
	return dbFromContext(ctx, r.db).WithContext(ctx).Create(attachment).Error
}

// GetByID returns an attachment by its ID.
func (r *attachmentRepository) GetByID(ctx context.Context, id uint) (*models.Attachment, error) {
	var attachment models.Attachment
	if err := dbFromContext(ctx, r.db).WithContext(ctx).First(&attachment, id).Error; err != nil {
		return nil, err
	}
	return &attachment, nil
}

// GetByIDAndChatID returns an attachment by ID scoped to a chat.
func (r *attachmentRepository) GetByIDAndChatID(ctx context.Context, id uint, chatID int64) (*models.Attachment, error) {
	var attachment models.Attachment
	if err := dbFromContext(ctx, r.db).WithContext(ctx).Where("id = ? AND chat_id = ?", id, chatID).First(&attachment).Error; err != nil {
		return nil, err
	}
	return &attachment, nil
}

// List returns all attachments.
func (r *attachmentRepository) List(ctx context.Context) ([]models.Attachment, error) {
	var attachments []models.Attachment
	if err := dbFromContext(ctx, r.db).WithContext(ctx).Find(&attachments).Error; err != nil {
		return nil, err
	}
	return attachments, nil
}

// ListByDeadlineID returns all attachments for a specific deadline.
func (r *attachmentRepository) ListByDeadlineID(ctx context.Context, deadlineID uint) ([]models.Attachment, error) {
	var attachments []models.Attachment
	if err := dbFromContext(ctx, r.db).WithContext(ctx).Where("deadline_id = ?", deadlineID).Find(&attachments).Error; err != nil {
		return nil, err
	}
	return attachments, nil
}

// ListByChatID returns all attachments for a specific chat.
func (r *attachmentRepository) ListByChatID(ctx context.Context, chatID int64) ([]models.Attachment, error) {
	var attachments []models.Attachment
	if err := dbFromContext(ctx, r.db).WithContext(ctx).Where("chat_id = ?", chatID).Find(&attachments).Error; err != nil {
		return nil, err
	}
	return attachments, nil
}

// Update saves attachment changes.
func (r *attachmentRepository) Update(ctx context.Context, attachment *models.Attachment) error {
	if attachment == nil {
		return fmt.Errorf("attachment is nil")
	}

	result := dbFromContext(ctx, r.db).WithContext(ctx).
		Model(&models.Attachment{}).
		Where("id = ? AND chat_id = ?", attachment.ID, attachment.ChatID).
		Updates(map[string]interface{}{
			"deadline_id": attachment.DeadlineID,
			"file_id":     attachment.FileID,
			"file_name":   attachment.FileName,
			"file_type":   attachment.FileType,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}

	return nil
}

// Delete removes an attachment by ID.
func (r *attachmentRepository) Delete(ctx context.Context, id uint) error {
	return dbFromContext(ctx, r.db).WithContext(ctx).Delete(&models.Attachment{}, id).Error
}

// LinkToDeadline updates deadline_id for the provided attachment IDs.
func (r *attachmentRepository) LinkToDeadline(ctx context.Context, deadlineID uint, attachmentIDs []uint) error {
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
