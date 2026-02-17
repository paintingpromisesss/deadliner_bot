package service

import (
	"context"
	"errors"
	"strings"

	"github.com/paintingpromisesss/deadliner_bot/internal/models"
	"github.com/paintingpromisesss/deadliner_bot/internal/repository"
	"gorm.io/gorm"
)

var (
	ErrAttachmentNil       = errors.New("attachment is nil")
	ErrAttachmentRepoNil   = errors.New("attachment repository is nil")
	ErrInvalidAttachmentID = errors.New("invalid attachment id")
	ErrAttachmentNotFound  = errors.New("attachment not found")
	ErrFileIDRequired      = errors.New("file id is required")
)

type AttachmentService struct {
	repo *repository.AttachmentRepository
}

func NewAttachmentService(repo *repository.AttachmentRepository) (*AttachmentService, error) {
	if repo == nil {
		return nil, ErrAttachmentRepoNil
	}
	return &AttachmentService{repo: repo}, nil
}

// Create validates and creates a new attachment.
func (s *AttachmentService) Create(ctx context.Context, attachment *models.Attachment) error {
	if err := validateAttachment(attachment); err != nil {
		return err
	}

	return s.repo.Create(ctx, attachment)
}

// GetByID returns an attachment by ID.
func (s *AttachmentService) GetByID(ctx context.Context, id uint) (*models.Attachment, error) {
	if id == 0 {
		return nil, ErrInvalidAttachmentID
	}

	attachment, err := s.repo.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrAttachmentNotFound
		}
		return nil, err
	}

	return attachment, nil
}

// GetByIDAndChatID returns a chat-scoped attachment by ID.
func (s *AttachmentService) GetByIDAndChatID(ctx context.Context, id uint, chatID int64) (*models.Attachment, error) {
	if id == 0 {
		return nil, ErrInvalidAttachmentID
	}
	if chatID == 0 {
		return nil, ErrInvalidChatID
	}

	attachment, err := s.repo.GetByIDAndChatID(ctx, id, chatID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrAttachmentNotFound
		}
		return nil, err
	}

	return attachment, nil
}

// List returns all attachments.
func (s *AttachmentService) List(ctx context.Context) ([]models.Attachment, error) {
	return s.repo.List(ctx)
}

// ListByDeadlineID returns all attachments for a specific deadline.
func (s *AttachmentService) ListByDeadlineID(ctx context.Context, deadlineID uint) ([]models.Attachment, error) {
	if deadlineID == 0 {
		return nil, ErrInvalidDeadlineID
	}
	return s.repo.ListByDeadlineID(ctx, deadlineID)
}

// ListByChatID returns all attachments for a specific chat.
func (s *AttachmentService) ListByChatID(ctx context.Context, chatID int64) ([]models.Attachment, error) {
	if chatID == 0 {
		return nil, ErrInvalidChatID
	}

	return s.repo.ListByChatID(ctx, chatID)
}

// Update validates and updates an existing attachment.
func (s *AttachmentService) Update(ctx context.Context, attachment *models.Attachment) error {
	if err := validateAttachment(attachment); err != nil {
		return err
	}
	if attachment.ID == 0 {
		return ErrInvalidAttachmentID
	}

	if _, err := s.repo.GetByIDAndChatID(ctx, attachment.ID, attachment.ChatID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrAttachmentNotFound
		}
		return err
	}

	return s.repo.Update(ctx, attachment)
}

// Delete removes an attachment by ID.
func (s *AttachmentService) Delete(ctx context.Context, id uint) error {
	if id == 0 {
		return ErrInvalidAttachmentID
	}

	if _, err := s.repo.GetByID(ctx, id); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrAttachmentNotFound
		}
		return err
	}

	return s.repo.Delete(ctx, id)
}

// DeleteByIDAndChatID removes a chat-scoped attachment.
func (s *AttachmentService) DeleteByIDAndChatID(ctx context.Context, id uint, chatID int64) error {
	if id == 0 {
		return ErrInvalidAttachmentID
	}
	if chatID == 0 {
		return ErrInvalidChatID
	}

	attachment, err := s.repo.GetByIDAndChatID(ctx, id, chatID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrAttachmentNotFound
		}
		return err
	}

	return s.repo.Delete(ctx, attachment.ID)
}

// LinkToDeadline links provided attachments to a deadline by updating deadline_id in DB.
func (s *AttachmentService) LinkAttachmentsToDeadline(ctx context.Context, deadlineID uint, attachmentIDs []uint) error {
	if deadlineID == 0 {
		return ErrInvalidDeadlineID
	}
	if len(attachmentIDs) == 0 {
		return nil
	}

	for _, id := range attachmentIDs {
		if id == 0 {
			return ErrInvalidAttachmentID
		}
	}

	return s.repo.LinkToDeadline(ctx, deadlineID, attachmentIDs)
}

func validateAttachment(attachment *models.Attachment) error {
	if attachment == nil {
		return ErrAttachmentNil
	}

	if attachment.ChatID == 0 {
		return ErrInvalidChatID
	}

	attachment.FileID = strings.TrimSpace(attachment.FileID)
	attachment.FileName = strings.TrimSpace(attachment.FileName)
	attachment.FileType = strings.TrimSpace(attachment.FileType)

	if attachment.FileID == "" {
		return ErrFileIDRequired
	}

	return nil
}
