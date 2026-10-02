-- api-gateway owns schema "gateway" (spec §3.1 #12). UUID v4 ids (spec §5.2).
CREATE SCHEMA IF NOT EXISTS gateway;

CREATE TABLE gateway.users (
    id             uuid        PRIMARY KEY,
    telegram_id    bigint      NOT NULL UNIQUE,
    first_name     text        NOT NULL DEFAULT '',
    last_name      text,
    username       text,
    photo_url      text,
    language_code  text,
    is_premium     boolean     NOT NULL DEFAULT false,
    role           text        NOT NULL DEFAULT 'user' CHECK (role IN ('user', 'admin')),
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    last_login_at  timestamptz
);
