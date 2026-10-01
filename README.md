# Deadliner

Telegram Mini App для дедлайнов учебных групп: группа привязывает свой чат,
староста (Group Admin) заводит дедлайны, а бот напоминает о них в чат и в ЛС.
Заработало это или нет — одна команда: `docker compose up -d`.

Один Go-бинарник `deadliner` содержит всё: Telegram-бота, REST API, статику
Mini App (React SPA в `embed.FS`), воркер напоминаний и cleanup-джобу. Единственная
внешняя зависимость — PostgreSQL 16.

---

## Содержание

- [Быстрый старт](#быстрый-старт)
- [Локальная разработка](#локальная-разработка)
- [Переменные окружения](#переменные-окружения)
- [Команды бота](#команды-бота)
- [TMA: разработка](#tma-разработка)
- [CLI администратора](#cli-администратора)
- [Архитектура](#архитектура)
- [Тестирование](#тестирование)
- [Деплой](#деплой)

---

## Быстрый старт

Нужны Docker (с compose) и токен бота от [@BotFather](https://t.me/BotFather).

```bash
git clone <repo> && cd deadliner_bot
cp .env.example .env
```

Заполните в `.env` минимум три переменные:

```dotenv
# Токен от @BotFather
BOT_TOKEN=123456:ABC-DEF...

# Внутри compose хост БД — postgres (см. примечание ниже)
DATABASE_URL=postgres://deadliner:deadliner@postgres:5432/deadliner?sslmode=disable

# https обязателен для Telegram
APP_PUBLIC_URL=https://<ваш-домен>
```

> **Комментарии в `.env` — только отдельными строками.** Парсеры env-файлов
> (`docker compose env_file`, `docker run --env-file`) не считают `#`
> комментарием после пустого значения: строка
> `WEBHOOK_URL=    # https://example/webhook` даёт значение
> `# https://example/webhook` — процесс уйдёт в webhook-режим с мусорным адресом
> и будет падать на `setWebhook`. `cp .env.example .env` этой ошибки не
> содержит: в примере все комментарии — отдельные строки.

> В `docker-compose.yml` `DATABASE_URL` перекрывается на `postgres:5432` внутри
> сети compose, поэтому значение из `.env` (с `localhost`) не мешает запуску.

```bash
docker compose up -d          # первая сборка: SPA + Go-бинарник
docker compose logs -f app    # ждём "serve: ready"
curl -fsS http://localhost:8080/healthz   # → ok
```

Дальше — в Telegram:

1. добавьте бота в чат группы и **выдайте права администратора**;
2. в чате группы: `/bind_group ИКБО-33-21` (слаг группы);
3. откройте Mini App (кнопка меню или `/start` → «Открыть Deadliner»);
4. создайте группу либо введите инвайт-код;
5. «Стать админом» → бот опубликует 6-значный код в чат → введите его в TMA.

Бот по умолчанию работает в long polling — домен и TLS для первого запуска не
нужны. Продакшен-настройка (webhook + Caddy) — в [docs/runbook.md](docs/runbook.md).

---

## Локальная разработка

```bash
# PostgreSQL удобнее поднять из compose
docker compose up -d postgres

cp .env.example .env     # DATABASE_URL с localhost:5432

# SPA собирается в internal/platform/tma/dist и встраивается в бинарник
make web

# запуск: схема применяется автоматически на старте
go run ./cmd/deadliner serve
```

Для живого фронтенда удобнее Vite-дев-сервер с проксированием API:

```bash
cd web && npm install && npm run dev    # http://localhost:5173
go run ./cmd/deadliner serve            # API на :8080
```

Полноценно протестировать вход можно только из Telegram: без `initData`
приложение показывает нейтральный экран «Откройте приложение из Telegram».

```bash
make build        # бинарник в bin/deadliner
make test         # go test ./... + vitest
make fmt          # gofmt по cmd/ и internal/
```

---

## Переменные окружения

Полный список с комментариями — [.env.example](.env.example). Дефолты задаёт
`config.Load`; обязательные помечены ⚠️.

| Переменная | По умолчанию | Назначение |
| --- | --- | --- |
| `BOT_TOKEN` ⚠️ | — | токен бота от @BotFather |
| `DATABASE_URL` ⚠️ | — | строка подключения PostgreSQL |
| `BOT_API_BASE` | — | локальный Bot API сервер (опционально) |
| `POLLING_MODE` | `long_polling` | `long_polling` либо `webhook` |
| `WEBHOOK_URL` ⚠️\* | — | публичный адрес приёма апдейтов; непустое значение включает webhook-режим; обязателен при `POLLING_MODE=webhook` |
| `WEBHOOK_SECRET` ⚠️\* | — | секрет заголовка `X-Telegram-Bot-Api-Secret-Token`; обязателен в webhook-режиме |
| `WEBHOOK_PORT` | `0` | задел (serve слушает `HTTP_ADDR`) |
| `TG_RATE_GLOBAL` | `25` | исходящих сообщений в секунду на процесс |
| `TG_RATE_PER_CHAT` | `18` | сообщений в минуту на один чат |
| `HTTP_ADDR` | `:8080` | адрес HTTP-сервера: API + TMA + `/webhook` + `/healthz` |
| `DB_POOL_MAX` | `10` | размер пула соединений |
| `APP_PUBLIC_URL` | — | базовый URL TMA (Telegram требует `https`) |
| `SESSION_TTL_DAYS` | `30` | TTL сессии, дни (sliding renewal) |
| `AUTH_DATE_MAX_AGE_HOURS` | `24` | максимальный возраст `auth_date` в initData |
| `DEFAULT_TZ` | `Europe/Moscow` | таймзона по умолчанию для новых пользователей |
| `LOG_LEVEL` | `info` | `debug` / `info` / `warn` / `error` |
| `LOG_FORMAT` | `json` | `json` / `text` |
| `SCHED_POLL_INTERVAL` | `10s` | период цикла воркера напоминаний |
| `SCHED_BATCH` | `50` | размер батча за один цикл |
| `SCHED_LOCK_TTL` | `2m` | TTL лока строки напоминания |
| `SCHED_MAX_ATTEMPTS` | `5` | попыток отправки до `failed` |
| `CLEANUP_INTERVAL` | `1h` | период cleanup: pending-TTL, счётчики, сессии |
| `GROUP_PENDING_TTL_DAYS` | `14` | через сколько дней без чата и админа pending-группа удаляется |
| `LIMIT_GROUP_CREATE_DAY` | `3` | лимит создания групп в сутки на пользователя |
| `LIMIT_GROUP_CREATE_WEEK` | `5` | лимит создания групп в неделю на пользователя |
| `LIMIT_CLAIM_PER_CHAT_HOUR` | `3` | запросов claim-кода в час на чат (и на пользователя) |
| `CLAIM_CODE_TTL` | `10m` | срок жизни claim-кода |
| `INVITE_DEFAULT_TTL_DAYS` | `7` | TTL инвайт-кода по умолчанию |
| `SLUG_REGEX` | `^[А-ЯA-Z0-9]+(-[А-ЯA-Z0-9]+)*$` | шаблон слага (плюс валидатор длины и цифры) |
| `COUNTER_RETENTION` | `192h` | retention окон rate-limit-счётчиков; строго больше 168ч (недельное окно), иначе процесс не стартует |

\* `WEBHOOK_URL` и `WEBHOOK_SECRET` обязательны в webhook-режиме
(`POLLING_MODE=webhook` либо непустой `WEBHOOK_URL`). Проверка fail-closed:
без URL бот некуда регистрировать вебхук (процесс стартовал бы и молча не
принимал апдейты), а без секрета Telegram принимает любой `POST /webhook`, то
есть подделанные апдейты.

Дополнительно для compose (приложением не читаются): `POSTGRES_USER`,
`POSTGRES_PASSWORD`, `POSTGRES_DB`, `HTTP_PORT` — порт на хосте.

---

## Команды бота

| Команда | Где | Кто | Действие |
| --- | --- | --- | --- |
| `/start` | ЛС | все | приветствие и кнопка «Открыть Deadliner» |
| `/help` | везде | все | справка по командам |
| `/groups` | везде | все | мои группы и роли |
| `/new_deadline` | ЛС | все | кнопка открытия формы дедлайна |
| `/bind_group <слаг>` | чат группы | админ чата | привязать чат к группе (бот должен быть админом) |
| `/unbind` | чат группы | Group Admin | снять привязку чата |
| `/promote <telegram_id>` | ЛС | супер-админ | выдать права супер-админа |
| `/ban <telegram_id>` | ЛС | супер-админ | забанить (сессии отзываются) |
| `/unban <telegram_id>` | ЛС | супер-админ | снять бан |
| `/stats` | ЛС | супер-админ | счётчики инстанса и очередь напоминаний |
| `/delete_group <слаг>` | ЛС | супер-админ | удалить группу по слагу |

Список команд публикуется при старте: клиентские — всем, `/bind_group` и
`/unbind` — только администраторам чатов (`setMyCommands` в двух scope). Кнопка
меню — `web_app` на `APP_PUBLIC_URL`.

---

## TMA: разработка

Стек: React 18 + TypeScript + Vite, UI-кит `@telegram-apps/telegram-ui`,
тема — из `themeParams` Telegram, роутинг хеш-базированный, состояние —
TanStack Query + zustand.

```
web/src/
  screens/     # дедлайны, календарь, группы, настройки
  components/  # карточки, шиты, таб-бар
  stores/      # auth (сессии), прочие сторы
  lib/         # api-клиент, TMA SDK, календарь, форматирование
```

Сборка попадает в `internal/platform/tma/dist` и встраивается в бинарник:

```bash
make web                 # npm ci + vite build + копирование в embed-каталог
go run ./cmd/deadliner serve   # свежая сборка уже внутри
```

`make web` возвращает отслеживаемый плейсхолдер `dist/index.html` из git, чтобы
собранный бандл не попал в коммит.

Проверки:

```bash
cd web
npm run typecheck    # tsc --noEmit
npm run test         # vitest
npm run build        # прод-сборка
```

Особенности окружения: приложение работает только внутри Telegram — вход идёт
по подписанному `initData` (HMAC на `WebAppData` + токен бота). В обычном
браузере показывается нейтральный экран без кнопки «Повторить»: там нечего
повторять.

---

## CLI администратора

```bash
deadliner admin promote <telegram_id>     # супер-админ (синоним promote-superadmin)
deadliner admin ban <telegram_id>         # бан + отзыв сессий
deadliner admin unban <telegram_id>       # снять бан
deadliner admin delete-group <slug>       # soft-delete группы
deadliner admin stats                     # счётчики инстанса
deadliner admin cleanup                   # один прогон cleanup
```

В compose:

```bash
docker compose exec app deadliner admin stats
docker compose run --rm app migrate       # применить миграции (serve делает это сам)
```

Коды выхода: `0` — успех, `1` — ошибка операции, `2` — ошибка использования.

---

## Архитектура

```
                         Telegram (webhook | long polling)
                                    │
  Mini App (React SPA) ── HTTP ──►  serve: chi-роутер ──► app-сервисы
                                    │                        │
                                    └── scheduler ───────────┤
                                                             ▼
                                              domain (порты) + repo (SQL)
                                                             ▼
                                                       PostgreSQL 16
```

Правило зависимостей: `domain` не импортирует ничего внутреннего, `app` зависит
только от `domain`, `platform` — от `app` + `domain`. Use cases получают порты
через конструкторы, поэтому проверяются юнит-тестами на фейках.

```
cmd/deadliner/          # точка входа, switch по os.Args[1]
internal/
  cmd/                  # подрежимы serve / migrate / admin, сборка графа
  config/               # env-конфиг, все лимиты и TTL
  domain/               # сущности, ошибки, интерфейсы портов
  app/                  # use cases
    auth/ groups/ deadlines/ claims/ notifications/ moderation/
  platform/
    db/                 # pgxpool, миграции (golang-migrate, embed)
    repo/               # SQL-реализации портов
    telegram/           # клиент Bot API, хендлеры, нотификатор, лимитер
    httpapi/            # chi-роутер, контроллеры, DTO, middleware
    scheduler/          # воркер напоминаний, rate limiter
    tma/                # embed.FS со сборкой SPA
  i18n/                 # каталог строк (ru.json)
migrations/             # SQL-миграции (встроены в бинарник)
web/                    # исходники React TMA
```

Ключевые инварианты:

- **Одна доставка.** Воркер берёт напоминания через `FOR UPDATE SKIP LOCKED`,
  а фиксация `sent` отвязана от отмены контекста: graceful shutdown не
  переотправляет уже ушедшее сообщение.
- **Код claim'а виден только в чате.** В БД лежит SHA-256, plaintext существует
  в сообщении и в ответе TMA; при переборе действует лимит попыток.
- **Fail-closed webhook.** Пустой `WEBHOOK_SECRET` или отсутствующий
  `WEBHOOK_URL` в webhook-режиме — отказ старта.
- **Последний админ неприкосновенен.** Понижение и кик проверяются условным SQL,
  поэтому группа не может остаться без администратора.
- **HTML-разметка.** Все пользовательские подстановки в сообщениях проходят
  `i18n.EscapeHTML` (сообщения уходят с `parse_mode=HTML`).

---

## Тестирование

```bash
make test    # go test ./... -count=1 + vitest
```

Или по частям:

```bash
go test ./... -count=1                  # всё, включая интеграционные
go test ./internal/app/... -count=1     # use cases на фейках, без Docker
go test ./internal/integration/...      # сквозной сценарий (testcontainers)
cd web && npm run test                  # фронтенд (vitest)
```

Что покрыто:

- **unit** — use cases на фейковых портах, валидация initData, слаг-валидатор,
  генерация и регенерация напоминаний, троттлинг и backoff;
- **repo** — реальный PostgreSQL в testcontainers: миграции, ограничения,
  конкурентный `SKIP LOCKED`, fan-out дублей;
- **httpapi** — `httptest` поверх реальных репозиториев, контракты DTO;
- **integration** (`internal/integration/e2e_test.go`) — сквозной сценарий:
  вход по initData → создание группы → привязка чата → claim (код в чат) →
  роль admin → групповой дедлайн с напоминанием → прогон воркера (сообщение в
  чат + дубль в ЛС только подписчику) → завершение дедлайна → cleanup не трогает
  живую группу;
- **фронтенд** — vitest на сторах, API-клиент, календарь и экраны.

Интеграционным тестам нужен работающий Docker (поднимаются контейнеры
`postgres:16-alpine`); Go-тесты без Docker — только `./internal/app/...`.

---

## Деплой

Compose-файл поднимает `app` (multi-stage сборка: node → go build → alpine,
не-root пользователь, `tzdata`) и `postgres:16-alpine` с томом `pgdata`.
Healthcheck `app` — `GET /healthz`, `depends_on: postgres: service_healthy`.

Пошаговая инструкция — **[docs/runbook.md](docs/runbook.md)**: webhook и TLS
через Caddy, `setWebhook`/`deleteWebhook`, бэкап и восстановление (`pg_dump`),
миграции, таблица CLI, мониторинг и логи, обновление версии и диагностика
типовых сбоев.
