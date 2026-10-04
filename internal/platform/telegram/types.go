package telegram

import (
	"context"

	"github.com/sauron/deadliner/internal/app/groups"
	"github.com/sauron/deadliner/internal/app/moderation"
	"github.com/sauron/deadliner/internal/domain"
)

// OutMessage — исходящее сообщение: минимум, который нужен боту и нотификатору.
// ButtonURL непустой добавляет inline-кнопку (спека §6.2): web_app-кнопка в
// ЛС, url-кнопка в группе (Telegram разрешает web_app только в приватных
// чатах).
type OutMessage struct {
	ChatID   int64
	ThreadID *int64
	Text     string

	// ButtonText/ButtonURL — inline-кнопка «Открыть Deadliner» (спека §6.2).
	ButtonText string
	ButtonURL  string

	// LinkPreviewOff отключает превью ссылок (как в нотификаторе).
	LinkPreviewOff bool
}

// MessageSender — опциональное РАСШИРЕНИЕ Sender: отправка OutMessage с
// inline-кнопкой и возвратом message_id. Отдельный интерфейс (а не методы в
// Sender), чтобы Notifier и его фейки компилировались без него: Notifier
// использует MessageSender, только если нижележащий sender его реализует,
// иначе деградирует до обычного SendMessage. Этот же интерфейс — контракт
// хендлеров бота на отправку.
type MessageSender interface {
	Send(ctx context.Context, m OutMessage) (messageID int64, err error)
}

// ChatAdminChecker — статус участника чата. Хендлерам нужна ровно одна
// проверка: является ли сам бот администратором чата (спека §6.1, /bind_group).
type ChatAdminChecker interface {
	IsChatAdmin(ctx context.Context, chatID, userID int64) (bool, error)
}

// GroupBinder — use case-поверхность привязки чата для хендлеров /bind_group,
// /unbind и /groups. Реализуется internal/app/groups.Service (адаптер не
// нужен; telegram может импортировать app по правилу зависимостей).
type GroupBinder interface {
	BindChat(ctx context.Context, actor *domain.User, chatID int64, threadID *int64, slug, chatTitle string) (*domain.Group, error)
	UnbindChat(ctx context.Context, actor *domain.User, chatID int64, threadID *int64) (*domain.Group, error)
	// ListMine — /groups: мои группы с ролями.
	ListMine(ctx context.Context, actor *domain.User) ([]groups.MyGroup, error)
}

// Superadmin — use case-поверхность служебных команд (§6.1). Реализуется
// internal/app/moderation.Service (адаптер не нужен).
type Superadmin interface {
	PromoteSuperadmin(ctx context.Context, actor *domain.User, telegramID int64) error
	BanUser(ctx context.Context, actor *domain.User, telegramID int64) error
	UnbanUser(ctx context.Context, actor *domain.User, telegramID int64) error
	DeleteGroup(ctx context.Context, actor *domain.User, slug string) (*domain.Group, error)
	Stats(ctx context.Context, actor *domain.User) (domain.Stats, error)
}

// SlugReporter — use case-поверхность жалобы на слаг (спека §3.3,
// /report_slug). Реализуется internal/app/groups.Service. Права и рассылка —
// внутри use case; хендлер отвечает обобщённым текстом.
type SlugReporter interface {
	ReportSlug(ctx context.Context, actor *domain.User, slug string) (*domain.Group, error)
}

// Проверки совместимости на этапе компиляции: реализации обязаны
// удовлетворять интерфейсам (иначе ошибка на сборке, а не в рантайме).
var (
	_ GroupBinder      = (*groups.Service)(nil)
	_ MessageSender    = (*BotSender)(nil)
	_ ChatAdminChecker = (*BotSender)(nil)
	_ Sender           = (*BotSender)(nil)
	_ Superadmin       = (*moderation.Service)(nil)
	_ SlugReporter     = (*groups.Service)(nil)
)
