CREATE TABLE IF NOT EXISTS users (
    id         SERIAL PRIMARY KEY,
    email      TEXT UNIQUE NOT NULL,
    api_key    TEXT UNIQUE NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS telegram_users (
    id          SERIAL PRIMARY KEY,
    telegram_id BIGINT UNIQUE NOT NULL,
    username    TEXT,
    user_id     INTEGER NOT NULL REFERENCES users(id),
    created_at  TIMESTAMP NOT NULL DEFAULT now()
);
-- очередь заявок на смену email
CREATE TABLE IF NOT EXISTS email_change_requests (
    id           BIGSERIAL PRIMARY KEY,
    user_id      INTEGER      NOT NULL REFERENCES users(id),
    telegram_id  BIGINT       NOT NULL,
    old_email    TEXT         NOT NULL,
    new_email    TEXT         NOT NULL,
    status       TEXT         NOT NULL DEFAULT 'pending', -- pending | approved | rejected
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),
    processed_at TIMESTAMPTZ
);
