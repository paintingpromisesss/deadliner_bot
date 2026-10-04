# Deadliner Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Telegram-бот + Mini App для управления дедлайнами учебных групп: групповые/персональные дедлайны, напоминания, RBAC с claim-механизмом, анти-спам.

**Architecture:** Единый Go-бинарник (режимы `serve`/`migrate`/`admin`): bot (go-telegram/bot) + REST API (chi) + scheduler на Postgres-очереди (`FOR UPDATE SKIP LOCKED`). Clean Architecture: `domain` ← `app` (use cases) ← `platform` (db/repo/telegram/httpapi/scheduler/tma). Фронтенд — React + TelegramUI, статика через `go:embed`.

**Tech Stack:** Go 1.24, PostgreSQL 16 (pgx v5, golang-migrate v4), github.com/go-telegram/bot, go-chi/chi v5, golang.org/x/time/rate, log/slog, testcontainers-go; React 18 + TypeScript + Vite + @telegram-apps/telegram-ui + @telegram-apps/sdk-react + TanStack Query + zustand; Docker Compose.

**Spec:** `docs/superpowers/specs/2026-09-28-deadliner-design.md` — читать вместе с планом; все значения (TTL, лимиты, схема БД, эндпоинты) берутся оттуда.

## Global Constraints

- Все строки интерфейсов — на русском, через каталог `internal/i18n` (ru.json); в коде — константные ключи.
- Все времена в БД — `timestamptz` (UTC); ввод/вывод — в tz пользователя (`Europe/Moscow` дефолт).
- ID — `bigint GENERATED ALWAYS AS IDENTITY`. Soft-delete только `groups`/`deadlines`.
- Лимиты/TTL — только из конфига (env), дефолты из спеки §8.
- Use cases не импортируют `platform`; только `domain`-порты. Тесты use cases — на моках портов.
- Логирование — `log/slog` JSON; каждый HTTP-запрос/апдейт — с request-id.
- Коммиты — Conventional Commits, после каждого зелёного тест-цикла.
- Каждый task заканчивается: `go build ./... && go vet ./... && go test ./...` зелёные + коммит.

## Review Focus

1. **Гонка двух воркеров на одном reminder** — второй воркер обязан не отправить дубль (SKIP LOCKED + `UPDATE … WHERE status='pending' AND locked_by=$me`). Тест: Task 9.
2. **Рестарт сервиса посреди batch** — залоченные job'ы возвращаются в pending после LOCK_TTL, уже отправленные не повторяются. Тест: Task 9.
3. **Подделка initData** — невалидная подпись/старый auth_date → 401, пользователь не создаётся. Тест: Task 6.
4. **Смена due_at дедлайна** — старые pending-напоминания отменяются, новые генерируются атомарно (одна транзакция), дублей нет (unique index). Тест: Task 8.
5. **Мусорные группы** — pending-группа без привязки и без админа удаляется после TTL; rate limit блокирует 4-ю группу за сутки. Тесты: Task 7 (лимиты), Task 12 (cleanup).
6. **429 от Telegram** — воркер уважает `retry_after`, не сжигает попытки мгновенно. Тест: Task 9 (fake Notifier).

---

### Task 1: Каркас репозитория, конфиг, логирование

**Files:**
- Create: `.gitignore`, `go.mod`, `cmd/deadliner/main.go`, `internal/config/config.go`, `internal/config/config_test.go`, `internal/platform/logx/logx.go`, `README.md` (заготовка), `docker-compose.yml`, `.env.example`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Produces: `config.Load() (*config.Config, error)` — все поля из спеки §8 с дефолтами; `logx.New(level, format string) *slog.Logger`.

- [ ] **Step 1: `git init`, `.gitignore` (bin/, .env, node_modules/, web/dist/), первый коммит**
- [ ] **Step 2: Тест конфига:** `Load` из env (через `t.Setenv`) — парсит `DATABASE_URL`, `BOT_TOKEN`, duration-поля (`SCHED_POLL_INTERVAL=10s`), дефолты (`GROUP_PENDING_TTL_DAYS=14`, `LIMIT_GROUP_CREATE_DAY=3`…), ошибка при отсутствии обязательных (`BOT_TOKEN`, `DATABASE_URL`).
- [ ] **Step 3: Запустить тест — FAIL (`undefined: Load`).**
- [ ] **Step 4: Реализовать `config.Load`** — структура `Config` c вложенными `{App, DB, Bot, Scheduler, Limits}`; парсинг `os.Getenv` + `strconv`/`time.ParseDuration`; валидация обязательных.
- [ ] **Step 5: Тест PASS.**
- [ ] **Step 6: `main.go`-заготовка:** switch по `os.Args[1]` (`serve`/`migrate`/`admin`), `logx.New`, graceful shutdown по SIGINT/SIGTERM. `docker-compose.yml`: `postgres:16-alpine` + volume, `app` (build из multi-stage Dockerfile — пока только go-стадия).
- [ ] **Step 7: `go build ./... && go test ./...` зелёные; коммит `feat: project skeleton, config, logging`.**

