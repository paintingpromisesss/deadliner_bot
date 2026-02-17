package service

import (
	"context"
	"errors"

	"github.com/paintingpromisesss/deadliner_bot/internal/models"
	"github.com/paintingpromisesss/deadliner_bot/internal/repository"
	"gorm.io/gorm"
)

var (
	// ErrChatSettingsNil is returned when a nil chat settings pointer is passed.
	ErrChatSettingsNil = errors.New("chat settings is nil")
	// ErrChatSettingsRepoNil is returned when chat settings repository dependency is missing.
	ErrChatSettingsRepoNil = errors.New("chat settings repository is nil")
	// ErrInvalidChatSettingsID is returned when chat settings ID is invalid.
	ErrInvalidChatSettingsID = errors.New("invalid chat settings id")
	// ErrChatSettingsNotFound is returned when chat settings cannot be found.
	ErrChatSettingsNotFound = errors.New("chat settings not found")
)

type ChatSettingsService struct {
	repo *repository.ChatSettingsRepository
}

func NewChatSettingsService(repo *repository.ChatSettingsRepository) (*ChatSettingsService, error) {
	if repo == nil {
		return nil, ErrChatSettingsRepoNil
	}
	return &ChatSettingsService{repo: repo}, nil
}

// Create validates and creates chat settings.
func (s *ChatSettingsService) Create(ctx context.Context, settings *models.ChatSettings) error {
	if err := validateChatSettings(settings); err != nil {
		return err
	}

	return s.repo.Create(ctx, settings)
}

// GetByChatID returns chat settings by chat ID.
func (s *ChatSettingsService) GetByChatID(ctx context.Context, chatID int64) (*models.ChatSettings, error) {
	if chatID == 0 {
		return nil, ErrInvalidChatID
	}

	settings, err := s.repo.GetByChatID(ctx, chatID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrChatSettingsNotFound
		}
		return nil, err
	}

	return settings, nil
}

// Update validates and updates chat settings.
func (s *ChatSettingsService) Update(ctx context.Context, settings *models.ChatSettings) error {
	if err := validateChatSettings(settings); err != nil {
		return err
	}
	if settings.ID == 0 {
		return ErrInvalidChatSettingsID
	}

	existing, err := s.repo.GetByID(ctx, settings.ID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrChatSettingsNotFound
		}
		return err
	}

	if existing.ChatID != settings.ChatID {
		return ErrChatSettingsNotFound
	}

	return s.repo.Update(ctx, settings)
}

// Delete removes chat settings by ID.
func (s *ChatSettingsService) Delete(ctx context.Context, id uint) error {
	if id == 0 {
		return ErrInvalidChatSettingsID
	}

	if _, err := s.repo.GetByID(ctx, id); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrChatSettingsNotFound
		}
		return err
	}

	return s.repo.Delete(ctx, id)
}

// DeleteByChatID removes chat settings by chat ID.
func (s *ChatSettingsService) DeleteByChatID(ctx context.Context, chatID int64) error {
	if chatID == 0 {
		return ErrInvalidChatID
	}

	settings, err := s.repo.GetByChatID(ctx, chatID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrChatSettingsNotFound
		}
		return err
	}

	return s.repo.Delete(ctx, settings.ID)
}

func validateChatSettings(settings *models.ChatSettings) error {
	if settings == nil {
		return ErrChatSettingsNil
	}
	if settings.ChatID == 0 {
		return ErrInvalidChatID
	}

	return nil
}
