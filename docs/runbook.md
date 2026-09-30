# Deadliner — runbook

Операционные процедуры: запуск в двух режимах приёма апдейтов, TLS, бэкапы,
миграции, CLI, мониторинг и обновление версии.

Приложение — один процесс `deadliner serve`: Telegram-бот, REST API для TMA,
статика Mini App, воркер напоминаний и cleanup-джоба. Состояние целиком в
PostgreSQL, поэтому процесс не хранит ничего между рестартами, а
`docker compose restart app` безопасен в любой момент.

---

## 1. Быстрый старт

```bash
cp .env.example .env
# заполнить обязательные: BOT_TOKEN, DATABASE_URL (хост postgres), APP_PUBLIC_URL
docker compose up -d
docker compose logs -f app
curl -fsS http://localhost:8080/healthz   # → ok
```

На старте `serve` сам применяет миграции (идемпотентно) и только после этого
принимает трафик. Готовность видна в логе строкой `serve: ready`.

---

## 2. Режимы приёма апдейтов

### 2.1 Long polling (по умолчанию, для разработки)

```dotenv
POLLING_MODE=long_polling
WEBHOOK_URL=
```

Плюсы: не нужен домен и TLS. Минусы: `getUpdates` держит соединение, бот должен
работать в одном экземпляре, задержка — до таймаута long polling.

### 2.2 Webhook (продакшен)

```dotenv
POLLING_MODE=webhook
WEBHOOK_URL=https://deadliner.example.com/webhook
WEBHOOK_SECRET=<32+ случайных символа>
APP_PUBLIC_URL=https://deadliner.example.com
HTTP_ADDR=:8080
```

`WEBHOOK_SECRET` **обязателен**: без него Telegram принимает любой POST на
`/webhook`, то есть подделанные апдейты — включая `/bind_group` в чужом чате.
Пустой секрет — отказ старта (fail-closed), а не предупреждение.

Что делает `serve` в этом режиме:

1. на старте вызывает `setWebhook(WEBHOOK_URL, secret_token)`;
2. принимает апдейты на `POST /webhook` (заголовок
   `X-Telegram-Bot-Api-Secret-Token` проверяет библиотека — несовпадение
   отбрасывается молча);
3. на graceful shutdown вызывает `deleteWebhook` (best-effort, с ошибкой в лог),
   не удаляя недоставленные апдейты: следующий запуск их заберёт.

Проверка регистрации:

```bash
curl -s "https://api.telegram.org/bot$BOT_TOKEN/getWebhookInfo" | jq
# ожидаем: url = WEBHOOK_URL, последний_ошибка_дата отсутствует
```

### 2.3 TLS через Caddy (рекомендуется)

Caddy сам получает и продлевает сертификат Let's Encrypt:

```caddyfile
# /etc/caddy/Caddyfile
deadliner.example.com {
    encode gzip

    # TMA и API
    reverse_proxy 127.0.0.1:8080

    # Webhook (тот же процесс, отдельного маршрута не нужно —
    # путь /webhook обслуживает роутер приложения)
    log {
        output file /var/log/caddy/deadliner.log
    }
}
```

```bash
sudo systemctl reload caddy
```

Требования: DNS A-запись на сервер, открытые 80/443. Telegram требует
**только** порты 443, 80, 88 или 8443 для вебхука и валидный сертификат.

Обновить вебхук вручную (если адрес изменился):

```bash
curl -s -X POST "https://api.telegram.org/bot$BOT_TOKEN/setWebhook" \
  -d "url=https://deadliner.example.com/webhook" \
  -d "secret_token=$WEBHOOK_SECRET" | jq
```

Снять вебхук и вернуться к polling:

```bash
curl -s -X POST "https://api.telegram.org/bot$BOT_TOKEN/deleteWebhook" \
  -d "drop_pending_updates=false" | jq
```

`APP_PUBLIC_URL` обязан быть `https://` — Telegram отклоняет `web_app`-кнопки
с не-HTTPS адресом, и Mini App просто не откроется.

