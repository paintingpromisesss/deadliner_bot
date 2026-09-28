# Deadliner

Telegram-бот + Mini App для управления дедлайнами: личные и групповые дедлайны, напоминания, инвайт-коды, привязка к чатам. Go (Clean Architecture), PostgreSQL, React TMA.

## Quickstart

```bash
cp .env.example .env   # заполните BOT_TOKEN и DATABASE_URL
docker compose up -d postgres
go run ./cmd/deadliner serve
```

Режимы: `serve` (бот + API + scheduler), `migrate` (применить миграции), `admin` (админ-операции).

> Заготовка; подробная документация появится по мере реализации.
