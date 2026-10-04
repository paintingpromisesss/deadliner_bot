# Deadliner — Technical Design Document

Дата: 2026-09-28
Статус: на ревью

## 1. Обзор и цели

Telegram-бот + Mini App (TMA) для управления дедлайнами учебных/проектных групп.

- Группы идентифицируются слагом (например, `М8О-401Б-23`).
- Дедлайны двух видов: **групповые** (видны всей группе, уведомления в привязанный чат/топик) и **персональные** (только в ЛС владельца).
- Напоминания: пресеты (7 дней / 3 дня / 24 часа) и кастомные интервалы или точные даты.
- RBAC: Superadmin → Group Admin → Member. Первый админ группы — через claim-код, публикуемый в привязанном чате.
- Анти-мусор: строгая валидация слага, TTL для неподтверждённых групп, rate limits, модерация.

**Язык интерфейсов:** русский; все строки — через каталог сообщений (заготовка i18n).

**Не цели (v1):** веб-админка, интеграция с API МАИ (заложены интерфейсы расширения), распределённый scheduler на внешней очереди, английский язык.

## 2. Высокоуровневая архитектура

Единый Go-бинарник `deadliner` с подрежимами (cobra-free, простой switch по `os.Args[1]`):

```
deadliner serve     # bot (webhook|polling) + HTTP API + TMA static + scheduler — все в одном процессе
deadliner migrate   # применить миграции БД
deadliner admin …   # CLI супер админа: promote / ban / list-groups / delete-group
```

Компоненты внутри `serve`:

```
                    ┌───────────────────────────────────────────┐
 Telegram ──webhook/polling──►  Bot Interface (handlers)        │
                    │                 │                         │
 TMA (React SPA) ───HTTP──►  REST API  │                         │
                    │      │          ▼                         │
                    │      └──►  Application (use cases)        │
                    │                 │                         │
                    │   Scheduler ────┤ (workers, rate limiter) │
                    │                 ▼                         │
                    │  Domain  +  Repository interfaces         │
                    │                 ▼                         │
                    │      PostgreSQL (pgx)                     │
                    └───────────────────────────────────────────┘
```

Один процесс — осознанный выбор (малый масштаб, один артефакт деплоя). Горизонтальное масштабирование возможно без изменения кода: scheduler координируется через `FOR UPDATE SKIP LOCKED` в БД, поэтому несколько инстансов `serve` не конфликтуют (для бота при этом нужен либо один инстанс-приёмник апдейтов, либо перевод polling→webhook с отдельным роутингом; фиксируем как операционное ограничение).

**Операционное ограничение (масштабирование).** Документированный деплой — **один инстанс** `serve`; несколько инстансов допустимы только в webhook-режиме за балансировщиком. Взаимное исключение воркеров держится на `SCHED_LOCK_TTL`: инстанс, у которого `now - LockTTL` превысило время дренажа батча, освободит ещё живой лок (`ReleaseStale`) и переотправит напоминание — дубль в чат. Поэтому при нескольких инстансах требуется `SCHED_LOCK_TTL` больше худшего времени дренажа батча (оценка и пример в `docs/runbook.md` §2.1), а внутрипроцессная защита (`MarkSent … WHERE status='pending' AND locked_by=…`) межпроцессной не является.

### 2.1 Структура Go-пакетов (Clean Architecture)

```
cmd/deadliner/main.go          # входная точка, режимы
internal/
  config/                      # загрузка env-конфига, все лимиты/TTL здесь
  domain/                      # сущности, value objects, ошибки, интерфейсы портов
    user.go group.go deadline.go reminder.go invite.go claim.go
    ports.go                   # Repo-интерфейсы, Notifier, SlugProvider, Clock
  app/                         # use cases (без зависимостей от infra)
    auth/ groups/ deadlines/ reminders/ claims/ invites/ moderation/
  platform/
    db/                        # postgres, pgxpool, транзакции
    repo/                      # SQL-реализации доменных репозиториев
    telegram/                  # Bot API клиент, bot-хендлеры, middleware
    httpapi/                   # REST-роутер, контроллеры, DTO, middleware
    scheduler/                 # воркеры напоминаний, rate limiter
    tma/                       # embed.FS со статикой React-сборки
  i18n/                        # каталоги сообщений (ru.json), локализация ошибок
migrations/                    # SQL-миграции (golang-migrate)
web/                           # исходники React TMA (Vite + TelegramUI)
```

