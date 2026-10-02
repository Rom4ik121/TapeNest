-- name: UpsertTrack :exec
INSERT INTO reco.tracks (id, title, artist_id, artist, album_id, album, genre, year, duration_sec, popularity, created_at, synced_at, deleted_at)
VALUES (@id, @title, @artist_id, @artist, sqlc.narg('album_id'), @album, @genre, sqlc.narg('year'), @duration_sec, @popularity, @created_at, @synced_at, NULL)
ON CONFLICT (id) DO UPDATE SET
    title = EXCLUDED.title, artist_id = EXCLUDED.artist_id, artist = EXCLUDED.artist, album_id = EXCLUDED.album_id,
    album = EXCLUDED.album, genre = EXCLUDED.genre, year = EXCLUDED.year, duration_sec = EXCLUDED.duration_sec,
    popularity = EXCLUDED.popularity, synced_at = EXCLUDED.synced_at, deleted_at = NULL;

-- name: MarkTracksDeleted :execrows
UPDATE reco.tracks SET deleted_at = now() WHERE deleted_at IS NULL AND synced_at < @synced_before;

-- name: ListActiveTracks :many
SELECT t.id, t.title, t.artist_id, t.artist, t.album_id, t.album, t.genre, t.year, t.duration_sec, t.popularity, t.created_at,
       COALESCE(f.raw, '{}'::real[])::real[] AS raw, COALESCE(f.version, 0)::int AS feature_version
FROM reco.tracks t
LEFT JOIN reco.track_features f ON f.track_id = t.id AND f.error = ''
WHERE t.deleted_at IS NULL
ORDER BY t.id;

-- name: TracksNeedingAnalysis :many
SELECT t.id FROM reco.tracks t
LEFT JOIN reco.track_features f ON f.track_id = t.id
WHERE t.deleted_at IS NULL
  AND (f.track_id IS NULL
       OR f.version < @version::int
       OR (f.error <> '' AND f.attempts < @max_attempts::int AND f.analyzed_at < now() - interval '30 minutes'))
ORDER BY t.created_at DESC, t.id
LIMIT @lim;

-- name: UpsertFeatures :exec
INSERT INTO reco.track_features (track_id, version, tempo, energy, loudness_db, centroid, flatness, valence, raw, error, attempts, analyzed_at)
VALUES (@track_id, @version, @tempo, @energy, @loudness_db, @centroid, @flatness, @valence, @raw::real[], '', 0, now())
ON CONFLICT (track_id) DO UPDATE SET
    version = EXCLUDED.version, tempo = EXCLUDED.tempo, energy = EXCLUDED.energy, loudness_db = EXCLUDED.loudness_db,
    centroid = EXCLUDED.centroid, flatness = EXCLUDED.flatness, valence = EXCLUDED.valence, raw = EXCLUDED.raw,
    error = '', attempts = 0, analyzed_at = now();

-- name: RecordAnalysisError :exec
INSERT INTO reco.track_features (track_id, version, error, attempts, analyzed_at)
VALUES (@track_id, @version, @error, 1, now())
ON CONFLICT (track_id) DO UPDATE SET error = EXCLUDED.error, attempts = reco.track_features.attempts + 1, analyzed_at = now();

-- name: FeatureStats :one
SELECT count(*) FILTER (WHERE f.error = '' AND f.version = @version::int)::bigint AS analyzed,
       count(*) FILTER (WHERE f.error <> '')::bigint AS failed,
       (SELECT count(*) FROM reco.tracks WHERE deleted_at IS NULL)::bigint AS tracks
FROM reco.track_features f JOIN reco.tracks t ON t.id = f.track_id AND t.deleted_at IS NULL;

-- name: TrackKeys :one
SELECT artist_id, album_id, genre FROM reco.tracks WHERE id = @id;
