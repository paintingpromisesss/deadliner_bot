package service

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/paintingpromisesss/deadliner_bot/internal/models"
	"github.com/paintingpromisesss/deadliner_bot/internal/repository"
	"gorm.io/gorm"
)

var (
	// ErrDeadlineNil is returned when a nil deadline pointer is passed.
	ErrDeadlineNil = errors.New("deadline is nil")
	// ErrDeadlineRepoNil is returned when deadline repository dependency is missing.
	ErrDeadlineRepoNil = errors.New("deadline repository is nil")
	// ErrTxManagerNil is returned when transaction manager dependency is missing.
	ErrTxManagerNil = errors.New("transaction manager is nil")
	// ErrChatSettingsServiceNil is returned when chat settings service dependency is missing.
	ErrChatSettingsServiceNil = errors.New("chat settings service is nil")
	// ErrAttachmentServiceNil is returned when attachment service dependency is missing.
	ErrAttachmentServiceNil = errors.New("attachment service is nil")
	// ErrReminderServiceNil is returned when reminder service dependency is missing.
	ErrReminderServiceNil = errors.New("reminder service is nil")
	// ErrInvalidDeadlineID is returned when deadline ID is invalid.
	ErrInvalidDeadlineID = errors.New("invalid deadline id")
	// ErrInvalidChatID is returned when chat ID is invalid.
	ErrInvalidChatID = errors.New("invalid chat id")
	// ErrDeadlineNotFound is returned when deadline cannot be found.
	ErrDeadlineNotFound = errors.New("deadline not found")
	// ErrTitleRequired is returned when title is empty.
	ErrTitleRequired = errors.New("title is required")
	// ErrDeadlineAtRequired is returned when deadline datetime is missing.
	ErrDeadlineAtRequired = errors.New("deadline_at is required")
	// ErrDeadlineAtInPast is returned when deadline datetime is in past.
	ErrDeadlineAtInPast = errors.New("deadline_at must be in the future")
	// ErrCategoryRequired is returned when category is empty.
	ErrCategoryRequired = errors.New("category is required")
	// ErrInvalidCategory is returned when category has unsupported value.
	ErrInvalidCategory = errors.New("invalid category")
	// ErrCreatedByRequired is returned when created_by is empty.
	ErrCreatedByRequired = errors.New("created_by is required")
	// ErrTitleTooLong is returned when title exceeds max allowed length.
	ErrTitleTooLong = errors.New("title is too long")
	// ErrChatSettingsLookupFailed is returned when loading chat settings fails.
	ErrChatSettingsLookupFailed = errors.New("failed to get chat settings")
	// ErrCreateDeadlineFailed is returned when deadline creation fails.
	ErrCreateDeadlineFailed = errors.New("failed to create deadline")
	// ErrCalculateRemindersFailed is returned when reminder timestamps calculation fails.
	ErrCalculateRemindersFailed = errors.New("failed to calculate reminders")
	// ErrBuildRemindersFailed is returned when reminder models construction fails.
	ErrBuildRemindersFailed = errors.New("failed to build reminders")
	// ErrCreateRemindersFailed is returned when reminder persistence fails.
	ErrCreateRemindersFailed = errors.New("failed to create reminders")
	// ErrLinkAttachmentsFailed is returned when attachments linking fails.
	ErrLinkAttachmentsFailed = errors.New("failed to link attachments")
	// ErrListAttachmentsFailed is returned when loading linked attachments fails.
	ErrListAttachmentsFailed = errors.New("failed to list attachments")
)

const maxDeadlineTitleLength = 255

var allowedDeadlineCategories = []string{"Лабораторная работа", "Курсовая работа", "Домашняя работа", "Экзамен", "Другое"}

// DeadlineService handles business logic for deadlines.
type DeadlineService struct {
	repo                *repository.DeadlineRepository
	txManager           repository.TransactionManager
	chatSettingsService *ChatSettingsService
	attachmentService   *AttachmentService
	reminderService     *ReminderService
}