Правило зависимостей: `domain` не импортирует ничего внутреннего; `app` импортирует только `domain`; `platform` импортирует `app`+`domain`. Use cases получают порты (интерфейсы) через конструкторы.

### 2.2 Ключевые порты (интерфейсы)

```go
// domain/ports.go (эскиз)
type GroupRepo interface { … }          // CRUD, поиск по префиксу, статусы
type DeadlineRepo interface { … }
type ReminderRepo interface {           // очередь напоминаний
    FetchDue(now time.Time, limit int) ([]Reminder, error)  // SKIP LOCKED + lock
    MarkSent(id int64, now time.Time) error
    MarkFailed(id int64, err string, retryAt time.Time) error
    ReleaseStale(olderThan time.Time) (int64, error)
    Regenerate(deadlineID int64, presets []Offset) error    // в транзакции
}
type Notifier interface {               // доставка сообщений (телеграм)
    SendToChat(ctx, chatID, threadID int64, text string) error
    SendToUser(ctx, userID int64, text string) error
}
type SlugProvider interface {           // ТОЧКА РАСШИРЕНИЯ: API МАИ
    Validate(slug string) error         // v1: SLUG_REGEX из конфига + правила Deadliner (длина, цифра)
    Suggest(ctx, prefix string, callerID int64, limit int) ([]Group, error) // v1: по локальной таблице groups
}
type Clock interface { Now() time.Time }
```

**Local-реализация (v1, зафиксировано).** `internal/platform/slugprovider.Local` — единственная реализация `SlugProvider` в v1: `Validate` компилирует `SLUG_REGEX` из конфига и накладывает поверх доменные правила (`domain.ValidateStrict`: длина 3–16, непустые сегменты через один дефис, **минимум одна цифра**), `Suggest` — обёртка над `GroupRepo.SearchByPrefix` (активные группы + свои pending, спека §6.4). Разделение ответственности обязательно к сохранению: charset — настройка оператора (`SLUG_REGEX`), правила длины/структуры/цифры — добавления Deadliner и действуют при ЛЮБОЙ регулярке. Некомпилируемый `SLUG_REGEX` отвергается на старте (`config.Load`). Superadmin по-прежнему создаёт группы вне формата (спека §3.3) — провайдер к нему не применяется.

**Расширение «API МАИ»** (зафиксировано): будущая реализация `SlugProvider` обращается к внешнему API вуза, разрешает создание только слаг из официального пула, даёт автокомплит и помечает группы флагом `official` (колонка закладывается в схему сразу). Замена реализации — без изменения use cases.

## 3. Роли и доступы

| Роль | Права |
|---|---|
| Superadmin | всё + CLI/бот-команды: promote, ban, list-groups, delete-group, force-resolve конфликтов |
| Group Admin | CRUD групповых дедлайнов своей группы, управление участниками (кик, promote/demote админов), инвайт-коды, привязка чата, настройка дефолтных пресетов группы |
| Member | просмотр групповых дедлайнов, CRUD своих персональных дедлайнов, свои настройки уведомлений, claim незанятой группы, выход из группы |

### 3.1 Выдача первого Group Admin — claim через код в чате

```
1. Пользователь в TMA создаёт группу (slug свободен) → статус pending, создатель НЕ админ.
   Либо группа уже существует (pending/active без админов).
2. В чате (где бот уже состоит) кто-то пишет /bind_group <slug> → привязка chat_id(+thread_id).
3. В TMA пользователь жмёт «Стать админом» → бот постит в привязанный чат одноразовый
   6-значный код (TTL 10 мин, не более 3 активных попыток/час на чат).
4. Пользователь вводит код в TMA → проверка (hash-сравнение) → роль Group Admin,
   код сгорает. Группа переходит в active.
5. Superadmin может назначить админа напрямую (CLI/бот-команда) — обходной путь.
```