### Task 2: Миграции и схема БД

**Files:**
- Create: `migrations/000001_init.up.sql`, `migrations/000001_init.down.sql`, `internal/platform/db/db.go`, `internal/cmd/migrate.go`, `Dockerfile` (полный multi-stage)
- Test: `internal/platform/db/db_test.go` (testcontainers)

**Interfaces:**
- Produces: `db.Connect(ctx, url string, poolMax int32) (*pgxpool.Pool, error)`; команда `deadliner migrate --dir migrations` (golang-migrate с `iofs`).

- [ ] **Step 1: Написать `000001_init.up.sql`** — ВСЕ таблицы из спеки §4 дословно: `users`, `groups`, `chat_bindings`, `group_memberships`, `deadlines`, `reminders`, `invites`, `claim_codes`, `user_action_counters`, `sessions`, `audit_log` + индексы + CHECK-ограничения (deadlines: group_id XOR owner_user_id; reminders unique index).
- [ ] **Step 2: `down.sql`** — обратный порядок DROP.
- [ ] **Step 3: Тест:** testcontainers поднимает postgres:16-alpine, `migrate.Up()` без ошибок, повторный `Up()` идемпотентен, `Down()` откатывает; проверить существование таблиц через `information_schema`.
- [ ] **Step 4: Тест FAIL → реализовать `db.Connect` + `cmd/migrate.go`** (golang-migrate, source `iofs` из `embed.FS` директории migrations, driver `pgx`).
- [ ] **Step 5: Тест PASS; коммит `feat: database schema and migrations`.**

### Task 3: Домен — сущности, ошибки, порты

**Files:**
- Create: `internal/domain/user.go`, `group.go`, `deadline.go`, `reminder.go`, `invite.go`, `claim.go`, `errors.go`, `ports.go`, `slug.go`
- Test: `internal/domain/slug_test.go`, `internal/domain/reminder_test.go`

**Interfaces:**
- Produces: типы `User, Group, ChatBinding, Membership, Deadline, Reminder, Invite, ClaimCode`; константы статусов (`GroupStatusPending/Active/Archived`, `RoleAdmin/Member`, `ReminderStatusPending/Sent/Failed/Cancelled`, `DeadlineStatusActive/Done`); ошибки-сентинелы (`ErrNotFound, ErrConflict, ErrForbidden, ErrRateLimit, ErrInvalidSlug`); порты из спеки §2.2: `GroupRepo, UserRepo, DeadlineRepo, ReminderRepo, InviteRepo, ClaimRepo, CounterRepo, SessionRepo, AuditRepo, Notifier, SlugProvider, Clock`; `slug.Normalize(raw string) string` и `slug.ValidateStrict(s string) error`.
- `Reminder.Plan(d Deadline, presets []time.Duration, now time.Time) []Reminder` — генерация fire_at (прошлые — отбрасываются).

- [ ] **Step 1: Тесты slug:** `Normalize(" м8о-401б-23 ") == "М8О-401Б-23"`; `ValidateStrict` принимает `М8О-401Б-23`, `ИКБО-33-21`; отклоняет `asdf123!!`, пустую, >16 симв., слаг не по шаблону (буквы-цифры-дефис, ≥1 дефис для вузовских — уточнение: допускаются и односегментные, решение зафиксировать в тесте).
- [ ] **Step 2: Тесты `Reminder.Plan`:** due через 10 дней, пресеты {7д,3д,24ч} → 3 напоминания с fire_at; due через 12 часов → только 24ч-пресет отброшен (в прошлом), остальные… (24ч тоже в прошлом → только custom_at остаётся); дубликаты offset схлопываются.
- [ ] **Step 3: FAIL → реализовать домен-типы и функции** (чистый Go, без БД).
- [ ] **Step 4: PASS; коммит `feat: domain model, errors, ports`.**

