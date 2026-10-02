-- acquisition-service schema (ADR 0011). Owned by this service (spec §3.1 #12).
CREATE SCHEMA IF NOT EXISTS acquisition;

-- One request per album (MusicBrainz release group): a second user asking for the
-- same album reuses it (shared library), a track request resolves to its album.
CREATE TABLE acquisition.requests (
    id                 uuid PRIMARY KEY,
    release_group_mbid uuid NOT NULL UNIQUE,
    release_mbid       uuid,
    artist_mbid        uuid,
    artist_name        text NOT NULL,
    album_title        text NOT NULL,
    year               integer NOT NULL DEFAULT 0,
    state              text NOT NULL DEFAULT 'queued'
        CHECK (state IN ('queued', 'searching', 'downloading', 'importing', 'available', 'failed')),
    priority           integer NOT NULL DEFAULT 0,
    error_code         text NOT NULL DEFAULT '',
    torrent_hash       text NOT NULL DEFAULT '',
    release_title      text NOT NULL DEFAULT '',
    indexer            text NOT NULL DEFAULT '',
    quality            text NOT NULL DEFAULT '',
    file_ext           text NOT NULL DEFAULT '',
    size_bytes         bigint NOT NULL DEFAULT 0,
    seeders            integer NOT NULL DEFAULT 0,
    progress           real NOT NULL DEFAULT 0,
    lidarr_album_id    integer,
    import_mode        text NOT NULL DEFAULT '',
    attempts           integer NOT NULL DEFAULT 0,
    requested_by       uuid NOT NULL,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    started_at         timestamptz,
    last_progress_at   timestamptz,
    completed_at       timestamptz
);
CREATE INDEX requests_queue ON acquisition.requests (priority DESC, created_at) WHERE state = 'queued';
CREATE INDEX requests_active ON acquisition.requests (state, updated_at) WHERE state IN ('searching', 'downloading', 'importing');
CREATE INDEX requests_user_created ON acquisition.requests (requested_by, created_at DESC);

-- Tracklist of the chosen release + where each track is in the torrent / library.
CREATE TABLE acquisition.request_tracks (
    request_id     uuid NOT NULL REFERENCES acquisition.requests (id) ON DELETE CASCADE,
    recording_mbid uuid NOT NULL,
    disc           integer NOT NULL DEFAULT 1,
    position       integer NOT NULL,
    title          text NOT NULL,
    length_ms      integer NOT NULL DEFAULT 0,
    file_index     integer,
    file_name      text NOT NULL DEFAULT '',
    file_size      bigint NOT NULL DEFAULT 0,
    library_path   text NOT NULL DEFAULT '',
    wanted_at      timestamptz,
    PRIMARY KEY (request_id, recording_mbid)
);
CREATE INDEX request_tracks_recording ON acquisition.request_tracks (recording_mbid);

-- Who asked and why (quotas + audit, spec §14).
CREATE TABLE acquisition.request_users (
    request_id uuid NOT NULL REFERENCES acquisition.requests (id) ON DELETE CASCADE,
    user_id    uuid NOT NULL,
    reason     text NOT NULL CHECK (reason IN ('play', 'like', 'playlist')),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (request_id, user_id, reason)
);
CREATE INDEX request_users_user ON acquisition.request_users (user_id, created_at DESC);

-- Audit trail of every state change (spec §14 "аудит-трейл").
CREATE TABLE acquisition.events (
    id         bigserial PRIMARY KEY,
    request_id uuid NOT NULL REFERENCES acquisition.requests (id) ON DELETE CASCADE,
    at         timestamptz NOT NULL DEFAULT now(),
    kind       text NOT NULL,
    user_id    uuid,
    detail     jsonb NOT NULL DEFAULT '{}'
);
CREATE INDEX events_request ON acquisition.events (request_id, at);
