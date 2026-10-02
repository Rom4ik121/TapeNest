-- music-service schema (spec §5.4, §7). Owned by this service (spec §3.1 #12).
-- pg_trgm is a trusted extension (PG ≥ 13): the database owner may create it.
CREATE SCHEMA IF NOT EXISTS music;
CREATE EXTENSION IF NOT EXISTS pg_trgm WITH SCHEMA public;

-- ── catalog (filled by the Navidrome sync worker; UUID ids, spec §5.4) ─────────
CREATE TABLE music.artists (
    id           uuid PRIMARY KEY,
    navidrome_id text NOT NULL UNIQUE,
    name         text NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE music.albums (
    id           uuid PRIMARY KEY,
    navidrome_id text NOT NULL UNIQUE,
    artist_id    uuid REFERENCES music.artists (id) ON DELETE SET NULL,
    title        text NOT NULL,
    year         integer NOT NULL DEFAULT 0,
    cover_art_id text NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE music.tracks (
    id           uuid PRIMARY KEY,
    navidrome_id text NOT NULL UNIQUE,
    album_id     uuid REFERENCES music.albums (id) ON DELETE SET NULL,
    artist_id    uuid REFERENCES music.artists (id) ON DELETE SET NULL,
    title        text NOT NULL,
    artist_name  text NOT NULL,
    album_title  text NOT NULL DEFAULT '',
    track_no     integer NOT NULL DEFAULT 0,
    duration_sec integer NOT NULL DEFAULT 0 CHECK (duration_sec >= 0),
    cover_art_id text NOT NULL DEFAULT '',
    content_type text NOT NULL DEFAULT '',
    size_bytes   bigint NOT NULL DEFAULT 0,
    bitrate      integer NOT NULL DEFAULT 0,
    search_text  text GENERATED ALWAYS AS (lower(title || ' ' || artist_name || ' ' || album_title)) STORED,
    synced_at    timestamptz NOT NULL DEFAULT now(),
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    deleted_at   timestamptz
);
CREATE INDEX tracks_search_trgm ON music.tracks USING gin (search_text gin_trgm_ops);
CREATE INDEX tracks_active ON music.tracks (id) WHERE deleted_at IS NULL;

-- ── user data (user_id first, spec §7.8) ─────────────────────────────────────
CREATE TABLE music.likes (
    user_id    uuid NOT NULL,
    track_id   uuid NOT NULL REFERENCES music.tracks (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, track_id)
);
CREATE INDEX likes_user_created ON music.likes (user_id, created_at DESC, track_id DESC);
CREATE INDEX likes_track ON music.likes (track_id);

CREATE TABLE music.playlists (
    user_id    uuid NOT NULL,
    id         uuid PRIMARY KEY,
    title      text NOT NULL CHECK (char_length(title) BETWEEN 1 AND 100),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX playlists_user ON music.playlists (user_id, created_at DESC);

CREATE TABLE music.playlist_tracks (
    user_id     uuid NOT NULL,
    playlist_id uuid NOT NULL REFERENCES music.playlists (id) ON DELETE CASCADE,
    track_id    uuid NOT NULL REFERENCES music.tracks (id) ON DELETE CASCADE,
    position    integer NOT NULL,
    added_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (playlist_id, track_id)
);
CREATE INDEX playlist_tracks_order ON music.playlist_tracks (playlist_id, position);

CREATE TABLE music.playback_positions (
    user_id      uuid NOT NULL,
    track_id     uuid NOT NULL REFERENCES music.tracks (id) ON DELETE CASCADE,
    position_sec double precision NOT NULL CHECK (position_sec >= 0),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, track_id)
);

-- last play per (user, track): "Recently played" + wave recency penalty
CREATE TABLE music.recent_plays (
    user_id   uuid NOT NULL,
    track_id  uuid NOT NULL REFERENCES music.tracks (id) ON DELETE CASCADE,
    played_at timestamptz NOT NULL,
    PRIMARY KEY (user_id, track_id)
);
CREATE INDEX recent_plays_user ON music.recent_plays (user_id, played_at DESC, track_id DESC);

-- ── listening history: monthly RANGE partitions + BRIN (spec §3.1 #8) ─────────
-- Written only by the batcher (Redis Streams → batches). event_id = stream entry id,
-- so redelivered batches are idempotent (ON CONFLICT DO NOTHING).
CREATE TABLE music.play_events (
    user_id      uuid NOT NULL,
    track_id     uuid NOT NULL,
    played_at    timestamptz NOT NULL,
    position_sec real NOT NULL,
    completed    boolean NOT NULL,
    event_id     text NOT NULL,
    UNIQUE (event_id, played_at)
) PARTITION BY RANGE (played_at);
CREATE INDEX play_events_played_brin ON music.play_events USING brin (played_at);
CREATE TABLE music.play_events_default PARTITION OF music.play_events DEFAULT;

-- creates the monthly partition that contains `month` (idempotent); called by the worker
CREATE FUNCTION music.ensure_play_events_partition(month date) RETURNS text
LANGUAGE plpgsql AS $$
DECLARE
    lo   date := date_trunc('month', month)::date;
    hi   date := (date_trunc('month', month) + interval '1 month')::date;
    name text := 'play_events_' || to_char(lo, 'YYYY_MM');
BEGIN
    IF to_regclass('music.' || name) IS NULL THEN
        EXECUTE format('CREATE TABLE music.%I PARTITION OF music.play_events FOR VALUES FROM (%L) TO (%L)', name, lo, hi);
    END IF;
    RETURN name;
END
$$;

SELECT music.ensure_play_events_partition(current_date);
SELECT music.ensure_play_events_partition((current_date + interval '1 month')::date);

-- ── aggregates: refreshed by the Go worker (spec §3.1 #11, §7.9) ──────────────
CREATE MATERIALIZED VIEW music.track_popularity AS
SELECT t.id AS track_id,
       coalesce(p.plays, 0)::bigint     AS plays_30d,
       coalesce(p.completed, 0)::bigint AS completed_30d,
       coalesce(l.likes, 0)::bigint     AS likes,
       (coalesce(p.plays, 0) + 2 * coalesce(p.completed, 0) + 3 * coalesce(l.likes, 0))::double precision AS score
FROM music.tracks t
LEFT JOIN (
    SELECT e.track_id, count(*) AS plays, count(*) FILTER (WHERE e.completed) AS completed
    FROM music.play_events e
    WHERE e.played_at > now() - interval '30 days'
    GROUP BY e.track_id
) p ON p.track_id = t.id
LEFT JOIN (SELECT track_id, count(*) AS likes FROM music.likes GROUP BY track_id) l ON l.track_id = t.id
WHERE t.deleted_at IS NULL;
CREATE UNIQUE INDEX track_popularity_pk ON music.track_popularity (track_id);
CREATE INDEX track_popularity_score ON music.track_popularity (score DESC, track_id DESC);