---

## 3. Миграции

```bash
# применить (serve делает это сам на старте)
docker compose run --rm app migrate

# текущая версия
docker compose run --rm app migrate 2>&1 | grep -o 'version=[0-9]*'
```

Миграции встроены в бинарник (`migrations/embed.go`), поэтому внешний том не
нужен. `migrate` не выполняется в подрежиме `admin`: CLI не должен менять схему.

Правило отката: down-миграции существуют для каждой версии, но **бэкап
обязателен** перед откатом — `migrate` умеет только `up` (откат — отдельной
операцией через `db.RunDown` в коде, вручную в проде не предусмотрен).

---

## 4. Бэкап и восстановление

### 4.1 Дамп

```bash
docker compose exec -T postgres pg_dump -U deadliner -Fc deadliner > backup-$(date +%F).dump

# ежедневно в 03:00 (crontab -e)
0 3 * * * cd /srv/deadliner && docker compose exec -T postgres \
  pg_dump -U deadliner -Fc deadliner > /srv/backups/deadliner-$(date +\%F).dump
```

Хранить 30 дней:

```bash
find /srv/backups -name 'deadliner-*.dump' -mtime +30 -delete
```

### 4.2 Восстановление

```bash
docker compose stop app
docker compose exec -T postgres dropdb -U deadliner deadliner
docker compose exec -T postgres createdb -U deadliner deadliner
docker compose exec -T postgres pg_restore -U deadliner -d deadliner --clean --if-exists < backup-2026-10-01.dump
docker compose start app
```

Проверка после восстановления:

```bash
docker compose run --rm app admin stats
```

### 4.3 Что важно в бэкапе

`audit_log` (история действий), `claim_codes` (активные коды), `reminders`
(очередь отправки) и `sessions` — всё в одной БД, отдельного состояния у
процесса нет. Redis/файловых очередей в системе не используется.

---

## 5. CLI администратора

Выполняется на сервере (доступа к БД никому больше нет):

| Команда | Действие |
| --- | --- |
| `deadliner admin promote <telegram_id>` | выдать права супер-админа (синоним `promote-superadmin`) |
| `deadliner admin ban <telegram_id>` | забанить; активные сессии отзываются немедленно |
| `deadliner admin unban <telegram_id>` | снять бан |
| `deadliner admin delete-group <slug>` | soft-delete группы по слагу |
| `deadliner admin stats` | счётчики инстанса (пользователи, группы, дедлайны, очередь напоминаний, сессии) |
| `deadliner admin cleanup` | один прогон cleanup: протухшие pending-группы, окна счётчиков, истёкшие сессии |

Коды выхода: `0` — успех, `1` — ошибка операции, `2` — ошибка использования
(неверная команда/аргумент).

```bash
docker compose exec app deadliner admin stats
docker compose exec app deadliner admin promote 123456789
```

Те же действия из Telegram доступны супер-админу командами `/promote`, `/ban`,
`/unban`, `/stats`, `/delete_group`.

---

## 6. Мониторинг

### 6.1 Healthcheck

```bash
curl -fsS http://localhost:8080/healthz   # → ok (200)
```

Отдаёт сам роутер: ответ означает, что процесс жив и HTTP обслуживается.
Healthcheck контейнера в compose делает то же самое через `wget` каждые 15с
(`start_period` 20s — запас на миграции при холодном старте).

Проверять БД отдельно не нужно: `serve` не стартует, если пул не поднялся, а
ошибки запросов логируются с status=5xx.

### 6.2 Логи

`LOG_FORMAT=json` в проде — строки разбираются `jq`:

```bash
docker compose logs -f app | jq -r 'select(.level=="ERROR") | "\(.msg) \(.error // "")"'

# напоминания, которые не дошли
docker compose logs app | jq -r 'select(.msg=="scheduler: reminder failed permanently") | .reminder_id'
```

