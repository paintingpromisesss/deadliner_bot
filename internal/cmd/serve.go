package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sauron/deadliner/internal/app/groups"
	"github.com/sauron/deadliner/internal/app/moderation"
	"github.com/sauron/deadliner/internal/config"
	"github.com/sauron/deadliner/internal/domain"
	"github.com/sauron/deadliner/internal/i18n"
	"github.com/sauron/deadliner/internal/platform/db"
	"github.com/sauron/deadliner/internal/platform/httpapi"
	"github.com/sauron/deadliner/internal/platform/scheduler"
	"github.com/sauron/deadliner/internal/platform/slugprovider"
	"github.com/sauron/deadliner/internal/platform/telegram"
)

// Бюджеты graceful shutdown (спека §10).
const (
	// shutdownTimeout — сколько ждём долетающие HTTP-запросы TMA после
	// Shutdown: запросы короткие, дольше держать процесс незачем.
	shutdownTimeout = 15 * time.Second
	// workerDrainTimeout — сколько ждём scheduler-воркер. Воркер дожидается
	// ТЕКУЩЕГО батча, а его горутины доводят начатую отправку под собственным
	// FinalizeTimeout (scheduler.DefaultFinalizeTimeout = 75s: нотификатор
	// держит 429 retry_after до 60с), поэтому бюджет — FinalizeTimeout плюс
	// запас на MarkSent.
	workerDrainTimeout = 90 * time.Second
	// botDrainTimeout — остановка бота: polling-цикл выходит сразу, воркеры
	// webhook-режима дорабатывают уже принятый апдейт.
	botDrainTimeout = 15 * time.Second
	// cleanupDrainTimeout — остановка cleanup-петли: она не блокируется на
	// работе дольше одного прогона (джоба выходит по ctx), поэтому бюджет
	// небольшой — он страхует от зависшего SQL, а не ждёт расписания.
	cleanupDrainTimeout = 30 * time.Second
	// webhookTimeout — best-effort setWebhook/deleteWebhook. Делается
	// собственным контекстом: на выходе ctx уже отменён сигналом.
	webhookTimeout = 10 * time.Second
	// readHeaderTimeout — защита от медленного клиента на уровне сервера.
	readHeaderTimeout = 15 * time.Second
)