Безопасность: код видят все участники чата (социальный контроль); захват чужой группы требует доступа к чату. Claim в группу без админов разрешён и в active-группе (смена старосты), но действующие админы получают уведомление и могут отозвать код до ввода.

### 3.2 Делегация

- Group Admin генерирует **инвайт-коды** (TMA): роль (admin|member), max_uses, TTL (дефолт 7 дней).
- Применение кода в TMA → membership с соответствующей ролью.
- Admin может promote/demote отдельных участников и удалять их.

### 3.3 Анти-мусор (значения — в конфиге, дефолты ниже)

| Механизм | Дефолт |
|---|---|
| Валидация слага | строгий regex вузовских форматов (кириллица/латиница/цифры/дефис, 3–16 симв.); superadmin может создать слаг вне regex |
| Нормализация | uppercase, trim; уникальность по нормализованному слагy (unique index на `slug_norm`) |
| TTL pending-группы | 14 дней без привязки чата И без админа → автоудаление cleanup-джобой |
| Лимит создания групп | 3/сутки, 5/неделю на пользователя |
| Лимит claim-кодов | 3/час на чат, cooldown 1 мин на пользователя |
| Бан | superadmin банит telegram_id → запрет создавать группы/claim/привязывать чаты |
| Жалобы | админ группы с конфликтующим слагом пишет в ЛС боту `/report_slug <slug>` → жалоба в ЛС всем супер-админам (жалоба доступна админу группы в любом статусе; ответ вызывающему обобщённый) |
| Разрешение конфликта | только супер-админ, вручную: `delete_group <slug>` (не тот слаг) или назначение админа группы; отдельной команды force-resolve нет |

## 4. Схема базы данных (PostgreSQL 16)

Все времена — `timestamptz` (UTC). ID — `bigint GENERATED ALWAYS AS IDENTITY`. Soft-delete для deadlines/groups (`deleted_at`), остальное — hard delete.

