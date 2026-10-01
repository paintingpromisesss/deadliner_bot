// Package telegram — Telegram Bot API (спека §6): адаптер Notifier (лимиты,
// 429/403), транспорт BotSender поверх go-telegram/bot и хендлеры команд
// (/start, /help, /bind_group, /unbind, /groups, /new_deadline).
//
// Хендлеры зависят только от узких интерфейсов (MessageSender,
// ChatAdminChecker, GroupBinder), поэтому проверяются юнит-тестами на фейках
// без сети; Bot связывает их с реальным клиентом.
package telegram

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/sauron/deadliner/internal/domain"
	"github.com/sauron/deadliner/internal/i18n"
)

// Mode — режим приёма апдейтов (спека §8: POLLING_MODE | WEBHOOK_URL).
type Mode string

const (
	// ModePolling — long polling (bot.Start).
	ModePolling Mode = "long_polling"
	// ModeWebhook — апдейты приходят на POST /webhook (bot.WebhookHandler).
	ModeWebhook Mode = "webhook"
)

// BotConfig — параметры Telegram-клиента (подмножество config.Bot + App).
type BotConfig struct {
	// Token — BOT_TOKEN (обязателен).
	Token string
	// APIBase — BOT_API_BASE: локальный bot-api сервер (пусто — api.telegram.org);
	// в тестах сюда подставляется httptest-заглушка.
	APIBase string
	// Mode — polling или webhook.
	Mode Mode
	// WebhookSecret — WEBHOOK_SECRET: значение заголовка
	// X-Telegram-Bot-Api-Secret-Token, проверяется библиотекой.
	WebhookSecret string
	// AppPublicURL — APP_PUBLIC_URL: цель web_app-кнопки и menu button.
	AppPublicURL string
	// PollTimeout — таймаут long polling (0 — дефолт библиотеки, 60с).
	PollTimeout time.Duration
}

// Deps — зависимости бота: domain-порты и узкие интерфейсы.
type Deps struct {
	Users domain.UserRepo
	// Binder — use case привязки чата (groups.Service).
	Binder GroupBinder
	// Superadmin — служебные команды /promote, /ban, /unban, /stats,
	// /delete_group (moderation.Service); nil — команды отвечают отказом
	// (generic), как и при отсутствии сервиса.
	Superadmin Superadmin
	// Reports — жалоба на слаг /report_slug (спека §3.3, groups.Service);
	// nil — команда отвечает generic'ом.
	Reports SlugReporter
	// Sender — транспорт отправки (BotSender).
	Sender MessageSender
	// AdminChecker — проверка «бот — администратор чата» (BotSender).
	AdminChecker ChatAdminChecker
	// BotUserID — telegram id бота для проверки админства. Ноль означает
	// «выведи из токена» (NewBot делает это офлайн через tgbot.Bot.ID).
	BotUserID int64
	// API — уже созданный клиент (NewClient). Нужен serve: транспорт должен
	// существовать ДО сервисов (нотификатор уходит в claim-флоу), а хендлеры
	// зависят от сервисов — поэтому клиент и диспетчер собираются в два шага.
	// nil — NewBot создаёт клиент сам.
	API *tgbot.Bot
}

// Client — созданный клиент Bot API и его транспорт, ещё без диспетчера
// апдейтов. Двухшаговая сборка нужна serve: транспорт требуется сервисам
// (нотификатор), а диспетчер — сервисам в своих зависимостях.
type Client struct {
	api    *tgbot.Bot
	sender *BotSender
}

// NewClient создаёт клиент Bot API и транспорт, не запрашивая сеть
// (WithSkipGetMe). Регистрация диспетчера — отдельно (NewBot с Deps.API).
func NewClient(cfg BotConfig) (*Client, error) {
	api, err := newAPI(cfg)
	if err != nil {
		return nil, err
	}
	return &Client{api: api, sender: &BotSender{api: api}}, nil
}

// API — нижележащий клиент (для NewBot с Deps.API).
func (c *Client) API() *tgbot.Bot { return c.api }

// Sender — транспорт (Notifier и хендлеры используют его же).
func (c *Client) Sender() *BotSender { return c.sender }

// newAPI собирает tgbot.Bot по конфигу (общий шаг NewBot и NewClient).
func newAPI(cfg BotConfig) (*tgbot.Bot, error) {
	opts := []tgbot.Option{tgbot.WithSkipGetMe()}
	if cfg.APIBase != "" {
		opts = append(opts, tgbot.WithServerURL(cfg.APIBase))
	}
	if cfg.WebhookSecret != "" {
		opts = append(opts, tgbot.WithWebhookSecretToken(cfg.WebhookSecret))
	}
	if cfg.PollTimeout > 0 {
		opts = append(opts, tgbot.WithHTTPClient(cfg.PollTimeout, &http.Client{Timeout: cfg.PollTimeout}))
	}
	api, err := tgbot.New(cfg.Token, opts...)
	if err != nil {
		return nil, fmt.Errorf("telegram: new bot: %w", err)
	}
	return api, nil
}

