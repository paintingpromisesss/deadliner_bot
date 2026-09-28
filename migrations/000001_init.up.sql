-- Deadliner initial schema (spec §4 + sessions from §5.1).
-- All timestamps are timestamptz (UTC); IDs are bigint GENERATED ALWAYS AS IDENTITY.

CREATE TABLE users (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    telegram_id       bigint UNIQUE NOT NULL,
    username          text,
    first_name        text,
    tz                text NOT NULL DEFAULT 'Europe/Moscow',
    dm_notify_default boolean NOT NULL DEFAULT false,
    is_superadmin     boolean NOT NULL DEFAULT false,
    is_banned         boolean NOT NULL DEFAULT false,
    created_at        timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE groups (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    slug             text NOT NULL,
    slug_norm        text NOT NULL UNIQUE,
    title            text NOT NULL,
    status           text NOT NULL DEFAULT 'pending',
    official         boolean NOT NULL DEFAULT false,
    created_by       bigint NOT NULL REFERENCES users(id),
    default_presets  int[] NOT NULL DEFAULT '{10080,4320,1440}',
    claim_expires_at timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    deleted_at       timestamptz
);

CREATE TABLE chat_bindings (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    group_id          bigint UNIQUE NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    chat_id           bigint NOT NULL,
    message_thread_id bigint,
    chat_title        text,
    bound_by          bigint NOT NULL REFERENCES users(id),
    bound_at          timestamptz NOT NULL DEFAULT now(),
    UNIQUE (chat_id, message_thread_id)
);

CREATE TABLE group_memberships (
    group_id  bigint REFERENCES groups(id) ON DELETE CASCADE,
    user_id   bigint REFERENCES users(id) ON DELETE CASCADE,
    role      text NOT NULL DEFAULT 'member',
    dm_notify boolean,
    joined_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (group_id, user_id)
);

CREATE TABLE deadlines (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    group_id      bigint REFERENCES groups(id) ON DELETE CASCADE,
    owner_user_id bigint REFERENCES users(id),
    title         text NOT NULL,
    description   text,
    due_at        timestamptz NOT NULL,
    tz            text NOT NULL DEFAULT 'Europe/Moscow',
    created_by    bigint NOT NULL REFERENCES users(id),
    status        text NOT NULL DEFAULT 'active',
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    deleted_at    timestamptz,
    CHECK ( (group_id IS NULL AND owner_user_id IS NOT NULL)
         OR (group_id IS NOT NULL AND owner_user_id IS NULL) )
);

CREATE INDEX deadlines_group_due_idx
    ON deadlines (group_id, due_at)
    WHERE deleted_at IS NULL AND status = 'active';

CREATE INDEX deadlines_owner_due_idx
    ON deadlines (owner_user_id, due_at)
    WHERE deleted_at IS NULL AND status = 'active';

CREATE TABLE reminders (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    deadline_id    bigint NOT NULL REFERENCES deadlines(id) ON DELETE CASCADE,
    kind           text NOT NULL,
    offset_minutes int,
    fire_at        timestamptz NOT NULL,
    status         text NOT NULL DEFAULT 'pending',
    attempts       int NOT NULL DEFAULT 0,
    locked_by      text,
    locked_at      timestamptz,
    last_error     text,
    sent_at        timestamptz
);

-- Spec asks for UNIQUE (deadline_id, kind, offset_minutes, fire_at) to protect against
-- duplicates on regeneration. offset_minutes is nullable, and NULLs never collide in a
-- plain unique constraint, so it is split into two unique indexes: one covering rows
-- without an offset, and a partial one covering rows with an offset.
CREATE UNIQUE INDEX reminders_deadline_fire_kind_uniq
    ON reminders (deadline_id, fire_at, kind);

CREATE UNIQUE INDEX reminders_deadline_kind_offset_uniq
    ON reminders (deadline_id, kind, offset_minutes)
    WHERE offset_minutes IS NOT NULL;

CREATE INDEX reminders_status_fire_idx
    ON reminders (status, fire_at)
    WHERE status = 'pending';

CREATE TABLE invites (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    group_id   bigint NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    code       text NOT NULL UNIQUE,
    role       text NOT NULL DEFAULT 'member',
    max_uses   int NOT NULL DEFAULT 1,
    used_count int NOT NULL DEFAULT 0,
    created_by bigint NOT NULL REFERENCES users(id),
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz
);

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

CREATE TABLE user_action_counters (
    user_id      bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    action       text NOT NULL,
    window_start timestamptz NOT NULL,
    count        int NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, action, window_start)
);

-- sessions (spec §5.1): opaque bearer tokens, stored as hashes.
CREATE TABLE sessions (
    token_hash text PRIMARY KEY,
    user_id    bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    last_seen  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE outbox_messages (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    topic      text NOT NULL,
    payload    jsonb NOT NULL,
    status     text NOT NULL DEFAULT 'pending',
    created_at timestamptz NOT NULL DEFAULT now(),
    sent_at    timestamptz,
    attempts   int NOT NULL DEFAULT 0
);

CREATE TABLE audit_log (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    actor_user_id bigint,
    action        text NOT NULL,
    target_type   text,
    target_id     bigint,
    meta          jsonb,
    created_at    timestamptz NOT NULL DEFAULT now()
);