type CreateOptions struct {
	AttachmentIDs   []uint
	CustomReminders []string
}

// NewDeadlineService creates a new DeadlineService.
func NewDeadlineService(repo *repository.DeadlineRepository, txManager repository.TransactionManager, chatSettingsService *ChatSettingsService, attachmentService *AttachmentService, reminderService *ReminderService) (*DeadlineService, error) {
	if repo == nil {
		return nil, ErrDeadlineRepoNil
	}
	if txManager == nil {
		return nil, ErrTxManagerNil
	}
	if chatSettingsService == nil {
		return nil, ErrChatSettingsServiceNil
	}
	if attachmentService == nil {
		return nil, ErrAttachmentServiceNil
	}
	if reminderService == nil {
		return nil, ErrReminderServiceNil
	}

	return &DeadlineService{repo: repo, txManager: txManager, chatSettingsService: chatSettingsService, attachmentService: attachmentService, reminderService: reminderService}, nil
}

// Create validates and creates a new deadline.
func (s *DeadlineService) CreateDeadline(ctx context.Context, deadline *models.Deadline, opts CreateOptions) error {
	if err := s.validateDeadline(deadline); err != nil {
		return err
	}

	settings, err := s.chatSettingsService.GetByChatID(ctx, deadline.ChatID)
	if err != nil {
		if errors.Is(err, ErrChatSettingsNotFound) {
			return fmt.Errorf("%w: chat_id=%d", ErrChatSettingsNotFound, deadline.ChatID)
		}
		return fmt.Errorf("%w: %v", ErrChatSettingsLookupFailed, err)
	}

	var remindersString []string
	if len(opts.CustomReminders) > 0 {
		remindersString = opts.CustomReminders
	} else {
		remindersString = settings.DefaultReminders
	}

	return s.txManager.RunInTransaction(ctx, func(txCtx context.Context) error {
		if err := s.repo.Create(txCtx, deadline); err != nil {
			return fmt.Errorf("%w: %v", ErrCreateDeadlineFailed, err)
		}

		remindersTimes, err := CalculateReminders(deadline.DeadlineAt, remindersString)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrCalculateRemindersFailed, err)
		}

		reminders, err := BuildRemindersFromTimes(deadline.ID, deadline.ChatID, remindersTimes)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrBuildRemindersFailed, err)
		}
		if len(reminders) > 0 {
			if err := s.reminderService.CreateBatch(txCtx, reminders); err != nil {
				return fmt.Errorf("%w: %v", ErrCreateRemindersFailed, err)
			}
		}
		deadline.Reminders = reminders

		if len(opts.AttachmentIDs) > 0 {
			if err := s.attachmentService.LinkAttachmentsToDeadline(txCtx, deadline.ID, opts.AttachmentIDs); err != nil {
				return fmt.Errorf("%w: %v", ErrLinkAttachmentsFailed, err)
			}

			attachments, err := s.attachmentService.ListByDeadlineID(txCtx, deadline.ID)
			if err != nil {
				return fmt.Errorf("%w: deadline_id=%d: %v", ErrListAttachmentsFailed, deadline.ID, err)
			}
			deadline.Attachments = attachments
		}

		return nil
	})
}

// GetByID returns a deadline by ID.
func (s *DeadlineService) GetByID(ctx context.Context, id uint) (*models.Deadline, error) {
	if id == 0 {
		return nil, ErrInvalidDeadlineID
	}

	deadline, err := s.repo.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrDeadlineNotFound
		}
		return nil, err
	}

	return deadline, nil
}

// GetByIDAndChatID returns a chat-scoped deadline by ID.
func (s *DeadlineService) GetByIDAndChatID(ctx context.Context, id uint, chatID int64) (*models.Deadline, error) {
	if id == 0 {
		return nil, ErrInvalidDeadlineID
	}
	if chatID == 0 {
		return nil, ErrInvalidChatID
	}

	deadline, err := s.repo.GetByIDAndChatID(ctx, id, chatID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrDeadlineNotFound
		}
		return nil, err
	}

	return deadline, nil
}

