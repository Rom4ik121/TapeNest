CREATE SCHEMA IF NOT EXISTS streaming;
CREATE EXTENSION IF NOT EXISTS pg_trgm WITH SCHEMA public;

CREATE TABLE streaming.titles (
    id             uuid PRIMARY KEY,
    kind           text NOT NULL CHECK (kind IN ('movie', 'series')),
    title          text NOT NULL,
    original_title text,
    year           integer,
    rating         double precision,
    genres         text[] NOT NULL DEFAULT '{}',
    description    text NOT NULL DEFAULT '',
    runtime_min    integer,
    sort_index     integer NOT NULL UNIQUE,
    search_text    text GENERATED ALWAYS AS (lower(title || ' ' || coalesce(original_title, ''))) STORED,
    created_at     timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX titles_search_trgm ON streaming.titles USING gin (search_text gin_trgm_ops);

CREATE TABLE streaming.media_files (
    id           uuid PRIMARY KEY,
    title_id     uuid NOT NULL REFERENCES streaming.titles (id) ON DELETE CASCADE,
    name         text NOT NULL,
    season       integer,
    episode      integer,
    quality      text NOT NULL,
    size_bytes   bigint NOT NULL,
    duration_sec double precision,
    magnet       text NOT NULL DEFAULT '',
    sort_index   integer NOT NULL,
    UNIQUE (title_id, sort_index)
);

CREATE TABLE streaming.watchlist (
    user_id    uuid NOT NULL,
    title_id   uuid NOT NULL REFERENCES streaming.titles (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, title_id)
);

CREATE TABLE streaming.watch_positions (
    user_id      uuid NOT NULL,
    title_id     uuid NOT NULL,
    file_id      uuid NOT NULL,
    position_sec double precision NOT NULL,
    duration_sec double precision NOT NULL,
    updated_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, title_id, file_id)
);

CREATE INDEX watch_positions_user_updated ON streaming.watch_positions (user_id, updated_at DESC);

CREATE TABLE streaming.sessions (
    id         uuid PRIMARY KEY,
    user_id    uuid NOT NULL,
    title_id   uuid NOT NULL,
    file_id    uuid NOT NULL,
    mode       text NOT NULL,
    err        text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    stopped    boolean NOT NULL DEFAULT false
);

CREATE UNIQUE INDEX sessions_active_file ON streaming.sessions (user_id, file_id) WHERE NOT stopped;

CREATE TABLE streaming.audit_log (
    id         bigserial PRIMARY KEY,
    actor      uuid,
    action     text NOT NULL,
    detail     text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