```
users
  id                bigint PK
  telegram_id       bigint UNIQUE NOT NULL
  username          text
  first_name        text
  tz                text NOT NULL DEFAULT 'Europe/Moscow'   -- из initData/настроек
  dm_notify_default boolean NOT NULL DEFAULT false          -- глобальный дефолт «дубль в ЛС»
  is_superadmin     boolean NOT NULL DEFAULT false
  is_banned         boolean NOT NULL DEFAULT false
  created_at        timestamptz NOT NULL DEFAULT now()

groups
  id                bigint PK
  slug              text NOT NULL            -- нормализованный
  slug_norm         text NOT NULL UNIQUE     -- upper(slug), индекс уникальности
  title             text NOT NULL            -- отображаемое имя (может отличаться от slug)
  status            text NOT NULL DEFAULT 'pending'  -- pending|active|archived
  official          boolean NOT NULL DEFAULT false   -- заготовка под API МАИ
  created_by        bigint NOT NULL REFERENCES users(id)
  default_presets   int[] NOT NULL DEFAULT '{10080,4320,1440}' -- минуты: 7д,3д,24ч
  claim_expires_at  timestamptz              -- pending-TTL: created_at + ttl
  created_at / updated_at / deleted_at

chat_bindings
  id                bigint PK
  group_id          bigint UNIQUE NOT NULL REFERENCES groups(id) ON DELETE CASCADE
  chat_id           bigint NOT NULL
  message_thread_id bigint                   -- NULL для обычных чатов
  chat_title        text
  bound_by          bigint NOT NULL REFERENCES users(id)
  bound_at          timestamptz NOT NULL DEFAULT now()
  UNIQUE NULLS NOT DISTINCT (chat_id, message_thread_id)  -- 1 чат(топик) = 1 группа (PG15+; NULL thread_id не плодит дубли)

group_memberships
  group_id          bigint REFERENCES groups(id) ON DELETE CASCADE
  user_id           bigint REFERENCES users(id) ON DELETE CASCADE
  role              text NOT NULL DEFAULT 'member'   -- admin|member
  dm_notify         boolean                  -- NULL = наследовать users.dm_notify_default
  joined_at         timestamptz NOT NULL DEFAULT now()
  PRIMARY KEY (group_id, user_id)

deadlines
  id                bigint PK
  group_id          bigint REFERENCES groups(id) ON DELETE CASCADE  -- NULL = персональный
  owner_user_id     bigint REFERENCES users(id)                     -- обязателен для персональных
  title             text NOT NULL
  description       text
  due_at            timestamptz NOT NULL
  tz                text NOT NULL DEFAULT 'Europe/Moscow'           -- tz автора для отображения
  created_by        bigint NOT NULL REFERENCES users(id)
  status            text NOT NULL DEFAULT 'active'  -- active|done|archived
  created_at / updated_at / deleted_at
  CHECK ( (group_id IS NULL AND owner_user_id IS NOT NULL)
       OR (group_id IS NOT NULL AND owner_user_id IS NULL) )
  INDEX (group_id, due_at) WHERE deleted_at IS NULL AND status='active'
  INDEX (owner_user_id, due_at) WHERE deleted_at IS NULL AND status='active'

reminders                              -- И очередь задач scheduler'а
  id                bigint PK
  deadline_id       bigint NOT NULL REFERENCES deadlines(id) ON DELETE CASCADE
  kind              text NOT NULL       -- 'preset'|'custom_offset'|'custom_at'
  offset_minutes    int                 -- для preset/custom_offset
  fire_at           timestamptz NOT NULL
  status            text NOT NULL DEFAULT 'pending'  -- pending|sent|failed|cancelled
  attempts          int NOT NULL DEFAULT 0
  locked_by         text                -- id воркера
  locked_at         timestamptz
  last_error        text
  sent_at           timestamptz
  UNIQUE (deadline_id, kind, offset_minutes, fire_at)  -- защита от дублей при регенерации
  INDEX (status, fire_at) WHERE status='pending'

invites
  id                bigint PK
  group_id          bigint NOT NULL REFERENCES groups(id) ON DELETE CASCADE
  code              text NOT NULL UNIQUE           -- 8 символов, хранится hash
  role              text NOT NULL DEFAULT 'member'
  max_uses          int NOT NULL DEFAULT 1         -- -1 = безлимита
  used_count        int NOT NULL DEFAULT 0
  created_by        bigint NOT NULL REFERENCES users(id)
  expires_at        timestamptz NOT NULL
  revoked_at        timestamptz

claim_codes
  id                bigint PK
  group_id          bigint NOT NULL REFERENCES groups(id) ON DELETE CASCADE
  code_hash         text NOT NULL                  -- SHA-256, сам код не храним
  chat_id           bigint NOT NULL
  message_id        bigint NOT NULL
  created_by        bigint NOT NULL REFERENCES users(id)
  expires_at        timestamptz NOT NULL
  used_at           timestamptz
  INDEX (group_id) WHERE used_at IS NULL

user_action_counters                   -- rate limits в БД (переживают рестарт)
  user_id           bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE
  action            text NOT NULL       -- 'group_create'|'claim_request'|…
  window_start      timestamptz NOT NULL
  count             int NOT NULL DEFAULT 0
  PRIMARY KEY (user_id, action, window_start)

outbox_messages                        -- транзакционная отправка событий ботом (опц. v1.1)
  id bigserial PK, topic text, payload jsonb,
  status text DEFAULT 'pending', created_at, sent_at, attempts int

audit_log
  id bigserial PK, actor_user_id bigint, action text,
  target_type text, target_id bigint, meta jsonb, created_at
```

Миграции — `golang-migrate`, файлы `migrations/000001_init.up.sql` и т.д.

