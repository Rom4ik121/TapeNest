-- download-service owns schema "download" (spec §3.1 #12). UUID ids, user_id first
-- in user tables (spec §7.8).
CREATE SCHEMA IF NOT EXISTS download;

-- Stored files. One row per canonical URL (dedup layer 2: SHA-256 of the
-- normalized URL). Rows past expires_at are ignored and replaced on re-download;
-- MinIO lifecycle removes the objects (MEDIA_RETENTION).
CREATE TABLE download.media (
    id              uuid        PRIMARY KEY,
    url_hash        text        NOT NULL UNIQUE CHECK (length(url_hash) = 64),
    normalized_url  text        NOT NULL,
    source          text        NOT NULL,
    external_id     text        NOT NULL DEFAULT '',
    title           text        NOT NULL DEFAULT '',
    duration_sec    integer     NOT NULL DEFAULT 0,
    width           integer     NOT NULL DEFAULT 0,
    height          integer     NOT NULL DEFAULT 0,
    format_id       text        NOT NULL,
    object_key      text        NOT NULL,
    size_bytes      bigint      NOT NULL CHECK (size_bytes >= 0),
    mime_type       text        NOT NULL,
    thumbnail_url   text        NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now(),
    expires_at      timestamptz NOT NULL
);

CREATE TABLE download.jobs (
    user_id              uuid        NOT NULL,
    id                   uuid        NOT NULL,
    url                  text        NOT NULL,
    normalized_url       text        NOT NULL,
    url_hash             text        NOT NULL,
    source               text        NOT NULL,
    status               text        NOT NULL CHECK (status IN ('queued', 'running', 'done', 'failed')),
    stage                text        NOT NULL DEFAULT '',
    priority             text        NOT NULL DEFAULT 'normal' CHECK (priority IN ('high', 'normal', 'low')),
    attempts             integer     NOT NULL DEFAULT 0,
    error_kind           text        NOT NULL DEFAULT '',
    error_message        text        NOT NULL DEFAULT '',
    media_id             uuid        REFERENCES download.media (id) ON DELETE SET NULL,
    title                text        NOT NULL DEFAULT '',
    -- bot jobs: where to report progress/result (NULL for mini app jobs)
    chat_id              bigint,
    status_message_id    bigint,
    reply_to_message_id  bigint,
    lang                 text        NOT NULL DEFAULT '',
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now(),
    finished_at          timestamptz,
    PRIMARY KEY (user_id, id)
);
CREATE UNIQUE INDEX jobs_id_key ON download.jobs (id);
-- history (keyset pagination) and daily quota
CREATE INDEX jobs_user_created_idx ON download.jobs (user_id, created_at DESC, id DESC);
-- active quota + "same user, same URL already in progress"
CREATE INDEX jobs_user_active_idx ON download.jobs (user_id, url_hash) WHERE status IN ('queued', 'running');
