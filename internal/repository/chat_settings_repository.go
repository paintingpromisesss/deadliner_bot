package repository

import (
	"context"
	"fmt"

	"github.com/paintingpromisesss/deadliner_bot/internal/models"
	"gorm.io/gorm"
)

type ChatSettingsRepository interface {
	Create(ctx context.Context, settings *models.ChatSettings) error
	GetByID(ctx context.Context, id uint) (*models.ChatSettings, error)
	GetByChatID(ctx context.Context, chatID int64) (*models.ChatSettings, error)
	List(ctx context.Context) ([]models.ChatSettings, error)
	Update(ctx context.Context, settings *models.ChatSettings) error
	Delete(ctx context.Context, id uint) error
}

var _ ChatSettingsRepository = (*chatSettingsRepository)(nil)

// chatSettingsRepository is a GORM-backed ChatSettingsRepository implementation.
type chatSettingsRepository struct {
	db *gorm.DB
}

// NewChatSettingsRepository creates a new ChatSettingsRepository.
func NewChatSettingsRepository(db *gorm.DB) (ChatSettingsRepository, error) {
	if db == nil {
		return nil, fmt.Errorf("db is nil")
	}
	return &chatSettingsRepository{db: db}, nil
}

// Create inserts new chat settings.
func (r *chatSettingsRepository) Create(ctx context.Context, settings *models.ChatSettings) error {
	if settings == nil {
		return fmt.Errorf("settings is nil")
	}
	return dbFromContext(ctx, r.db).WithContext(ctx).Create(settings).Error
}

// GetByID returns settings by ID.
func (r *chatSettingsRepository) GetByID(ctx context.Context, id uint) (*models.ChatSettings, error) {
	var settings models.ChatSettings
	if err := dbFromContext(ctx, r.db).WithContext(ctx).First(&settings, id).Error; err != nil {
		return nil, err
	}
	return &settings, nil
}

// GetByChatID returns settings for a specific chat.
func (r *chatSettingsRepository) GetByChatID(ctx context.Context, chatID int64) (*models.ChatSettings, error) {
	var settings models.ChatSettings
	if err := dbFromContext(ctx, r.db).WithContext(ctx).Where("chat_id = ?", chatID).First(&settings).Error; err != nil {
		return nil, err
	}
	return &settings, nil
}

// List returns all chat settings.
func (r *chatSettingsRepository) List(ctx context.Context) ([]models.ChatSettings, error) {
	var settings []models.ChatSettings
	if err := dbFromContext(ctx, r.db).WithContext(ctx).Find(&settings).Error; err != nil {
		return nil, err
	}
	return settings, nil
}

// Update saves settings changes.
func (r *chatSettingsRepository) Update(ctx context.Context, settings *models.ChatSettings) error {
	if settings == nil {
		return fmt.Errorf("settings is nil")
	}

	result := dbFromContext(ctx, r.db).WithContext(ctx).
		Model(&models.ChatSettings{}).
		Where("id = ? AND chat_id = ?", settings.ID, settings.ChatID).
		Updates(map[string]interface{}{
			"deadline_topic_id": settings.DeadlineTopicID,
			"time_zone":         settings.TimeZone,
			"default_reminders": settings.DefaultReminders,
			"updated_by":        settings.UpdatedBy,
			"setup_needed":      settings.SetupNeeded,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}

	return nil
}

// Delete removes settings by ID.
func (r *chatSettingsRepository) Delete(ctx context.Context, id uint) error {
	return dbFromContext(ctx, r.db).WithContext(ctx).Delete(&models.ChatSettings{}, id).Error
}