// Serve поднимает полный процесс `deadliner serve`: миграции → граф сервисов
// → Telegram-бот → HTTP-сервер (REST API + статика TMA + /webhook + /healthz)
// → scheduler → cleanup-джоба. Блокируется до отмены ctx (SIGINT/SIGTERM),
// затем гасит компоненты в порядке, обратном запуску.
func Serve(ctx context.Context, cfg *config.Config, log *slog.Logger) error {
	if log == nil {
		log = slog.Default()
	}
	// Каталог строк грузится здесь, а не только в main: роутер, контроллеры и
	// воркер берут тексты из пакета i18n (ключ без загрузки вернётся сам собой,
	// а сообщения уйдут в Telegram «сырыми»). Загрузка идемпотентна — вызов из
	// main безвреден и оставлен для других подрежимов.
	i18n.MustLoad(i18n.Locales)

	mode := botMode(cfg)
	log.Info("serve: starting",
		slog.String("mode", mode),
		slog.String("addr", cfg.App.HTTPAddr),
		slog.String("version", buildVersion()))

	// 1. Схема. Serve — владелец схемы в одно-процессном деплое (спека §10):
	// применённые миграции идемпотентны, поэтому рестарт контейнера безопасен.
	if err := Migrate(ctx, cfg, log); err != nil {
		return fmt.Errorf("serve: migrate: %w", err)
	}

	// 2. Пул соединений — единственный на процесс: репозитории, воркер и
	// cleanup работают через него.
	pool, err := db.Connect(ctx, cfg.DB.URL, int32(cfg.DB.PoolMax))
	if err != nil {
		return fmt.Errorf("serve: db: %w", err)
	}
	defer pool.Close()

	// 3. Граф: клиент Telegram → сервисы → роутер → воркер.
	g, err := buildGraph(cfg, pool, log)
	if err != nil {
		return err
	}

	// Дочерний ctx для фоновых компонентов и приёма апдейтов: остановка —
	// это отмена ЭТОГО контекста (runCtx), а не родительского сигнала, потому
	// что за ней следует ещё работа (снятие вебхука, дренаж воркера).
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()

	// stop — единый порядок остановки для всех выходов из Serve (включая
	// ранние): HTTP перестаёт принимать → runCtx отменяется (бот и фоновые
	// петли выходят) → снимается вебхук (best-effort) → компоненты дренируются.
	//
	// nil-канал botDone означает «приём апдейтов не запускался» (отказ
	// setWebhook): тогда ждать нечего и вебхук снимать не нужно — он не
	// регистрировался. Условие обязано быть явным: waitCh на nil-канале
	// отсчитал бы весь бюджет впустую.
	stop := func(hs *http.Server, serverDone, botDone <-chan struct{}, bg *background) {
		if hs != nil {
			shutdownHTTP(hs, serverDone, log)
		}
		cancelRun()
		if botDone != nil {
			waitCh(botDone, botDrainTimeout, "telegram", log)
			stopBot(ctx, g.bot, log)
		}
		drain(bg, log)
	}

	// 4. Фон: scheduler-воркер и cleanup-джоба.
	bg := startBackground(runCtx, g, cfg, log)

	// 5. Telegram: регистрация вебхука (в webhook-режиме) и приём апдейтов.
	// Обработчик POST /webhook уже смонтирован в роутере (шаг 6).
	botDone, err := startBot(runCtx, g, cfg, log)
	if err != nil {
		stop(nil, nil, nil, bg)
		return err
	}

	// 6. Сокет слушаем явно: «порт занят» должен выясниться до объявления
	// готовности, а не внутри горутины сервера.
	ln, err := net.Listen("tcp", cfg.App.HTTPAddr)
	if err != nil {
		stop(nil, nil, botDone, bg)
		return fmt.Errorf("serve: listen %s: %w", cfg.App.HTTPAddr, err)
	}

	srv := &http.Server{Handler: g.router, ReadHeaderTimeout: readHeaderTimeout}
	serverDone := serveHTTP(srv, ln, log)
	log.Info("serve: ready",
		slog.String("addr", ln.Addr().String()),
		slog.String("mode", mode),
		slog.Bool("webhook", cfg.Bot.UsesWebhook()))

	// 7. Ждём сигнал и гасим в порядке, обратном запуску.
	<-ctx.Done()
	log.Info("serve: shutdown signal received", slog.String("mode", mode))
	stop(srv, serverDone, botDone, bg)

	log.Info("serve: stopped")
	return nil
}

// serveGraph — собранный граф serve. Отдельная структура вместо десятка
// локальных переменных: шаги сборки, запуска и остановки передают друг другу
// ровно один объект, и связь между ними видна целиком.
type serveGraph struct {
	moder  *moderation.Service
	worker *scheduler.Worker
	bot    *telegram.Bot
	router http.Handler
}