## 5. REST API (TMA ↔ backend)

Base: `/api/v1`. Формат: JSON. Ошибки: `{ "error": { "code": string, "message": string } }` + корректный HTTP-статус. Локализация `message` — по `Accept-Language` / полю tz пользователя.

### 5.1 Авторизация через initData

1. TMA получает `window.Telegram.WebApp.initData` (query-строка).
2. `POST /api/v1/auth/telegram` `{ initData: string }`.
3. Бэкенд валидирует по стандарту Telegram:
   - разобрать query-пары, выделить `hash`;
   - `data_check_string` = пары без `hash`, отсортированные по ключу, через `\n`;
   - `secret_key = HMAC_SHA256(key="WebAppData", data=BOT_TOKEN)`;
   - сравнить `HMAC_SHA256(secret_key, data_check_string)` с `hash` (constant-time);
   - проверить `auth_date` (свежесть ≤ `AUTH_TTL`, дефолт 24 ч — сессия живёт дольше, но вход свежий).
4. Upsert пользователя по `id` из `user`, выдача **сессии**: opaque-токен (32 байт random, hex), хранится в таблице `sessions(token_hash PK, user_id, expires_at, created_at)`, TTL 30 дней sliding. Токен возвращается клиенту, TMA хранит его в `localStorage` и шлёт в `Authorization: Bearer <token>`.

Выбор opaque-токена вместо JWT: нужна возможность отзыва (бан), состояние и так в Postgres, масштаб мал.

### 5.2 Эндпоинты

```
POST   /auth/telegram                    # initData → { token, user }

GET    /me                               # профиль + настройки
PATCH  /me                               # tz, dm_notify_default, first_name…
POST   /me/logout                        # отозвать токен

GET    /groups?q=<prefix>                # поиск/подсказка слагов (по активным + своим)
POST   /groups                           # { slug, title } → создать (pending); 409 если занят
GET    /groups/{id}                      # детали: группа, моя роль, привязка чата, статистика
PATCH  /groups/{id}                      # title, default_presets (admin)
DELETE /groups/{id}                      # admin/superadmin (soft delete)

POST   /groups/{id}/claim/start          # → постит код в чат; { expires_at } ; 4xx без привязки
POST   /groups/{id}/claim/confirm        # { code } → роль admin
POST   /groups/{id}/claim/revoke         # admin отзывает активный код

POST   /groups/{id}/invites              # admin: { role, max_uses, ttl_hours } → { code }
POST   /invites/redeem                   # { code } → вступление
DELETE /groups/{id}/invites/{code}       # admin: отозвать

GET    /groups/{id}/members              # admin: полный список; member: счётчики
PATCH  /groups/{id}/members/{user_id}    # admin: role, kick
DELETE /groups/{id}/me                   # выйти из группы (вступление — только через invite-код)

GET    /groups/{id}/deadlines?from=&to=&status=     # групповые
GET    /me/deadlines?from=&to=&status=              # персональные (+ все групповые одним флагом ?scope=all)
POST   /deadlines                                     # { group_id? | personal, title, description?,
                                                      #   due_at, reminders: [{kind, offset_minutes?|fire_at?}] }
GET    /deadlines/{id}
PATCH  /deadlines/{id}                                # автор/admin; смена due_at → регенерация reminders
DELETE /deadlines/{id}                                # soft delete + cancel pending reminders
POST   /deadlines/{id}/complete                       # статус done

POST   /groups/{id}/deadlines/preview-reminders       # отладка: что будет создано (опц.)

GET    /notifications/settings?group_id=              # dm_notify: default/effective per group
PATCH  /notifications/settings                        # { group_id?, dm_notify }
```

DTO-валидация: `due_at` — RFC3339, не в прошлом (для новых), ≤ +5 лет; title ≤ 200 симв; description ≤ 2000; reminders ≤ 10 на дедлайн; offsets ≥ 5 минут.

### 5.3 Статика TMA

