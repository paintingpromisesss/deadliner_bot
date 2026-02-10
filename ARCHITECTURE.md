## 1. Общее описание (Vision)
Проект представляет собой гибрид Telegram-бота и Telegram Mini App (TMA) для управления учебными дедлайнами студенческой группы.
**Основная цель**: Централизованное уведомление о дедлайнах и упрощенное их администрирование через визуальный интерфейс, а не текстовые команды.

### Ключевые особенности
- **Среда обитания**: Бот работает в супергруппе с включенными топиками (форумами).
- **Интерфейс уведомлений**: Отдельный топик "Дедлайны", куда бот пишет напоминания.
- **Интерфейс управления**: Mini App (Web App), открывающееся по кнопке. Позволяет создавать, редактировать и удалять дедлайны, а также загружать файлы.
- **Самоочистка**: В топике настроек сообщения команд и технические ответы удаляются, оставляя чат чистым.
- **Ролевая модель**: Просматривать могут все, редактировать — только администраторы группы.

## 2. Технологический стек
- **Backend Language**: Go 1.22+
- **Web Server**: Fiber или Echo (для API Mini App).
- **Telegram Bot Lib**: `github.com/go-telegram/bot` (поддержка топиков, middleware).
- **Database**: PostgreSQL (Driver: `pgx`, ORM: `GORM`).
- **Frontend**: HTML5 + Vanilla JS + CSS (TailwindCSS через CDN для простоты).
- **Scheduler**: `github.com/robfig/cron/v3`.
- **Config**: `github.com/spf13/viper`.
- **Deployment**: Docker + Docker Compose.

## 3. Структура проекта (File Structure)
student-deadline-bot/
├── cmd/
│   └── bot/
│       └── main.go              # Entry point: запуск HTTP сервера и Bot Pollera
├── internal/
│   ├── config/                  # Загрузка env/yaml конфигов
│   ├── database/                # Подключение к GORM, миграции
│   ├── models/                  # Structs: Deadline, User, File
│   ├── bot/                     # Логика Telegram бота
│   │   ├── handlers.go          # Обработка команд (/start, /settings)
│   │   └── notifications.go     # Логика отправки сообщений в топик
│   ├── api/                     # REST API для Mini App
│   │   ├── server.go            # Роутер (Gin/Fiber)
│   │   ├── handlers.go          # Endpoints (GET/POST deadlines, upload)
│   │   └── auth.go              # Middleware валидации WebAppInitData
│   ├── service/                 # Бизнес-логика
│   │   ├── deadline_service.go  # CRUD дедлайнов
│   │   ├── file_service.go      # Загрузка файлов и получение file_id
│   │   └── reminder_service.go  # Расчет времени напоминаний
│   └── scheduler/               # Cron jobs для проверки напоминаний
├── web/                         # Статика для Mini App
│   ├── index.html               # SPA приложение
│   ├── script.js                # JS логика (Fetch API, Telegram SDK)
│   └── style.css                # Стили
├── .env                         # Токены и пароли (не в git)
├── Dockerfile
└── docker-compose.yml



## 4. Схема Базы Данных (Database Schema)

### Table: `deadlines`

| Field | Type | Description |
| :-- | :-- | :-- |
| `id` | UUID/Int | PK |
| `title` | Varchar | Краткий заголовок (напр. "Лаба по физике") |
| `description` | Text | Подробное описание |
| `deadline_at` | Timestamp | Дата и время сдачи |
| `category` | Varchar | Enum: "Lab", "Exam", "Coursework", "Homework" |
| `chat_id` | Int64 | ID группы (для мультитеннантности в будущем) |
| `topic_id` | Int | ID топика уведомлений |
| `message_id` | Int | ID сообщения с анонсом (чтобы обновлять его) |
| `created_by` | Int64 | Telegram ID создателя |

### Table: `attachments`

| Field | Type | Description |
| :-- | :-- | :-- |
| `id` | UUID/Int | PK |
| `deadline_id` | FK | Ссылка на deadline |
| `file_id` | Varchar | Telegram File ID (для отправки ботом) |
| `file_name` | Varchar | Оригинальное имя файла |
| `file_type` | Varchar | "photo", "document" |

### Table: `reminders`

| Field | Type | Description |
| :-- | :-- | :-- |
| `id` | UUID/Int | PK |
| `deadline_id` | FK | Ссылка на deadline |
| `remind_at` | Timestamp | Время отправки напоминания |
| `is_sent` | Boolean | Флаг отправки |

## 5. API Интерфейс (Backend <-> Mini App)

Все запросы от Mini App должны содержать заголовок `X-Telegram-Init-Data` для валидации.

### `GET /api/deadlines`

- **Query Params**: `chat_id` (берется из start_param).
- **Response**: JSON список актуальных дедлайнов.


### `POST /api/deadlines` (Admin only)

- **Body**: JSON `{ title, description, deadline_at, category, attachments: [] }`
- **Action**:

1. Проверяет права юзера (через `getChatMember` API Телеграма).
2. Сохраняет в БД.
3. Создает расписание напоминаний.
4. **Сразу** отправляет красивое уведомление в топик.


### `POST /api/upload` (Admin only)

- **Body**: Multipart Form Data (File).
- **Action**:

1. Принимает файл.
2. Отправляет его в Telegram (метод `sendDocument` в личный чат с админом или дамп-канал).
3. Получает `file_id` от Telegram.
4. Удаляет временный файл.
5. Возвращает `file_id` фронтенду.


## 6. Логика напоминаний (Reminder System)

Планировщик (Cron) запускается раз в минуту.

1. `SELECT * FROM reminders WHERE remind_at <= NOW() AND is_sent = FALSE`.
2. Для каждого напоминания формирует текст: "🔥 **Напоминание**: [Название] — Срок: [Время]".
3. Отправляет сообщение в топик (`MessageThreadID` обязателен).
4. Помечает `is_sent = TRUE`.

## 7. UX Flow (User Experience)

### Сценарий: Создание дедлайна

1. Админ открывает Mini App.
2. Жмет "+".
3. Заполняет форму, прикрепляет методичку (PDF).
4. Жмет "Создать".
5. Mini App показывает спиннер -> "Успешно" -> Закрывается (через `WebApp.close()`).
6. В чате группы появляется сообщение:
> 📌 **Новый дедлайн: Лаба по Физике**
> 📅 25 Окт, 14:00 (через 5 дней)
> 📎 Файлы: *Metodichka.pdf*
>
> [🔘 Открыть список]

### Сценарий: Просмотр

1. Студент видит уведомление или закреп.
2. Жмет кнопку [Список дедлайнов].
3. Видит веб-интерфейс со списком, отсортированным по срочности.
4. Может скачать файлы прямо из интерфейса (ссылки на `t.me/bot?start=file_id` или прямые ссылки).

## 8. Безопасность

1. **Validation**: Middleware на Go проверяет HMAC подпись `initData` с помощью токена бота.