// buildGraph собирает сервисы, адаптеры и роутер. Сеть не запрашивается:
// клиент Telegram создаётся с WithSkipGetMe, id бота берётся из самого токена.
//
// Порядок сборки задан зависимостями: транспорт (BotSender) нужен нотификатору,
// нотификатор — сервису инвайтов (публикация в чат), а хендлеры бота — тем же
// сервисам, что и REST API. Поэтому клиент и диспетчер собираются двумя шагами:
// NewClient → сервисы → NewBot с готовым Deps.API.
func buildGraph(cfg *config.Config, pool *pgxpool.Pool, log *slog.Logger) (*serveGraph, error) {
	clock := domain.SystemClock{}
	r := newRepos(pool)

	// Конфиг Telegram собирается один раз: клиент и диспетчер обязаны видеть
	// один и тот же режим и секрет.
	botCfg := telegram.BotConfig{
		Token:         cfg.Bot.Token,
		APIBase:       cfg.Bot.APIBase,
		Mode:          telegramMode(cfg),
		WebhookSecret: cfg.Bot.WebhookSecret,
		AppPublicURL:  cfg.App.PublicURL,
	}

	client, err := telegram.NewClient(botCfg)
	if err != nil {
		return nil, fmt.Errorf("serve: telegram client: %w", err)
	}

	// Notifier поверх того же транспорта: один лимитер (§7.4) на все исходящие
	// сообщения процесса — и напоминания воркера, и публикации инвайтов.
	notifier := telegram.New(client.Sender(), cfg.Bot.RateGlobal, cfg.Bot.RatePerChat).WithLogger(log)

	groupsSvc := newGroupsService(r, cfg, clock, log)
	slugProvider, err := slugprovider.NewLocal(cfg.Limits.SlugRegex, r.Groups)
	if err != nil {
		return nil, fmt.Errorf("serve: slug provider: %w", err)
	}
	// Жалоба на слаг (/report_slug, спека §3.3) ходит в ЛС супер-админам через
	// тот же нотификатор, что и напоминания: один лимитер на процесс (§7.4).
	groupsSvc.WithOptions(groups.Options{Slugs: slugProvider, Notifier: notifier, Users: r.Users})
	// Инвайты с чекбоксом «Опубликовать в чат» уходят в привязанный чат
	// группы с Main App-кнопкой (startapp = код инвайта).
	groupsSvc.WithOptions(groups.Options{InvitePublisher: telegram.NewInvitePublisher(notifier, cfg.App.PublicURL)})
	moderationSvc := newModerationService(r, cfg, clock, log)
	authSvc := newAuthService(r, cfg, clock)
	deadlinesSvc := newDeadlinesService(r, clock, log)
	// Модерация дедлайнов: уведомления админам и announce в чат группы.
	deadlinesSvc.WithNotifier(notifier)
	notificationsSvc := newNotificationsService(r)

	bot, err := telegram.NewBot(botCfg, telegram.Deps{
		Users:      r.Users,
		Binder:     groupsSvc,     // /bind_group, /unbind, /groups
		Superadmin: moderationSvc, // /promote, /ban, /unban, /stats, /delete_group
		Reports:    groupsSvc,     // /report_slug (§3.3)
		Sender:     client.Sender(),
		API:        client.API(),
	}, log)
	if err != nil {
		return nil, fmt.Errorf("serve: telegram bot: %w", err)
	}

	router := httpapi.New(httpapi.Deps{
		Auth:          authSvc,
		Groups:        groupsSvc,
		Deadlines:     deadlinesSvc,
		Notifications: notificationsSvc,
		Users:         r.Users,
		Sessions:      r.Sessions,
		Log:           log,
		SessionTTL:    sessionTTL(cfg),
		// nil в polling-режиме: POST /webhook тогда отвечает 404 от статики,
		// а не принимает апдейты, которые никто не обрабатывает.
		WebhookHandler: webhookHandler(cfg, bot),
	})

	worker := scheduler.New(scheduler.Deps{
		Pool:        pool,
		Reminders:   r.Reminders,
		Deadlines:   r.Deadlines,
		Memberships: r.Memberships,
		Groups:      r.Groups,
		Bindings:    r.Bindings,
		Users:       r.Users,
		Notifier:    notifier,
		Clock:       clock,
		Log:         log,
	}, scheduler.Config{
		PollInterval: cfg.Scheduler.PollInterval,
		Batch:        cfg.Scheduler.Batch,
		LockTTL:      cfg.Scheduler.LockTTL,
		MaxAttempts:  cfg.Scheduler.MaxAttempts,
		WorkerID:     workerID(),
		// Ноль — воркер подставит свой дефолт (scheduler.DefaultFinalizeTimeout).
		// Значение дублировать здесь не нужно: оно обязано совпадать с тем, под
		// которым воркер реально работает, а единственный источник — константа
		// в пакете scheduler (проверяется тестом).
	})

	return &serveGraph{moder: moderationSvc, worker: worker, bot: bot, router: router}, nil
}

// background — фоновые компоненты serve и их завершение.
type background struct {
	workerDone chan struct{}
	cleanDone  chan struct{}
}

// startBackground запускает scheduler-воркер и cleanup-джобу. Обе петли
// блокирующие, поэтому живут в горутинах и выходят по отмене ctx.
func startBackground(ctx context.Context, g *serveGraph, cfg *config.Config, log *slog.Logger) *background {
	bg := &background{
		workerDone: make(chan struct{}),
		cleanDone:  make(chan struct{}),
	}

	go func() {
		defer close(bg.workerDone)
		defer recoverLoop("scheduler", log)
		if err := g.worker.Run(ctx); err != nil && ctx.Err() == nil {
			log.Error("serve: scheduler stopped with error", slog.String("error", err.Error()))
		}
	}()

	go func() {
		defer close(bg.cleanDone)
		moderation.StartCleanupLoop(ctx, g.moder, cfg.Cleanup.Interval, log)
	}()

	return bg
}

