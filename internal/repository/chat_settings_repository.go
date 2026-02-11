package repository

import (
	"context"
	"fmt"

	"github.com/paintingpromisesss/deadliner_bot/internal/models"
	"gorm.io/gorm"
)

// ChatSettingsRepository provides CRUD access to chat settings.
type ChatSettingsRepository struct {
	db *gorm.DB
}

// NewChatSettingsRepository creates a new ChatSettingsRepository.
func NewChatSettingsRepository(db *gorm.DB) (*ChatSettingsRepository, error) {
	if db == nil {
		return nil, fmt.Errorf("db is nil")
	}
	return &ChatSettingsRepository{db: db}, nil
}

// Create inserts new chat settings.
func (r *ChatSettingsRepository) Create(ctx context.Context, settings *models.ChatSettings) error {
	if settings == nil {
		return fmt.Errorf("settings is nil")
	}
	return r.db.WithContext(ctx).Create(settings).Error
}

// GetByID returns settings by ID.
func (r *ChatSettingsRepository) GetByID(ctx context.Context, id uint) (*models.ChatSettings, error) {
	var settings models.ChatSettings
	if err := r.db.WithContext(ctx).First(&settings, id).Error; err != nil {
		return nil, err
	}
	return &settings, nil
}

// GetByChatID returns settings for a specific chat.
func (r *ChatSettingsRepository) GetByChatID(ctx context.Context, chatID int64) (*models.ChatSettings, error) {
	var settings models.ChatSettings
	if err := r.db.WithContext(ctx).Where("chat_id = ?", chatID).First(&settings).Error; err != nil {
		return nil, err
	}
	return &settings, nil
}

// List returns all chat settings.
func (r *ChatSettingsRepository) List(ctx context.Context) ([]models.ChatSettings, error) {
	var settings []models.ChatSettings
	if err := r.db.WithContext(ctx).Find(&settings).Error; err != nil {
		return nil, err
	}
	return settings, nil
}

// Update saves settings changes.
func (r *ChatSettingsRepository) Update(ctx context.Context, settings *models.ChatSettings) error {
	if settings == nil {
		return fmt.Errorf("settings is nil")
	}
	return r.db.WithContext(ctx).Save(settings).Error
}

// Delete removes settings by ID.
func (r *ChatSettingsRepository) Delete(ctx context.Context, id uint) error {
	return r.db.WithContext(ctx).Delete(&models.ChatSettings{}, id).Error
}