// Bot — обёртка над клиентом go-telegram/bot: транспорт, хендлеры и режимы
// запуска. Конструируется без сети (skipGetMe), поэтому доступен в тестах.
type Bot struct {
	api    *tgbot.Bot
	sender *BotSender
	cfg    BotConfig
	log    *slog.Logger
	handl  *Handlers

	// mu защищает пересборку диспетчера: SetDeps зовётся один раз на старте
	// (serve), до запуска приёма апдейтов, но подписка на апдейты идёт через
	// указатель в замыкании — пересборка обязана быть безопасной.
	mu sync.RWMutex
}

// SetDeps пересобирает диспетчер апдейтов на новых зависимостях.
//
// Нужен из-за порядка сборки в serve: транспорт (BotSender) обязан
// существовать ДО сервисов — нотификатор уходит в claim-флоу, — а сервисы
// зависят от него. Поэтому клиент и диспетчер собираются двумя шагами:
// NewClient → сервисы → NewBot(Deps.API, ... ) либо NewBot → SetDeps.
// Вызывается один раз до старта приёма апдейтов.
func (b *Bot) SetDeps(deps Deps) {
	if deps.Sender == nil {
		deps.Sender = b.sender
	}
	if deps.AdminChecker == nil {
		deps.AdminChecker = b.sender
	}
	if deps.BotUserID == 0 {
		deps.BotUserID = b.api.ID()
	}
	b.mu.Lock()
	b.handl = b.newHandlers(deps)
	b.mu.Unlock()
}

// handler возвращает текущий диспетчер (nil-safe: до SetDeps — nil).
func (b *Bot) handler() *Handlers {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.handl
}

// newHandlers собирает диспетчер на зависимостях (общий шаг NewBot/SetDeps).
func (b *Bot) newHandlers(deps Deps) *Handlers {
	return NewHandlers(HandlersDeps{
		Users:        deps.Users,
		Binder:       deps.Binder,
		Superadmin:   deps.Superadmin,
		Reports:      deps.Reports,
		Sender:       deps.Sender,
		AdminChecker: deps.AdminChecker,
		BotUserID:    deps.BotUserID,
		AppPublicURL: b.cfg.AppPublicURL,
	}, b.log)
}

// NewBot создаёт бота, транспорт и регистрирует диспетчер апдейтов. Сеть не
// запрашивается (WithSkipGetMe): id бота для проверок админства берётся из
// самого токена (tgbot.Bot.ID разбирает префикс "<id>:<secret>"), а явный
// Deps.BotUserID переопределяет его при необходимости.
//
// Deps.API позволяет передать клиент, созданный ранее через NewClient (serve:
// транспорт должен существовать до сервисов); иначе клиент создаётся здесь.
func NewBot(cfg BotConfig, deps Deps, log *slog.Logger) (*Bot, error) {
	if strings.TrimSpace(cfg.Token) == "" {
		return nil, errors.New("telegram: empty bot token")
	}
	if log == nil {
		log = slog.Default()
	}
	if cfg.Mode == "" {
		cfg.Mode = ModePolling
	}
	// Fail-closed (спека §6.1): в webhook-режиме без секрета библиотека
	// принимает любой POST /webhook, то есть подделанные апдейты — включая
	// /bind_group в чужом чате. Лучше не стартовать, чем стартовать открытым.
	if cfg.Mode == ModeWebhook && cfg.WebhookSecret == "" {
		return nil, errors.New("telegram: webhook mode requires WebhookSecret (empty secret accepts forged updates)")
	}

	api := deps.API
	if api == nil {
		var err error
		if api, err = newAPI(cfg); err != nil {
			return nil, err
		}
	}

	b := &Bot{api: api, sender: &BotSender{api: api}, cfg: cfg, log: log}
	b.SetDeps(deps)

	// Диспетчер ловит все апдейты: маршрутизация команд выполняется вручную
	// (точный разбор «/bind_group <slug>»), а не паттернами библиотеки.
	api.RegisterHandlerMatchFunc(func(*models.Update) bool { return true },
		func(ctx context.Context, _ *tgbot.Bot, upd *models.Update) {
			if h := b.handler(); h != nil {
				h.Handle(ctx, upd)
			}
		})
	return b, nil
}

// Sender — транспорт (нужен serve для Notifier: общий лимитер на отправки).
func (b *Bot) Sender() *BotSender { return b.sender }

// API — нижележащий клиент (для методов, не покрытых адаптером).
func (b *Bot) API() *tgbot.Bot { return b.api }

// WebhookHandler — http.Handler для монтирования в chi-роутер на POST /webhook
// (Task 16). Секрет проверяет библиотека (WithWebhookSecretToken).
func (b *Bot) WebhookHandler() http.Handler { return b.api.WebhookHandler() }