`GET /` и `/app/*` — из `embed.FS`, SPA-fallback на `index.html`. Кэш статики: `Cache-Control: max-age=31536000, immutable` для ассетов с хешем, `no-cache` для `index.html`.

## 6. Bot-интерфейс

### 6.1 Команды

| Команда | Контекст | Действие |
|---|---|---|
| `/start` | ЛС | приветствие + кнопка «Открыть Deadliner» (Web App) + set menu button |
| `/help` | везде | справка |
| `/bind_group <slug>` | группа/топик | привязать этот chat_id(+message_thread_id) к группе; требовать: бот — админ чата (проверка getChatMember), вызывающий — member/admin группы или создатель; 1 чат = 1 группа (иначе ошибка с инструкцией /unbind) |
| `/unbind` | группа/топик | снять привязку (admin группы) |
| `/groups` | ЛС | мои группы + роли |
| `/new_deadline` | ЛС | inline-форма → Web App (deeplink `#add`) |
| `/promote <user_id>`, `/ban <user_id>`, `/unban <user_id>`, `/stats`, `/delete_group <slug>` | ЛС superadmin | управление инстансом |
| `/report_slug <slug>` | ЛС | админ группы жалуется на конфликтующий слаг → ЛС всем супер-админам (спека §3.3) |

Регистрация команд: `setMyCommands` — клиентские и superadmin-команды в scope personal (default), команды привязки чата дополнительно в all_chat_administrators; menu button → URL TMA.

### 6.2 Уведомления (формат)

Групповое напоминание в чат (HTML parse mode):
```
⏰ <b>Дедлайн через 24 часа</b>
📌 Курсовая работа по БД — М8О-401Б-23
🗓 29.09.2026 23:59 (MSK)
Открыть в Deadliner → [кнопка web_app]
```
Персональное — аналогично, только в ЛС. Дубли в ЛС членам группы — отдельной рассылкой из того же job'а (см. 7.3).

## 7. Scheduler / Notifier

### 7.1 Модель

Таблица `reminders` — одновременно доменная сущность и очередь задач. Генерация:

- При создании/редактировании дедлайна use case в **одной транзакции**: пишет deadline, удаляет (`cancelled`) все `pending` reminders, создаёт новые по пресетам группы (или переданным в запросе) + кастомные. `fire_at = due_at - offset`; если `fire_at` уже в прошлом — не создаётся (для «через X часов» от now — создаётся сразу как due).
- `custom_at` — точная дата-время напоминания, независимо от due_at.
- Уникальный индекс `(deadline_id, kind, offset_minutes, fire_at)` — страховка от дублей при гонках/повторах.

### 7.2 Воркер

```
цикл (интервал POLL_INTERVAL=10s, джиттер):
  1. ReleaseStale(locked_at < now-LOCK_TTL=2min) → status='pending', attempts+=1
  2. FetchDue: SELECT … WHERE status='pending' AND fire_at <= now()
     ORDER BY fire_at LIMIT BATCH=50 FOR UPDATE SKIP LOCKED
     → UPDATE status='pending', locked_by=$worker, locked_at=now() (в той же транзакции)
  3. Для каждого: notifier.Send… → MarkSent | MarkFailed(retry_at = now + backoff)
  4. attempts >= MAX_ATTEMPTS=5 → status='failed' (аудит-лог; супер админу в /stats)
  5. sleep до следующего цикла; graceful shutdown по ctx.Done (ждём текущий batch)
```

Устойчивость к рестарту: состояния только в БД; незавершённые локи снимаются п.1; `sent` не переотправляется. Идемпотентность доставки: `MarkSent` через `UPDATE … WHERE status='pending'` — если два воркера чудом взяли один job, второй UPDATE вернёт 0 строк и отправку подавит (дополнительно: отправка только держателем лока).

Backoff: 30s, 2m, 10m, 30m, 1h. Для ошибок 429 — уважать `retry_after` из ответа Telegram.

### 7.3 Fan-out дублей в ЛС

