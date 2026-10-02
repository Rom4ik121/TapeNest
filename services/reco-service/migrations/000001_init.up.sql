-- reco-service schema (ADR 0010). Owns everything personalization-related; the
-- catalog is a read-only copy synced from music-service's internal API.
CREATE SCHEMA IF NOT EXISTS reco;

CREATE TABLE reco.tracks (
    id           uuid PRIMARY KEY,               -- = music.tracks.id
    title        text        NOT NULL,
    artist_id    uuid        NOT NULL,
    artist       text        NOT NULL,
    album_id     uuid,
    album        text        NOT NULL DEFAULT '',
    genre        text        NOT NULL DEFAULT '',
    year         integer,
    duration_sec integer     NOT NULL DEFAULT 0,
    popularity   real        NOT NULL DEFAULT 0,  -- normalized 0..1
    created_at   timestamptz NOT NULL,
    synced_at    timestamptz NOT NULL DEFAULT now(),
    deleted_at   timestamptz
);
CREATE INDEX tracks_active_idx ON reco.tracks (id) WHERE deleted_at IS NULL;

CREATE TABLE reco.track_features (
    track_id    uuid PRIMARY KEY REFERENCES reco.tracks (id) ON DELETE CASCADE,
    version     integer     NOT NULL,
    tempo       real        NOT NULL DEFAULT 0,
    energy      real        NOT NULL DEFAULT 0,
    loudness_db real        NOT NULL DEFAULT 0,
    centroid    real        NOT NULL DEFAULT 0,
    flatness    real        NOT NULL DEFAULT 0,
    valence     real        NOT NULL DEFAULT 0,
    raw         real[]      NOT NULL DEFAULT '{}',
    error       text        NOT NULL DEFAULT '',   -- last analysis error (no URLs, see ADR 0010 §8)
    attempts    integer     NOT NULL DEFAULT 0,
    analyzed_at timestamptz NOT NULL DEFAULT now()
);

-- long-term profile: decayed affinity per (user, track) + counters
CREATE TABLE reco.user_tracks (
    user_id       uuid        NOT NULL,
    track_id      uuid        NOT NULL,
    affinity      double precision NOT NULL DEFAULT 0,
    affinity_at   timestamptz NOT NULL,
    plays         integer     NOT NULL DEFAULT 0,
    completes     integer     NOT NULL DEFAULT 0,
    early_skips   integer     NOT NULL DEFAULT 0,
    skips         integer     NOT NULL DEFAULT 0,
    playlist_adds integer     NOT NULL DEFAULT 0,
    liked         boolean     NOT NULL DEFAULT false,
    last_played   timestamptz,
    PRIMARY KEY (user_id, track_id)
);

-- long-term profile: decayed weights per artist / album / genre / audio tag
CREATE TABLE reco.user_taste (
    user_id    uuid        NOT NULL,
    kind       text        NOT NULL CHECK (kind IN ('artist', 'album', 'genre', 'tag')),
    key        text        NOT NULL,
    weight     double precision NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (user_id, kind, key)
);

-- Thompson-sampling arms per user and candidate source
CREATE TABLE reco.source_stats (
    user_id uuid NOT NULL,
    source  text NOT NULL,
    alpha   double precision NOT NULL DEFAULT 0,
    beta    double precision NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, source)
);

CREATE TABLE reco.item_neighbors (
    track_id    uuid NOT NULL,
    source      text NOT NULL CHECK (source IN ('cf', 'content')),
    neighbor_id uuid NOT NULL,
    sim         real NOT NULL,
    PRIMARY KEY (track_id, source, neighbor_id)
);

CREATE TABLE reco.item_factors (
    track_id uuid PRIMARY KEY,
    factors  real[] NOT NULL
);

CREATE TABLE reco.user_factors (
    user_id uuid PRIMARY KEY,
    factors real[] NOT NULL
);

CREATE TABLE reco.state (
    key        text PRIMARY KEY,
    value      text        NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- idempotency of event ingestion (stream entry ids / backfill keys)
CREATE TABLE reco.ingested (
    key text PRIMARY KEY,
    at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ingested_at_idx ON reco.ingested USING brin (at);