// Start запускает приём апдейтов (блокирующий вызов — serve гоняет его в
// горутине): команды и menu button настраиваются один раз в начале.
func (b *Bot) Start(ctx context.Context) {
	b.setupUI(ctx)
	if b.cfg.Mode == ModeWebhook {
		b.api.StartWebhook(ctx)
		return
	}
	b.api.Start(ctx)
}

// StartWebhook — явный запуск webhook-режима (апдейты приходят в
// WebhookHandler, который монтируется в chi-роутер на POST /webhook).
func (b *Bot) StartWebhook(ctx context.Context) {
	b.setupUI(ctx)
	b.api.StartWebhook(ctx)
}

// Handle — точка входа для одного апдейта (используется и тестами, и
// webhook-режимом при ручном разборе тела запроса).
func (b *Bot) Handle(ctx context.Context, upd *models.Update) {
	if h := b.handler(); h != nil {
		h.Handle(ctx, upd)
	}
}

// RegisterWebhook публикует адрес приёма апдейтов в Telegram (setWebhook,
// спека §10) вместе с secret_token: Telegram присылает его в заголовке
// X-Telegram-Bot-Api-Secret-Token, который проверяет WebhookHandler. В
// polling-режиме не делает ничего (вебхук был бы вторым конкурирующим
// приёмником апдейтов).
func (b *Bot) RegisterWebhook(ctx context.Context, url string) error {
	if b.cfg.Mode != ModeWebhook {
		return nil
	}
	if strings.TrimSpace(url) == "" {
		return errors.New("telegram: setWebhook without WEBHOOK_URL")
	}
	ok, err := b.api.SetWebhook(ctx, &tgbot.SetWebhookParams{
		URL:         url,
		SecretToken: b.cfg.WebhookSecret,
	})
	if err != nil {
		return fmt.Errorf("telegram: setWebhook: %w", fmt.Errorf("%v", TelegramError(err, 0)))
	}
	if !ok {
		return errors.New("telegram: setWebhook returned false")
	}
	b.log.Info("telegram: webhook registered", slog.String("url", url))
	return nil
}

// UnregisterWebhook снимает вебхук (graceful shutdown, спека §10). В
// polling-режиме — no-op. Вызов best-effort: ошибку возвращает вызывающий,
// который решает, считать ли её фатальной (при остановке — нет).
func (b *Bot) UnregisterWebhook(ctx context.Context) error {
	if b.cfg.Mode != ModeWebhook {
		return nil
	}
	// drop_pending_updates=false: недоставленные апдейты должны быть забраны
	// следующим запуском (или новым инстансом), а не потеряны.
	if _, err := b.api.DeleteWebhook(ctx, &tgbot.DeleteWebhookParams{DropPendingUpdates: false}); err != nil {
		return fmt.Errorf("telegram: deleteWebhook: %v", TelegramError(err, 0))
	}
	b.log.Info("telegram: webhook unregistered")
	return nil
}

// setupUI — setMyCommands + setChatMenuButton (спека §6.1). Сбой не критичен:
// логируется, запуск продолжается (без команд бот работоспособен).
func (b *Bot) setupUI(ctx context.Context) {
	h := b.handler()
	if h == nil {
		return
	}
	cmds := h.commands()
	if err := b.sender.SetCommands(ctx, cmds); err != nil {
		b.log.Warn("telegram: setMyCommands failed", slog.String("error", err.Error()))
	}
	if b.cfg.AppPublicURL == "" {
		return
	}
	if err := b.sender.SetMenuButton(ctx, i18n.T("bot.button.open_app"), b.cfg.AppPublicURL); err != nil {
		b.log.Warn("telegram: setChatMenuButton failed", slog.String("error", err.Error()))
	}
}

// TelegramError маппит ошибки Telegram API в доменные: 429 → RateLimitError;
// 403 в ЛС → BotBlockedError (chat_id ЛС = telegram_id получателя, а воркер
// помечает users.bot_blocked именно по telegram_id). 403 в ГРУППЕ означает
// «бота кикнули/нет прав», а не блокировку пользователя, поэтому уходит как
// есть — иначе MarkBotBlocked искал бы пользователя с отрицательным id.
// Остальное — как есть. Notifier и хендлеры используют один маппинг.
func TelegramError(err error, chatID int64) error {
	if err == nil {
		return nil
	}
	var tooMany *tgbot.TooManyRequestsError
	if errors.As(err, &tooMany) {
		return &domain.RateLimitError{RetryAfter: time.Duration(tooMany.RetryAfter) * time.Second}
	}
	if errors.Is(err, tgbot.ErrorForbidden) && chatID > 0 {
		return &domain.BotBlockedError{UserID: chatID}
	}
	return err
}
