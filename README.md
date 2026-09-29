# Deadliner

Telegram-бот + Mini App для управления дедлайнами: личные и групповые дедлайны, напоминания, инвайт-коды, привязка к чатам. Go (Clean Architecture), PostgreSQL, React TMA.

## Quickstart

```bash
cp .env.example .env   # заполните BOT_TOKEN и DATABASE_URL
docker compose up -d postgres
go run ./cmd/deadliner serve
```

Режимы: `serve` (бот + API + scheduler), `migrate` (применить миграции), `admin` (админ-операции).

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