Job группового напоминания = 1 отправка в чат + N отправок в ЛС. Чтобы частичный отказ не терял ЛС: job в чат — родительский; после успеха создаются (в транзакции MarkSent) дочерние `reminders` c `kind='dm_dup'` для участников с включённым флагом (`memberships.dm_notify COALESCE users.dm_notify_default`), `fire_at=now`. Каждый дочерний живёт своим статусом — отказ в одном ЛС (пользователь не писал боту / заблокировал) не влияет на остальных. Пользователям, никогда не запускавшим бота, не отправляем (нет chat-а) — проверяется по наличию записи в users и флагу `bot_blocked` (обновляется по ошибкам 403 от Telegram).

### 7.4 Rate limits Telegram

Глобальный токеновый бакет в Notifier: ~25 msg/s (лимит 30), per-chat бакет: 18/мин (лимит 20), при 429 — пауза на `retry_after` для конкретного chat_id. Реализация: `golang.org/x/time/rate` на канал; блокировка перед Send.

## 8. Конфигурация (env)

```
BOT_TOKEN, BOT_API_BASE(опц., локальный сервер), WEBHOOK_URL|POLLING_MODE=long_polling,
WEBHOOK_SECRET, WEBHOOK_PORT
DATABASE_URL, DB_POOL_MAX=10
HTTP_ADDR=:8080           # адрес HTTP-сервера serve: REST API + TMA + /webhook + /healthz
APP_PUBLIC_URL            # базовый URL TMA (https обязателен для Telegram)
SESSION_TTL_DAYS=30, AUTH_DATE_MAX_AGE_HOURS=24
SCHED_POLL_INTERVAL=10s, SCHED_BATCH=50, SCHED_LOCK_TTL=2m, SCHED_MAX_ATTEMPTS=5
                           # FinalizeTimeout воркера — 75s (фиксирован в коде): нотификатор
                           # выдерживает 429 retry_after до 60s внутри себя
CLEANUP_INTERVAL=1h        # период cleanup-джобы: pending-TTL, счётчики, сессии
COUNTER_RETENTION=192h     # retention окон rate-limit-счётчиков; строго > 168h (недельное окно)
TG_RATE_GLOBAL=25, TG_RATE_PER_CHAT=18
GROUP_PENDING_TTL_DAYS=14
LIMIT_GROUP_CREATE_DAY=3, LIMIT_GROUP_CREATE_WEEK=5, LIMIT_CLAIM_PER_CHAT_HOUR=3
CLAIM_CODE_TTL=10m, INVITE_DEFAULT_TTL_DAYS=7
SLUG_REGEX='^[А-ЯA-Z0-9]+(-[А-ЯA-Z0-9]+)*$'   # charset слага; компилируется на старте
                           # (некомпилируемое значение — отказ Load). Поверх charset
                           # действуют правила Deadliner: длина 3–16, сегменты через
                           # один дефис, минимум одна цифра — при любой SLUG_REGEX.
                           # Применяется в internal/platform/slugprovider (спека §2.2).
DEFAULT_TZ=Europe/Moscow
LOG_LEVEL=info, LOG_FORMAT=json
```

Логирование: `log/slog` (структурированное, JSON в проде), request-id middleware, ctx-проброс.

## 9. TMA: UX/UI (React 18 + Vite + TelegramUI + Tailwind)

**Стек:** React + TypeScript + Vite; UI-кит **`@telegram-apps/telegram-ui`** (официальный кит Telegram-Mini-Apps, MIT) — компоненты AppRoot, Cell, Banner, Card, TabBar, Placeholder, Snackbar, Spinner, Modal/bottom-sheet; точечно Tailwind для раскладки. SDK: `@telegram-apps/sdk-react` (initData, themeParams, viewport, haptics, MainButton/BackButton). Роутинг — хеш-база (`#/groups/123`), стейт — TanStack Query + zustand; fetch-клиент с Bearer-токеном, 401 → re-init через `/auth/telegram`. Статика — в `embed.FS` Go-бинарника.

