-- video-editor-service owns schema videoedit. Editing never lives in download.
CREATE SCHEMA IF NOT EXISTS videoedit;

CREATE TABLE videoedit.projects (
    user_id           uuid             NOT NULL,
    id                uuid             NOT NULL,
    source_job_id     uuid             NOT NULL,
    object_key        text             NOT NULL,
    title             text             NOT NULL DEFAULT '',
    duration_sec      double precision NOT NULL DEFAULT 0,
    width             integer          NOT NULL DEFAULT 0,
    height            integer          NOT NULL DEFAULT 0,
    recipe            jsonb            NOT NULL,
    music_key         text             NOT NULL DEFAULT '',
    latest_export_id  uuid,
    created_at        timestamptz      NOT NULL DEFAULT now(),
    updated_at        timestamptz      NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, id),
    UNIQUE (user_id, source_job_id)
);

CREATE TABLE videoedit.exports (
    user_id        uuid        NOT NULL,
    id             uuid        NOT NULL,
    project_id     uuid        NOT NULL,
    status         text        NOT NULL CHECK (status IN ('queued', 'running', 'done', 'failed')),
    recipe         jsonb       NOT NULL,
    output_key     text        NOT NULL DEFAULT '',
    error_message  text        NOT NULL DEFAULT '',
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, id)
);

CREATE INDEX exports_queued_idx ON videoedit.exports (created_at) WHERE status = 'queued';
