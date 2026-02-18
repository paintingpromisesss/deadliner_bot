package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/paintingpromisesss/deadliner_bot/internal/models"
	"github.com/paintingpromisesss/deadliner_bot/internal/repository"
	"gorm.io/gorm"
)

var (
	ErrReminderNil              = errors.New("reminder is nil")
	ErrReminderRepoNil          = errors.New("reminder repository is nil")
	ErrInvalidReminderID        = errors.New("invalid reminder id")
	ErrReminderNotFound         = errors.New("reminder not found")
	ErrInvalidReminderBatchItem = errors.New("invalid reminder in batch")
	ErrInvalidReminderDuration  = errors.New("invalid reminder duration")
	ErrNoReminderTimesProvided  = errors.New("no reminder times provided")
	ErrRemindAtRequired         = errors.New("remind_at is required")
)

type ReminderService struct {
	repo repository.ReminderRepository
}

func NewReminderService(repo repository.ReminderRepository) (*ReminderService, error) {
	if repo == nil {
		return nil, ErrReminderRepoNil
	}
	return &ReminderService{repo: repo}, nil
}

// Create validates and creates a new reminder.
func (s *ReminderService) Create(ctx context.Context, reminder *models.Reminder) error {
	if err := validateReminder(reminder); err != nil {
		return err
	}

	return s.repo.Create(ctx, reminder)
}

// CreateBatch validates and creates reminders.
func (s *ReminderService) CreateBatch(ctx context.Context, reminders []models.Reminder) error {
	if len(reminders) == 0 {
		return nil
	}

	for i := range reminders {
		if err := validateReminder(&reminders[i]); err != nil {
			return fmt.Errorf("%w: index=%d: %v", ErrInvalidReminderBatchItem, i, err)
		}
	}

	return s.repo.CreateBatch(ctx, reminders)
}

// GetByID returns a reminder by ID.
func (s *ReminderService) GetByID(ctx context.Context, id uint) (*models.Reminder, error) {
	if id == 0 {
		return nil, ErrInvalidReminderID
	}

	reminder, err := s.repo.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrReminderNotFound
		}
		return nil, err
	}

	return reminder, nil
}

// GetByIDAndChatID returns a chat-scoped reminder by ID.
func (s *ReminderService) GetByIDAndChatID(ctx context.Context, id uint, chatID int64) (*models.Reminder, error) {
	if id == 0 {
		return nil, ErrInvalidReminderID
	}
	if chatID == 0 {
		return nil, ErrInvalidChatID
	}

	reminder, err := s.repo.GetByIDAndChatID(ctx, id, chatID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrReminderNotFound
		}
		return nil, err
	}

	return reminder, nil
}

// List returns all reminders.
func (s *ReminderService) List(ctx context.Context) ([]models.Reminder, error) {
	return s.repo.List(ctx)
}

// ListByChatID returns all reminders for a specific chat.
func (s *ReminderService) ListByChatID(ctx context.Context, chatID int64) ([]models.Reminder, error) {
	if chatID == 0 {
		return nil, ErrInvalidChatID
	}

	return s.repo.ListByChatID(ctx, chatID)
}

// Update validates and updates an existing reminder.
func (s *ReminderService) Update(ctx context.Context, reminder *models.Reminder) error {
	if err := validateReminder(reminder); err != nil {
		return err
	}
	if reminder.ID == 0 {
		return ErrInvalidReminderID
	}

	if err := s.repo.Update(ctx, reminder); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrReminderNotFound
		}
		return err
	}

	return nil
}

// Delete removes a reminder by ID.
func (s *ReminderService) Delete(ctx context.Context, id uint) error {
	if id == 0 {
		return ErrInvalidReminderID
	}

	if _, err := s.repo.GetByID(ctx, id); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrReminderNotFound
		}
		return err
	}

	return s.repo.Delete(ctx, id)
}

// DeleteByIDAndChatID removes a chat-scoped reminder.
func (s *ReminderService) DeleteByIDAndChatID(ctx context.Context, id uint, chatID int64) error {
	if id == 0 {
		return ErrInvalidReminderID
	}
	if chatID == 0 {
		return ErrInvalidChatID
	}

	reminder, err := s.repo.GetByIDAndChatID(ctx, id, chatID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrReminderNotFound
		}
		return err
	}

	return s.repo.Delete(ctx, reminder.ID)
}

// CalculateReminders returns reminder times based on base time minus each duration string.
func CalculateReminders(base time.Time, durations []string) ([]time.Time, error) {
	reminders := make([]time.Time, 0, len(durations))

	for _, durationText := range durations {
		duration, err := time.ParseDuration(durationText)
		if err != nil {
			return nil, fmt.Errorf("%w: %q: %v", ErrInvalidReminderDuration, durationText, err)
		}

		reminders = append(reminders, base.Add(-duration))
	}

	return reminders, nil
}

// BuildRemindersFromTimes converts reminder times into reminder models.
func BuildRemindersFromTimes(deadlineID uint, chatID int64, reminderTimes []time.Time) ([]models.Reminder, error) {
	reminders := make([]models.Reminder, 0, len(reminderTimes))

	if deadlineID == 0 {
		return nil, ErrInvalidDeadlineID
	}
	if chatID == 0 {
		return nil, ErrInvalidChatID
	}

	if len(reminderTimes) == 0 {
		return nil, ErrNoReminderTimesProvided
	}

	for _, remindAt := range reminderTimes {
		reminders = append(reminders, models.Reminder{DeadlineID: deadlineID, ChatID: chatID, RemindAt: remindAt})
	}

	return reminders, nil
}

func validateReminder(reminder *models.Reminder) error {
	if reminder == nil {
		return ErrReminderNil
	}
	if reminder.ChatID == 0 {
		return ErrInvalidChatID
	}
	if reminder.DeadlineID == 0 {
		return ErrInvalidDeadlineID
	}
	if reminder.RemindAt.IsZero() {
		return ErrRemindAtRequired
	}

	return nil
}
