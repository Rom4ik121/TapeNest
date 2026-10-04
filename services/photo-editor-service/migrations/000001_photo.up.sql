CREATE SCHEMA IF NOT EXISTS photo;

CREATE TABLE photo.photos (
    user_id     uuid        NOT NULL,
    id          uuid        NOT NULL,
    object_key  text        NOT NULL,
    title       text        NOT NULL DEFAULT '',
    width       integer     NOT NULL,
    height      integer     NOT NULL,
    mime_type   text        NOT NULL,
    size_bytes  bigint      NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, id)
);

CREATE INDEX photos_user_created_idx ON photo.photos (user_id, created_at DESC, id DESC);

CREATE TABLE photo.exports (
    user_id     uuid        NOT NULL,
    id          uuid        NOT NULL,
    photo_id    uuid        NOT NULL,
    object_key  text        NOT NULL,
    mime_type   text        NOT NULL,
    width       integer     NOT NULL,
    height      integer     NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, id)
);