**Визуальный язык — как у Wallet в Telegram** (по официальному Figma-киту TelegramUI): нижний таб-бар (иконка+подпись), крупные карточки-«балансы» на главном экране, секционные cell-списки (аватар/иконка + заголовок + подпись + шеврон), действия через bottom sheets, скруглённые карточки на фоне themeParams, крупные даты/статусы как фокус, минимум хрома. Тёмная/светлая тема — автоматически из `themeParams`.

**Навигация:** нижний TabBar: «Дедлайны» / «Календарь» / «Группы» / «Настройки».

Экраны:
1. **Онбординг/Авторизация** — автоматически по initData.
2. **Дедлайны (главный)** — hero-карточка «ближайший дедлайн» (крупная дата + обратный отсчёт, как баланс в Wallet), ниже cell-список с фильтром-чипами: «Все / Личные / Групповые», сегменты «Сегодня / 7 дней / Позже / Просрочено». Круглая FAB «+» → bottom sheet с формой.
3. **Календарь** — месячный вид, точки-маркеры на днях с дедлайнами, тап по дню → cell-список дня. (Канбан — v1.1, YAGNI.)
4. **Форма дедлайна (bottom sheet)** — заголовок, описание, тип (личный/группа из моих), дата+время (tz пользователя), блок напоминаний: пресеты-чипы (7д/3д/24ч — по умолчанию активны для групповых) + «добавить своё» (offset или точное время). Submit — через Telegram MainButton. Валидация инлайн.
5. **Группы** — cell-список моих групп (аватар-слаг + роль-бейдж) + поиск/подсказка слага + «Создать группу» + «Ввести инвайт-код». Экран группы: участники, роль, привязка чата, claim-кнопки, дефолтные пресеты.
6. **Настройки** — cell-секции: tz, `dm_notify_default`, per-group переопределение, logout.
7. **Админ-панель группы** — участники (role/kick через swipe-action или меню ячейки), инвайт-коды (создать/отозвать), привязка чата (статус + инструкция), claim (start/revoke), danger zone.

## 10. Деплой

`docker-compose.yml`: сервисы `app` (сборка multi-stage: node для web → go build с embed → distroless/alpine) и `postgres:16-alpine` (volume). `app` на старте гоняет `migrate`, затем `serve`. Env — `.env` файл. Healthcheck: `GET /healthz`. Reverse proxy с TLS (Caddy/Traefik — вне compose, документируем).

## 11. Тестирование

- Unit: use cases (моки портов), валидация initData (векторы из документации Telegram), slug-валидатор, генерация reminders.
- Интеграционные: `repo` против Postgres в testcontainers-go (миграции, SKIP LOCKED конкурентно — 2 воркера, дубли не проходят).
- Scheduler: fake Clock + fake Notifier — сценарии рестарта (stale locks), 429-backoff, fan-out dm_dup.
- HTTP API: `httptest` + реальные репозитории; контракт-тесты DTO.
- Фронтенд: vitest на логику хранилищ/валидацию; ручное тестирование в Telegram dev-окружении.

## 12. Фазы реализации (для Этапа 3)

1. **Каркас:** репозиторий, go.mod, конфиг, slog, миграции (init schema), Docker Compose, healthz.
2. **Telegram-ядро:** бот (polling), /start, /help, menu button; users upsert; привязка чата /bind_group /unbind.
3. **Auth+API:** валидация initData, сессии, /me; каркас httpapi + embed статики (заглушка TMA).
4. **Группы и роли:** создание, поиск, membership, claim-флоу (коды в чат), инвайты, rate limits, cleanup pending.
5. **Дедлайны и reminders:** CRUD, пресеты/кастом, регенерация; scheduler-воркер + rate limiter + dm_dup fan-out.
6. **TMA:** полноценный React SPA на TelegramUI в стиле Wallet (экраны 2–7), интеграция с API.
7. **Superadmin:** CLI + бот-команды, /stats, аудит.
8. **Полировка:** i18n-каталог, тесты (наполнение), документация README, деплой-инструкция.
