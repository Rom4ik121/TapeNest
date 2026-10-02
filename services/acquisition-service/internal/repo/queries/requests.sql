-- name: RequestByReleaseGroup :one
SELECT * FROM acquisition.requests WHERE release_group_mbid = @release_group_mbid;

-- name: RequestByID :one
SELECT * FROM acquisition.requests WHERE id = @id;

-- name: InsertRequest :exec
INSERT INTO acquisition.requests (id, release_group_mbid, release_mbid, artist_mbid, artist_name, album_title, year, priority, requested_by)
VALUES (@id, @release_group_mbid, sqlc.narg(release_mbid), sqlc.narg(artist_mbid), @artist_name, @album_title, @year, @priority, @requested_by);

-- name: InsertRequestTrack :exec
INSERT INTO acquisition.request_tracks (request_id, recording_mbid, disc, position, title, length_ms, wanted_at)
VALUES (@request_id, @recording_mbid, @disc, @position, @title, @length_ms, sqlc.narg(wanted_at))
ON CONFLICT (request_id, recording_mbid) DO NOTHING;

-- name: AddRequestUser :execrows
INSERT INTO acquisition.request_users (request_id, user_id, reason) VALUES (@request_id, @user_id, @reason)
ON CONFLICT DO NOTHING;

-- name: InsertEvent :exec
INSERT INTO acquisition.events (request_id, kind, user_id, detail) VALUES (@request_id, @kind, sqlc.narg(user_id), @detail);

-- name: CountUserRequestsSince :one
SELECT count(*) FROM acquisition.requests WHERE requested_by = @user_id AND created_at > @since;

-- name: CountUserActive :one
SELECT count(*) FROM acquisition.requests
WHERE requested_by = @user_id AND state IN ('queued', 'searching', 'downloading', 'importing');

-- name: BumpPriority :exec
UPDATE acquisition.requests SET priority = GREATEST(priority, @priority), updated_at = now() WHERE id = @id;

-- name: MarkWanted :exec
UPDATE acquisition.request_tracks SET wanted_at = now() WHERE request_id = @request_id AND recording_mbid = @recording_mbid;

-- name: RequeueFailed :execrows
UPDATE acquisition.requests
SET state = 'queued', error_code = '', attempts = attempts + 1, torrent_hash = '', progress = 0, updated_at = now()
WHERE id = @id AND state = 'failed';

-- name: ClaimQueued :many
-- Moves up to @lim queued requests to "searching" (highest priority first).
UPDATE acquisition.requests SET state = 'searching', started_at = now(), updated_at = now()
WHERE id IN (
    SELECT id FROM acquisition.requests WHERE state = 'queued'
    ORDER BY priority DESC, created_at LIMIT @lim FOR UPDATE SKIP LOCKED
)
RETURNING *;

-- name: CountActive :one
SELECT count(*) FROM acquisition.requests WHERE state IN ('searching', 'downloading', 'importing');

-- name: ListByState :many
SELECT * FROM acquisition.requests WHERE state = @state ORDER BY updated_at LIMIT @lim;

-- name: ResetStaleSearching :execrows
UPDATE acquisition.requests SET state = 'queued', updated_at = now()
WHERE state = 'searching' AND updated_at < @before;

-- name: SetDownloading :exec
UPDATE acquisition.requests
SET state = 'downloading', torrent_hash = @torrent_hash, release_title = @release_title, indexer = @indexer,
    quality = @quality, size_bytes = @size_bytes, seeders = @seeders, file_ext = @file_ext,
    last_progress_at = now(), updated_at = now()
WHERE id = @id;

-- name: SetProgress :exec
UPDATE acquisition.requests
SET progress = @progress, seeders = @seeders,
    last_progress_at = CASE WHEN @advanced::boolean THEN now() ELSE last_progress_at END, updated_at = now()
WHERE id = @id;

-- name: SetState :exec
UPDATE acquisition.requests SET state = @state, error_code = @error_code, updated_at = now(),
    completed_at = CASE WHEN @state IN ('available', 'failed') THEN now() ELSE completed_at END
WHERE id = @id;

-- name: SetLidarrAlbum :exec
UPDATE acquisition.requests SET lidarr_album_id = @lidarr_album_id, updated_at = now() WHERE id = @id;

-- name: SetImportMode :exec
UPDATE acquisition.requests SET import_mode = @import_mode, updated_at = now() WHERE id = @id;

-- name: RequestTracks :many
SELECT * FROM acquisition.request_tracks WHERE request_id = @request_id ORDER BY disc, position;

-- name: SetTrackFile :exec
UPDATE acquisition.request_tracks SET file_index = @file_index, file_name = @file_name, file_size = @file_size
WHERE request_id = @request_id AND recording_mbid = @recording_mbid;

-- name: SetTrackLibraryPath :exec
UPDATE acquisition.request_tracks SET library_path = @library_path
WHERE request_id = @request_id AND recording_mbid = @recording_mbid;

-- name: TrackByRecording :one
-- The most useful request for a recording: one that is not failed, newest first.
SELECT t.*, r.state, r.torrent_hash, r.error_code, r.progress, r.file_ext
FROM acquisition.request_tracks t JOIN acquisition.requests r ON r.id = t.request_id
WHERE t.recording_mbid = @recording_mbid
ORDER BY (r.state = 'failed'), r.created_at DESC
LIMIT 1;

-- name: ListRequests :many
SELECT * FROM acquisition.requests
WHERE (sqlc.narg(state)::text IS NULL OR state = sqlc.narg(state)::text)
ORDER BY updated_at DESC LIMIT @lim;

-- name: RequestUsers :many
SELECT user_id, reason, created_at FROM acquisition.request_users WHERE request_id = @request_id ORDER BY created_at;

-- name: RequestEvents :many
SELECT kind, user_id, detail, at FROM acquisition.events WHERE request_id = @request_id ORDER BY at, id;

-- name: CountByState :many
SELECT state, count(*)::bigint AS n FROM acquisition.requests GROUP BY state;

-- name: RequestByHash :one
SELECT * FROM acquisition.requests WHERE torrent_hash = @torrent_hash ORDER BY updated_at DESC LIMIT 1;
