# Deadliner

Telegram-бот + Mini App для управления дедлайнами: личные и групповые дедлайны, напоминания, инвайт-коды, привязка к чатам. Go (Clean Architecture), PostgreSQL, React TMA.

## Quickstart

```bash
cp .env.example .env   # заполните BOT_TOKEN и DATABASE_URL
docker compose up -d postgres
go run ./cmd/deadliner serve
```

Режимы: `serve` (бот + API + scheduler), `migrate` (применить миграции), `admin` (админ-операции).

## Фронтенд (Telegram Mini App)

Приложение живёт в `web/`: React 18 + Vite + TypeScript, UI — `@telegram-apps/telegram-ui`, состояние — TanStack Query + zustand, навигация — собственный хеш-роутер (`#/`, `#/calendar`, `#/groups`, `#/settings`).

### Разработка

```bash
cd web
npm install
npm run dev        # http://localhost:5173
```

Dev-сервер проксирует `/api` на Go-бэкенд (`http://localhost:8080`), поэтому последний нужно поднять рядом:

```bash
go run ./cmd/deadliner serve   # или: make build && ./bin/deadliner serve
```

В обычном браузере приложение открывается, но войти не сможет: `initData` выдаёт только клиент Telegram, поэтому экран покажет «откройте приложение из Telegram». Для полноценной отладки откройте Mini App через `@BotFather`-ссылку или tunnel (`APP_PUBLIC_URL`).

Прочие команды: `npm run build` (прод-сборка в `web/dist`), `npm run typecheck` (`tsc --noEmit`), `npm test` (vitest).

### Продакшн-сборка

Бандл встраивается в Go-бинарник через `//go:embed` — каталог `internal/platform/tma/dist` собирается из `web/dist`:

```bash
make web      # npm ci + vite build + cp web/dist → internal/platform/tma/dist
make build    # go build -o bin/deadliner ./cmd/deadliner
```

`internal/platform/tma/dist/index.html` — плейсхолдер в репозитории: без него `go build` падает на `//go:embed` на чистом клоне. Настоящий бандл перезаписывает его при `make web` (и в Docker-сборке).

Один бинарник отдаёт и API, и статику: `tma.Handler()` смонтирован на `/` последним, `index.html` с `Cache-Control: no-cache`, хешированные ассеты из `assets/` — `immutable` (спека §5.3).

### Makefile

| Цель | Действие |
|---|---|
| `make web` | сборка TMA и укладка бандла в `internal/platform/tma/dist` |
| `make build` | `go build -o bin/deadliner ./cmd/deadliner` |
| `make test` | `go test ./...` + `vitest run` |
| `make up` | `docker compose up --build -d` |
| `make fmt` | `gofmt -w cmd internal` |
| `make clean` | удалить артефакты сборки (`dist`, `bin`) |

> **Статус wiring:** режим `serve` пока заглушка (`serve: not implemented`) —
> сборка роутера (REST API + статика TMA) подключается в Task 16. До этого
> `tma.Handler()` проверяется тестами и dev-сборкой, но живьём через бинарник
> не отдаётся. Сам фронтенд в dev-режиме работает от Vite с проксированием
> `/api` на Go-сервер, поэтому для отладки UI это не мешает.

## Администрирование

`deadliner admin` работает только с сервера и с уже применённой схемой (миграции — отдельный режим):

| Команда | Действие |
|---|---|
| `admin promote <telegram_id> [promote-superadmin]` | выдать права супер-админа |
| `admin ban <telegram_id>` | забанить; активные сессии отзываются немедленно |
| `admin unban <telegram_id>` | снять бан |
| `admin delete-group <slug>` | soft-delete группы по слагу (регистр не важен) |
| `admin stats` | пользователи, группы, дедлайны, напоминания (в т.ч. failed), сессии |
| `admin cleanup` | один прогон cleanup: pending-TTL, старые счётчики rate-limit, протухшие сессии |

Коды выхода: `0` — успех, `1` — ошибка операции, `2` — ошибка использования.

Супер-админ дублируется в боте (только в ЛС, проверка `is_superadmin`): `/promote`, `/ban`, `/unban`, `/stats`, `/delete_group`.

Cleanup-джоба идёт в `serve` каждые `CLEANUP_INTERVAL` (дефолт 1ч): pending-группа без привязки чата и без админов удаляется после `GROUP_PENDING_TTL_DAYS` (14 дней), её pending-напоминания гасятся; попутно чистятся окна rate-limit-счётчиков старше `COUNTER_RETENTION` (дефолт 192ч — строго больше недельного окна лимита, иначе живая строка недельного счётчика удалялась бы) и сессии, истёкшие более 7 дней назад.

> Заготовка; подробная документация появится по мере реализации.
