-- chat_action_counters — rate limits, привязанные к ЧАТУ (спека §3.3:
-- «Лимит claim-кодов: 3/час на чат»). Отдельно от user_action_counters,
-- потому что та keyed by users.id (FK на users) и per-chat счётчик в ней
-- невыразим. FK на чат не нужен: chat_id — идентификатор Telegram, и счётчик
-- может существовать до/после привязки чата к группе.
CREATE TABLE chat_action_counters (
    chat_id      bigint NOT NULL,
    action       text NOT NULL,
    window_start timestamptz NOT NULL,
    count        int NOT NULL DEFAULT 0,
    PRIMARY KEY (chat_id, action, window_start)
);