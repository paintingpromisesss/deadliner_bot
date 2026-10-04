-- Откат v2-схемы: claim-таблица восстанавливается пустой (данные
-- невосстановимы), expires_at снова обязателен.
ALTER TABLE invites ALTER COLUMN expires_at SET NOT NULL;

CREATE TABLE claim_codes (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    group_id   bigint NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    code_hash  text NOT NULL,
    chat_id    bigint NOT NULL,
    message_id bigint NOT NULL,
    created_by bigint NOT NULL REFERENCES users(id),
    expires_at timestamptz NOT NULL,
    used_at    timestamptz
);

CREATE INDEX claim_codes_group_unused_idx
    ON claim_codes (group_id)
    WHERE used_at IS NULL;
