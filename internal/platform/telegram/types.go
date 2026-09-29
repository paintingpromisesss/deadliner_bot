package telegram

import (
	"context"

	"github.com/sauron/deadliner/internal/app/groups"
	"github.com/sauron/deadliner/internal/domain"
)

// OutMessage — исходящее сообщение: минимум, который нужен боту и нотификатору.
// ButtonURL непустой добавляет inline-кнопку «Открыть Deadliner» (спека §6.2):
// web_app-кнопка в личном чате, обычная url-кнопка в группе (Telegram разрешает
// web_app-кнопки только в приватных чатах, поэтому в группах используется URL
// того же APP_PUBLIC_URL — иначе отправка кода в чат группы падала бы с 400).
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
// Sender) — чтобы существующий Notifier и его тестовые фейки продолжали
// компилироваться: Notifier использует MessageSender, только если нижележащий
// sender его реализует, иначе деградирует до обычного SendMessage.
//
// RULING (Task 10): этот же интерфейс — контракт хендлеров бота на отправку.
type MessageSender interface {
	Send(ctx context.Context, m OutMessage) (messageID int64, err error)
}

// ChatAdminChecker — статус участника чата. Хендлерам нужна ровно одна
// проверка: является ли сам бот администратором чата (спека §6.1, /bind_group).
type ChatAdminChecker interface {
	IsChatAdmin(ctx context.Context, chatID, userID int64) (bool, error)
}

// GroupBinder — use case-поверхность привязки чата для хендлеров /bind_group,
// /unbind и /groups. Реализуется internal/app/groups.Service (сигнатуры
// совпадают, адаптер не нужен). Пакет telegram лежит в platform и по правилу
// зависимостей может импортировать app — здесь это нужно ради типа MyGroup:
// дублировать проекцию групп в отдельный DTO смысла нет.
type GroupBinder interface {
	BindChat(ctx context.Context, actor *domain.User, chatID int64, threadID *int64, slug, chatTitle string) (*domain.Group, error)
	UnbindChat(ctx context.Context, actor *domain.User, chatID int64, threadID *int64) (*domain.Group, error)
	// ListMine — /groups: мои группы с ролями.
	ListMine(ctx context.Context, actor *domain.User) ([]groups.MyGroup, error)
}

// Проверки совместимости на этапе компиляции: реальные реализации обязаны
// удовлетворять интерфейсам без адаптеров (иначе serve сломается на сборке,
// а не в рантайме).
var (
	_ GroupBinder      = (*groups.Service)(nil)
	_ MessageSender    = (*BotSender)(nil)
	_ ChatAdminChecker = (*BotSender)(nil)
	_ Sender           = (*BotSender)(nil)
)