// drain ждёт завершения фоновых петель: воркер дожидается текущего батча
// (бюджет workerDrainTimeout), cleanup выходит по ctx сразу.
func drain(bg *background, log *slog.Logger) {
	if bg == nil {
		return
	}
	waitCh(bg.workerDone, workerDrainTimeout, "scheduler", log)
	waitCh(bg.cleanDone, cleanupDrainTimeout, "cleanup", log)
}

// waitCh ждёт закрытия канала с бюджетом. Логгер передаётся явно: сообщение об
// исчерпанном бюджете — самое важное при разборе зависшей остановки, и оно
// обязано попасть в тот же поток (и формат), что и остальные логи процесса, а
// не в дефолтный slog.
func waitCh(ch <-chan struct{}, budget time.Duration, what string, log *slog.Logger) {
	t := time.NewTimer(budget)
	defer t.Stop()
	select {
	case <-ch:
	case <-t.C:
		log.Warn("serve: component did not stop within budget",
			slog.String("component", what), slog.Duration("budget", budget))
	}
}

// recoverLoop логирует панику фоновой петли: падение планировщика не должно
// уносить процесс, обслуживающий бота и API (спека §2: один процесс).
func recoverLoop(what string, log *slog.Logger) {
	if r := recover(); r != nil {
		log.Error("serve: background loop panicked",
			slog.String("component", what),
			slog.Any("panic", r),
			slog.String("stack", string(debug.Stack())))
	}
}

// startBot приводит Telegram к рабочему состоянию: в webhook-режиме
// регистрирует адрес приёма апдейтов (setWebhook с secret_token), затем
// запускает цикл приёма в горутине — polling (bot.Start) или webhook-воркеры
// (bot.StartWebhook; сами апдейты приходят в POST /webhook). Возвращает канал,
// закрывающийся по выходу цикла: после отмены ctx его нужно дождаться, иначе
// процесс завершится, не доработав уже принятый апдейт.
//
// Проверки «UsesWebhook() ⇒ URL непуст» здесь нет: config.Load отвергает
// POLLING_MODE=webhook без WEBHOOK_URL, а при пустом режиме webhook включает сам
// URL. От пустого адреса всё равно страхует RegisterWebhook (возвращает ошибку),
// поэтому рассинхронизация валидации даст отказ старта, а не молчащий бот.
func startBot(ctx context.Context, g *serveGraph, cfg *config.Config, log *slog.Logger) (<-chan struct{}, error) {
	if cfg.Bot.UsesWebhook() {
		// Регистрация — под отдельным контекстом: setWebhook переживает сигнал
		// остановки, пришедший до готовности, и не тянет за собой отмену.
		wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), webhookTimeout)
		err := g.bot.RegisterWebhook(wctx, cfg.Bot.WebhookURL)
		cancel()
		if err != nil {
			// Отказ регистрации фатален: бот молча не получал бы апдейты, а
			// /bind_group и claim-флоу держатся именно на них.
			return nil, fmt.Errorf("serve: setWebhook: %w", err)
		}
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer recoverLoop("telegram", log)
		g.bot.Start(ctx)
	}()
	return done, nil
}

// stopBot останавливает бота и best-effort снимает вебхук (спека §10):
// недоставленные апдейты не теряются (drop_pending_updates=false), а Telegram
// перестаёт стучаться в уже остановленный инстанс.
func stopBot(ctx context.Context, bot *telegram.Bot, log *slog.Logger) {
	if bot == nil {
		return
	}
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), webhookTimeout)
	defer cancel()
	// Ошибка снятия вебхука не фатальна: повторный setWebhook при следующем
	// старте перезапишет адрес, а процесс уже завершается.
	if err := bot.UnregisterWebhook(wctx); err != nil {
		log.Warn("serve: deleteWebhook failed", slog.String("error", err.Error()))
	}
}

// serveHTTP отдаёт сервер в горутине и закрывает канал по выходу ServeHTTP.
func serveHTTP(srv *http.Server, ln net.Listener, log *slog.Logger) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer recoverLoop("http", log)
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("serve: http server stopped with error", slog.String("error", err.Error()))
		}
	}()
	return done
}

// shutdownHTTP гасит HTTP-сервер: новое не принимается, долетающие запросы
// успевают закрыться в пределах shutdownTimeout.
func shutdownHTTP(srv *http.Server, done <-chan struct{}, log *slog.Logger) {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Warn("serve: http shutdown incomplete", slog.String("error", err.Error()))
	}
	<-done
}