Ключевые строки для наблюдения:

| Строка | Значение |
| --- | --- |
| `serve: ready` | процесс поднят, схема применена |
| `scheduler: worker started` | воркер напоминаний в цикле |
| `cleanup: loop started` | cleanup-джоба в цикле |
| `telegram: webhook registered` | `setWebhook` прошёл (webhook-режим) |
| `telegram: setMyCommands failed` | обычно неверный `BOT_TOKEN` — бот не будет работать |
| `scheduler: reminder failed permanently` | напоминание исчерпало 5 попыток |
| `serve: component did not stop within budget` | компонент не уложился в бюджет остановки (стоит проверить БД) |

### 6.3 Статистика

```bash
docker compose exec app deadliner admin stats
```

Из бота: `/stats` (только супер-админ). Показывает глубину очереди
(`reminders_pending`) и счётчик `reminders_failed` — рост последнего означает
проблемы с Telegram (бот удалён из чата, неверные права) или с биндингами.

---

## 7. Обновление версии

```bash
git pull
docker compose build app          # пересобирает SPA и бинарник
docker compose up -d app          # миграции применятся на старте
docker compose logs -f app        # дождаться "serve: ready"
curl -fsS http://localhost:8080/healthz
```

Порядок безопасен благодаря graceful shutdown: при остановке процесс
перестаёт брать запросы, дожидается батча воркера (напоминания помечаются
отправленными, а не переотправляются) и снимает вебхук. Недоставленные
апдейты Telegram остаются в очереди и приходят после старта.

Откат: `git checkout <предыдущий_тег> && docker compose build app && docker compose up -d app`.
Down-миграции автоматически не применяются — при несовместимой схеме сначала
восстанавливают дамп (§4.2).

Перед обновлением:

```bash
docker compose exec -T postgres pg_dump -U deadliner -Fc deadliner > pre-upgrade.dump
```

---

## 8. Диагностика

| Симптом | Причина и действие |
| --- | --- |
| `serve failed: config: missing required env var BOT_TOKEN` | не заполнен `.env` |
| `missing required env var WEBHOOK_SECRET: webhook mode without a secret…` | включён webhook без секрета — заполнить `WEBHOOK_SECRET` |
| `COUNTER_RETENTION=… must exceed the longest rate-limit window (168h…)` | retention уборки счётчиков ≤ недельного окна лимита — поставить `192h`+ |
| `POLLING_MODE="polling" is not a known mode` | опечатка; допустимо `long_polling`, `webhook` или непустой `WEBHOOK_URL` |
| `serve: setWebhook: … unauthorized` | неверный `BOT_TOKEN` |
| `serve: listen :8080: address already in use` | порт занят другим процессом/контейнером |
| бот молчит в группах | бот не админ чата: `/bind_group` требует прав администратора |
| Mini App не открывается | `APP_PUBLIC_URL` не `https://` или домен не проксируется |
| напоминания копятся в `reminders_pending` | воркер не доходит до Telegram: см. `/stats`, логи `scheduler:*` |
| `scheduler: no chat binding` | у группы нет привязанного чата — `/bind_group` в чате группы |
| TMA показывает «Откройте приложение из Telegram» | страница открыта в обычном браузере: вход только по initData |

Проверить webhook, если апдейты не доходят:

```bash
curl -s "https://api.telegram.org/bot$BOT_TOKEN/getWebhookInfo" | jq '.result | {url, last_error_message, pending_update_count}'
```

---

## 9. Остановка и рестарт

```bash
docker compose stop app      # SIGTERM → graceful shutdown (до ~2 мин: воркер
                             # доводит текущий батч, бюджет 90с)
docker compose start app
docker compose down          # остановить всё (том pgdata сохраняется)
docker compose down -v       # ⚠️ вместе с данными
```

SIGINT/SIGTERM обрабатывается одинаково: `Ctrl+C` в `go run` и `docker stop`
ведут себя идентично.