// List returns all deadlines.
func (s *DeadlineService) List(ctx context.Context) ([]models.Deadline, error) {
	deadlines, err := s.repo.List(ctx)
	if err != nil {
		return nil, err
	}

	s.sortDeadlines(deadlines)
	return deadlines, nil
}

// ListByChatID returns all deadlines for a specific chat.
func (s *DeadlineService) ListByChatID(ctx context.Context, chatID int64) ([]models.Deadline, error) {
	if chatID == 0 {
		return nil, ErrInvalidChatID
	}

	deadlines, err := s.repo.ListByChatID(ctx, chatID)
	if err != nil {
		return nil, err
	}

	s.sortDeadlines(deadlines)
	return deadlines, nil
}

// Update validates and updates an existing deadline.
func (s *DeadlineService) Update(ctx context.Context, deadline *models.Deadline) error {
	if err := s.validateDeadline(deadline); err != nil {
		return err
	}
	if deadline.ID == 0 {
		return ErrInvalidDeadlineID
	}

	if _, err := s.repo.GetByIDAndChatID(ctx, deadline.ID, deadline.ChatID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrDeadlineNotFound
		}
		return err
	}

	return s.repo.Update(ctx, deadline)
}

// Delete removes a deadline by ID.
func (s *DeadlineService) Delete(ctx context.Context, id uint) error {
	if id == 0 {
		return ErrInvalidDeadlineID
	}

	if _, err := s.repo.GetByID(ctx, id); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrDeadlineNotFound
		}
		return err
	}

	return s.repo.Delete(ctx, id)
}

// DeleteByIDAndChatID removes a chat-scoped deadline.
func (s *DeadlineService) DeleteByIDAndChatID(ctx context.Context, id uint, chatID int64) error {
	if id == 0 {
		return ErrInvalidDeadlineID
	}
	if chatID == 0 {
		return ErrInvalidChatID
	}

	deadline, err := s.repo.GetByIDAndChatID(ctx, id, chatID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrDeadlineNotFound
		}
		return err
	}

	return s.repo.Delete(ctx, deadline.ID)
}

func (s *DeadlineService) validateDeadline(deadline *models.Deadline) error {
	if deadline == nil {
		return ErrDeadlineNil
	}

	deadline.Title = strings.TrimSpace(deadline.Title)
	deadline.Description = strings.TrimSpace(deadline.Description)
	deadline.Category = strings.TrimSpace(deadline.Category)

	if deadline.Title == "" {
		return ErrTitleRequired
	}
	if len(deadline.Title) > maxDeadlineTitleLength {
		return fmt.Errorf("%w: max %d", ErrTitleTooLong, maxDeadlineTitleLength)
	}

	if deadline.ChatID == 0 {
		return ErrInvalidChatID
	}

	if deadline.DeadlineAt.IsZero() {
		return ErrDeadlineAtRequired
	}
	if deadline.DeadlineAt.Before(time.Now()) {
		return ErrDeadlineAtInPast
	}

	if deadline.Category == "" {
		return ErrCategoryRequired
	}

	normalizedCategory, ok := normalizeCategory(deadline.Category)
	if !ok {
		return fmt.Errorf("%w: %s", ErrInvalidCategory, deadline.Category)
	}
	deadline.Category = normalizedCategory

	return nil
}

func normalizeCategory(category string) (string, bool) {
	for _, allowed := range allowedDeadlineCategories {
		if strings.EqualFold(category, allowed) {
			return allowed, true
		}
	}

	return "", false
}

func (s *DeadlineService) sortDeadlines(deadlines []models.Deadline) {
	slices.SortStableFunc(deadlines, func(a, b models.Deadline) int {
		switch {
		case a.DeadlineAt.Before(b.DeadlineAt):
			return -1
		case a.DeadlineAt.After(b.DeadlineAt):
			return 1
		default:
			return 0
		}
	})
}