### Task 4: SQL-репозитории (users, groups, memberships, bindings)

**Files:**
- Create: `internal/platform/repo/users.go`, `groups.go`, `memberships.go`, `bindings.go`, `counters.go`, `testutil_test.go`
- Test: `internal/platform/repo/groups_test.go` и др. (testcontainers, общая фикстура)

**Interfaces:**
- Consumes: порты Task 3, `db.Connect` Task 2.
- Produces: `repo.NewUsers(pool) domain.UserRepo`, `repo.NewGroups(pool) domain.GroupRepo`, и т.д. Метод `UserRepo.UpsertByTelegram(ctx, tgID int64, username, firstName string) (*domain.User, error)`. `CounterRepo.IncAndCheck(ctx, tx, userID, action string, window time.Duration, limit int) (bool, error)` — инкремент в окне, `false` если лимит исчерпан (участвует в транзакциях use case'ов).

- [ ] **Step 1: `testutil_test.go`:** helper `newTestDB(t)` — testcontainers + migrate.Up + pgxpool.
- [ ] **Step 2: Тесты groups:** Create (pending, claim_expires_at = now+TTL из параметра), конфликт по `slug_norm` (создать `М8О-401Б-23` и `м8о-401б-23` → второй ErrConflict), GetByID, SearchByPrefix (только active + свои pending), UpdateStatus, SoftDelete.
- [ ] **Step 3: Тесты users/memberships/bindings:** upsert идемпотентен; binding unique (chat_id, thread_id) → ErrConflict; role update; counter IncAndCheck: limit=3, 4-й вызов false, новое окно — снова true.
- [ ] **Step 4: FAIL → реализовать; PASS.**
- [ ] **Step 5: Коммит `feat: core SQL repositories`.**

### Task 5: i18n-каталог

**Files:**
- Create: `internal/i18n/i18n.go`, `internal/i18n/locales/ru.json`
- Test: `internal/i18n/i18n_test.go`

**Interfaces:**
- Produces: `i18n.T(key string, args ...any) string` (fmt-подстановка), `i18n.Load(fs embed.FS) error`. Ключи: `bot.start`, `bot.bind.ok`, `bot.bind.conflict`, `reminder.group.title`, `api.error.*` и т.д. — заводится по мере надобности в задачах; в этой — каркас + первые 20 ключей.

- [ ] **Step 1: Тест:** T("bot.start") из тестового JSON; отсутствующий ключ → возвращает сам ключ (не падает); подстановка аргументов.
- [ ] **Step 2: FAIL → реализовать (embed + json.Unmarshal в map). PASS. Коммит `feat: i18n catalog`.**

### Task 6: Auth — валидация initData, сессии, /me

**Files:**
- Create: `internal/app/auth/service.go`, `internal/platform/telegram/webapp/validate.go`, `internal/platform/httpapi/router.go`, `internal/platform/httpapi/middleware.go`, `internal/platform/httpapi/auth_controller.go`, `internal/platform/repo/sessions.go`
- Test: `internal/platform/telegram/webapp/validate_test.go`, `internal/app/auth/service_test.go`, `internal/platform/httpapi/auth_controller_test.go`

**Interfaces:**
- Produces: `webapp.ValidateInitData(raw, botToken string, now time.Time, maxAge time.Duration) (*webapp.InitData, error)` — HMAC по стандарту (секрет = HMAC_SHA256(key=`WebAppData`, msg=botToken); data_check_string = пары без hash, отсорт., `\n`-join; constant-time compare; проверка auth_date). `InitData{User{TgID,Username,FirstName}, AuthDate, Hash}`.
- `auth.Service.Login(ctx, initData string) (*domain.User, token string, err)`; `SessionRepo.Create/GetActive/Revoke`; сессия: 32 байт random hex, в БД — SHA-256 токена, TTL 30 дней sliding (обновлять expires_at при валидации не чаще раза в час — по полю last_seen).
- httpapi: `router.New(deps) chi.Router`; middleware `Auth(sessionRepo)` кладёт `*domain.User` в ctx (`middleware.UserFrom(ctx)`); `RequestID`; JSON error helper `httpapi.WriteError(w, status, code, msg)`.
- Эндпоинты: `POST /api/v1/auth/telegram`, `GET /api/v1/me`, `PATCH /api/v1/me` (tz, dm_notify_default), `POST /api/v1/me/logout`, `GET /healthz`.

- [ ] **Step 1: Тесты validate_test.go:** собрать валидную initData вручную (известный токен, ручная HMAC-подпись в тесте) → OK; битый hash → ErrForbidden-аналог; auth_date старше maxAge → ошибка; отсутствующий user → ошибка.
- [ ] **Step 2: FAIL → реализовать validate.go. PASS.**
- [ ] **Step 3: Тесты auth.Service на моках:** Login создаёт пользователя (UpsertByTelegram) и сессию; невалидная initData — сессия не создаётся.
- [ ] **Step 4: Реализовать service + SessionRepo.**
- [ ] **Step 5: HTTP-тесты (httptest + testcontainers репозитории):** POST auth → 200 {token,user}; GET /me с Bearer → профиль; без токена → 401; PATCH /me меняет tz; logout отзывает токен (повторный /me → 401).**
- [ ] **Step 6: Коммит `feat: initData auth, sessions, me endpoints`.**

### Task 7: Use cases групп — создание, поиск, membership, инвайты, rate limits

**Files:**
- Create: `internal/app/groups/service.go`, `internal/platform/httpapi/groups_controller.go`
- Test: `internal/app/groups/service_test.go`

**Interfaces:**
- Consumes: репо Task 4, домен Task 3, `CounterRepo.IncAndCheck`.
- Produces: `groups.Service` c методами `Create(ctx, actor, slug, title) (*Group, error)` (нормализация, ValidateStrict — superadmin минует regex, pending-TTL, лимиты 3/день 5/неделю, аудит), `Search(ctx, actor, q)`, `Get(ctx, actor, id)` (роль вызывающего в ответе), `Update(ctx, actor, id, patch)` (admin), `Delete`, `ListMine(ctx, actor)`, `Invite.Create/Redeem/Revoke`, `Membership.SetRole/Remove/Leave`.
- HTTP: все `/groups*`, `/invites/redeem` из спеки §5.2 (без claim — Task 10).

- [ ] **Step 1: Тесты service на моках:** Create — нормализация слага; занятый слаг → ErrConflict; 4-я группа за сутки → ErrRateLimit; superadmin с нестандартным слагом → OK. Redeem инвайта: истёк → ошибка; max_uses исчерпан → ошибка; роль проставляется; повторный redeem тем же → уже member (идемпотентно).
- [ ] **Step 2: FAIL → реализовать service.go.**
- [ ] **Step 3: PASS; контроллеры + httptest-тесты основных маршрутов (201/403/409/429).**
- [ ] **Step 4: Коммит `feat: groups, memberships, invites`.**

### Task 8: Дедлайны и генерация reminders

**Files:**
- Create: `internal/app/deadlines/service.go`, `internal/platform/repo/deadlines.go`, `internal/platform/repo/reminders.go`, `internal/platform/httpapi/deadlines_controller.go`
- Test: `internal/app/deadlines/service_test.go`, `internal/platform/repo/reminders_test.go`

**Interfaces:**
- Produces: `deadlines.Service`: `Create(ctx, actor, in CreateInput) (*Deadline, []Reminder, error)` — в одной транзакции deadline + reminders (пресеты группы для групповых, если `reminders` не переданы явно); `Update` — при смене due_at: `ReminderRepo.Regenerate` (cancel pending → создать новые, unique index страхует); `Delete` (soft) + cancel pending; `Complete`; `List` (фильтры from/to/status/scope). Права: персональный — только owner; групповой — member читает, admin пишет.
- `ReminderRepo` — полный интерфейс из спеки §2.2 (FetchDue — в Task 9; здесь Regenerate/CancelByDeadline/ByOffset).
- HTTP: `/deadlines*`, `/groups/{id}/deadlines`, `/me/deadlines`.

- [ ] **Step 1: Repo-тесты (testcontainers):** Regenerate атомарен: создать deadline + 3 reminders, Update due_at → старые cancelled, новые 3; concurrent-тест: два Regenerate параллельно — unique index не пропускает дубли, итог консистентен.
- [ ] **Step 2: Service-тесты на моках:** права (member не может создать групповой дедлайн; не-owner не может менять персональный); due_at в прошлом → валидационная ошибка; ≤10 reminders; offsets ≥5 мин.
- [ ] **Step 3: FAIL → реализовать. PASS.**
- [ ] **Step 4: HTTP-тесты CRUD. Коммит `feat: deadlines with reminder generation`.**

### Task 9: Scheduler-воркер + Notifier c rate limiter

**Files:**
- Create: `internal/platform/scheduler/worker.go`, `internal/platform/scheduler/limiter.go`, `internal/platform/telegram/notifier.go`, `internal/platform/repo/reminders_fetch.go`
- Test: `internal/platform/scheduler/worker_test.go`, `internal/platform/repo/reminders_fetch_test.go`

**Interfaces:**
- Consumes: `ReminderRepo.FetchDue/MarkSent/MarkFailed/ReleaseStale`, `Notifier` (порт), `Clock`, конфиг scheduler.
- Produces: `scheduler.New(repos, notifier, cfg, log) *scheduler.Worker` с `Run(ctx)`; реализация `FetchDue` — `SELECT … FOR UPDATE SKIP LOCKED LIMIT $batch` + `UPDATE locked_by/locked_at` одной транзакцией; `MarkSent` — `UPDATE … WHERE status='pending' AND locked_by=$me` → rows affected = право считать отправленным.
- `notifier.New(botAPI, globalRate, perChatRate) domain.Notifier` — x/time/rate лимитеры, обработка 429 (`retry_after`), ошибки 403 (пользователь заблокировал бота) → помечать `users.bot_blocked` (добавить колонку миграцией 000002).
- Fan-out `dm_dup`: после успешной отправки в чат — в той же транзакции MarkSent создать дочерние reminders kind='dm_dup' для участников с флагом (спека §7.3).

- [ ] **Step 1: Repo-тест FetchDue:** 3 pending (2 due, 1 будущий) → вернулись 2; конкурентный тест: две горутины с двумя пулами вызывают FetchDue на одном due-job → ровно одна получила его (SKIP LOCKED).
- [ ] **Step 2: Worker-тесты (fake Clock, fake Notifier, реальные repo):** due job → sent; notifier error → failed + retry_at backoff (30s,2m,…); attempts=5 → status failed; restart-сценарий: job locked, ReleaseStale(LOCK_TTL) → pending снова, отправка ровно один раз; 429 с retry_after=7 → MarkFailed retry_at=now+7s, attempt не сгорает зря.
- [ ] **Step 3: dm_dup тест:** групповой reminder sent → созданы N дочерних для участников с dm_notify=on; у дочернего ошибка (ЛС недоступен) → родитель остаётся sent.
- [ ] **Step 4: FAIL → реализовать worker.go, limiter.go, notifier.go. PASS.**
- [ ] **Step 5: Коммит `feat: reminder scheduler with SKIP LOCKED and rate limiting`.**

### Task 10: Привязка чата + claim-флоу (бот + API)

**Files:**
- Create: `internal/platform/telegram/bot.go`, `internal/platform/telegram/handlers_start.go`, `handlers_bind.go`, `internal/app/claims/service.go`, `internal/platform/repo/claims.go`, `internal/platform/httpapi/claims_controller.go`
- Test: `internal/app/claims/service_test.go`, `internal/platform/telegram/handlers_bind_test.go`

**Interfaces:**
- Produces: `telegram.NewBot(cfg, deps, log) (*bot.Bot, error)` — go-telegram/bot, режимы polling/webhook (webhook handler монтируется в тот же chi-роутер на `POST /webhook` с `WithWebhookSecretToken`); хендлеры `/start` (приветствие + web_app кнопка + setMenuButton), `/help`, `/bind_group <slug>`, `/unbind`, `/groups`; при любом ЛС-апдейте — `UserRepo.UpsertByTelegram` + `setMyCommands` при старте.
- `claims.Service`: `StartClaim(ctx, actor, groupID) (expiresAt, error)` — группа pending/active без админа ИЛИ активный админ-сценарий смены старосты; наличие binding обязательно; лимит 3/час на чат + cooldown 1 мин; код 6 цифр, в БД SHA-256, пост в чат через Notifier; `Confirm(ctx, actor, groupID, code)` — роль admin, группа → active, код сгорает, уведомление действующим админам (если были); `Revoke`.
- Bind-логика (use case `groups.Service.BindChat(ctx, actor, chatID, threadID, slug, chatTitle)`: бот — админ чата (проверка через bot API `GetChatMember` — в тестах мок интерфейса `ChatAdminChecker`); вызывающий — member группы/её создатель; 1 чат = 1 группа.

- [ ] **Step 1: Service-тесты claims на моках:** Start без binding → ошибка; 4-й код за час в чат → ErrRateLimit; Confirm с верным кодом → admin + active; с неверным → ошибка; с истёкшим → ошибка; повторный Confirm — ErrConflict.
- [ ] **Step 2: Bind-тесты:** не-админ чата → отказ; второй слаг на тот же чат → конфликт с текстом про /unbind; успех → binding создан, группа active не становится (нужен claim).
- [ ] **Step 3: FAIL → реализовать claims service/repo, bot handlers (тесты хендлеров — на fake-ботах go-telegram/bot или прямом вызове handler-функций с сконструированным Update).**
- [ ] **Step 4: HTTP-контроллеры `/groups/{id}/claim/*`. PASS.**
- [ ] **Step 5: Коммит `feat: chat binding and claim flow`.**

### Task 11: Настройки уведомлений + персональные дедлайны в боте

**Files:**
- Create: `internal/platform/httpapi/notifications_controller.go`, `internal/platform/telegram/handlers_deadline.go`
- Test: `internal/platform/httpapi/notifications_controller_test.go`

**Interfaces:**
- Produces: `GET/PATCH /api/v1/notifications/settings` (спека §5.2: dm_notify default + per-group override в `group_memberships.dm_notify`); `/new_deadline` в ЛС → web_app deeplink-кнопка.

- [ ] **Step 1: Тесты:** PATCH без group_id меняет users.dm_notify_default; с group_id — membership строку (создаёт при отсутствии? — НЕТ: только для участника, иначе 403); GET возвращает effective-значение (COALESCE).
- [ ] **Step 2: FAIL → реализовать. PASS. Коммит `feat: notification settings`.**

### Task 12: Cleanup-джоба pending-групп + аудит + superadmin CLI

**Files:**
- Create: `internal/app/moderation/service.go`, `internal/cmd/admin.go`, `internal/platform/telegram/handlers_superadmin.go`
- Test: `internal/app/moderation/service_test.go`

**Interfaces:**
- Produces: `moderation.Service`: `CleanupExpiredPending(ctx) (deleted int, err)` (claim_expires_at < now, нет binding, нет admin-membership → soft delete + cancel reminders), `PromoteSuperadmin`, `BanUser` (is_banned → middleware httpapi и bot отвергнут), `DeleteGroup`, `Stats` (счётчики users/groups/deadlines/reminders failed). Запуск cleanup — cron-петля в `serve` (каждый час).
- CLI `deadliner admin promote-superadmin <tg_id> | ban <tg_id> | delete-group <slug> | stats`; бот-команды `/promote /ban /stats /delete_group` (проверка is_superadmin).

- [ ] **Step 1: Тесты:** pending-группа с истёкшим TTL удаляется; с binding — НЕ удаляется; с админом — НЕ; ban → Login существующей сессией работает, но создание групп/claim возвращает ErrForbidden (проверить в httpapi middleware).
- [ ] **Step 2: FAIL → реализовать. PASS. Коммит `feat: moderation, cleanup job, superadmin CLI`.**

### Task 13: TMA-каркас — Vite + TelegramUI + авторизация

**Files:**
- Create: `web/` (package.json, vite.config.ts, tsconfig, tailwind.config, src/main.tsx, src/App.tsx, src/lib/api.ts, src/lib/tma.ts, src/stores/auth.ts, src/router.tsx), `internal/platform/tma/tma.go` (embed + SPA fallback handler), Dockerfile node-стадия
- Test: `web/src/lib/api.test.ts` (vitest), `internal/platform/tma/tma_test.go`

**Interfaces:**
- Produces: `tma.Handler() http.Handler` — отдаёт `web/dist` из embed.FS, SPA-fallback, cache-заголовки по спеке §5.3; монтируется на `/`.
- `web/src/lib/api.ts`: `api.fetch<T>(path, opts)` — Bearer из store, 401 → повторный `POST /auth/telegram` c initData из `@telegram-apps/sdk-react` → retry.
- `web/src/lib/tma.ts`: init (`initData`, `expand()`, themeParams → TelegramUI AppRoot platform/theme, haptics helper).

- [ ] **Step 1: Vitest api.test.ts:** 401-перезапрос один раз (mock fetch); token подставляется в Authorization.
- [ ] **Step 2: Реализовать каркас + `AppRoot` с табами-заглушками. Go-тест tma.Handler: index.html fallback для /app/groups, 404 для /api/unknown (не перехватывать API).**
- [ ] **Step 3: Собрать web (`npm run build`), go build с embed — бинарник отдаёт SPA. Коммит `feat: TMA scaffold with auth`.**

### Task 14: TMA — экраны дедлайнов и календарь

**Files:**
- Create: `web/src/screens/deadlines/*`, `web/src/screens/calendar/*`, `web/src/components/DeadlineCard.tsx`, `DeadlineSheet.tsx`, `FilterChips.tsx`, `web/src/lib/format.ts` (склонения «день/дня/дней», обратный отсчёт)
- Test: `web/src/lib/format.test.ts`, `web/src/screens/deadlines/store.test.ts`

**Interfaces:**
- Consumes: API Task 6–8/11; TelegramUI: Cell, Card, TabBar, Placeholder, Spinner, Modal (bottom sheet), MainButton из sdk-react для submit формы.
- Produces: экраны 2–4 спеки §9: hero-карточка ближайшего дедлайна, чип-фильтры, сегменты по срокам, календарь-месяц с маркерами, bottom-sheet форма с пресет-чипами напоминаний.

- [ ] **Step 1: format.test.ts:** «1 день / 2 дня / 5 дней», «просрочен на …», локальное время в tz пользователя.
- [ ] **Step 2: store.test.ts:** фильтрация (личные/групповые), группировка по сегментам (Сегодня/7 дней/Позже/Просрочено) на фикстурных данных.
- [ ] **Step 3: Реализовать экраны; ручная проверка в Telegram dev-окружении (бот + ngrok/локальный веб-сервер).**
- [ ] **Step 4: Коммит `feat: TMA deadlines and calendar screens`.**

### Task 15: TMA — группы, настройки, админ-панель, claim UI

**Files:**
- Create: `web/src/screens/groups/*`, `web/src/screens/settings/*`, `web/src/screens/admin/*`, компоненты `MemberCell.tsx`, `InviteSheet.tsx`, `ClaimSheet.tsx`, `SlugPicker.tsx` (подсказки `/groups?q=`)
- Test: `web/src/screens/groups/flow.test.ts` (логика сторов)

**Interfaces:**
- Consumes: API `/groups*`, `/invites/redeem`, `/claim/*`, `/notifications/settings`.
- Produces: экраны 5–7 спеки §9, включая claim-флоу (кнопка «Стать админом» → start → ввод 6-значного кода → confirm) и админ-панель (участники, инвайты, binding-статус, danger zone).

- [ ] **Step 1: flow.test.ts:** стейт-машина claim (нет binding → подсказка; code sent → ввод; confirmed → роль admin); создание группы с валидацией слага на клиенте (тот же regex).
- [ ] **Step 2: Реализовать экраны. Ручная проверка. Коммит `feat: TMA groups, settings, admin screens`.**

### Task 16: Интеграция, E2E-сценарий, README, деплой

**Files:**
- Modify: `docker-compose.yml` (app целиком), `Dockerfile` (финальный multi-stage), `README.md` (полный), `.env.example` (финальный), Create: `docs/runbook.md` (webhook + TLS + Caddy)
- Test: `internal/integration/e2e_test.go` (testcontainers: БД + httptest API + fake bot Notifier)

**Interfaces:**
- Consumes: всё выше.
- Produces: сквозной тест-сценарий.

- [ ] **Step 1: e2e_test.go — сценарий:** initData-login → создать группу (pending) → bind chat (мок) → claim start/confirm → admin → создать групповой дедлайн due +30 мин c кастомным reminder через 5 мин → запустить Worker (fake Clock сдвинут) → chat-уведомление «отправлено» + dm_dup участникам с флагом → complete deadline → cleanup не трогает active.
- [ ] **Step 2: FAIL/пробелы → довести. PASS.**
- [ ] **Step 3: `docker compose up` локально: бот отвечает, TMA открывается по APP_PUBLIC_URL. README: quickstart, env-таблица, команды admin, деплой.**
- [ ] **Step 4: Коммит `feat: e2e integration, docker deploy, docs`.**
