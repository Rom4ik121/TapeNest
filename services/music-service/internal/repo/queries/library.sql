-- name: ListLiked :many
SELECT t.id, t.title, t.artist_name, t.album_title, t.cover_art_id, t.duration_sec, (t.navidrome_id IS NULL)::boolean AS remote,
       true::boolean AS liked, l.created_at AS sort_at
FROM music.likes l
JOIN music.tracks t ON t.id = l.track_id AND t.deleted_at IS NULL
WHERE l.user_id = @user_id
  AND (sqlc.narg(cursor_at)::timestamptz IS NULL
       OR (l.created_at, l.track_id) < (sqlc.narg(cursor_at)::timestamptz, sqlc.narg(cursor_id)::uuid))
ORDER BY l.created_at DESC, l.track_id DESC
LIMIT @lim;

-- name: ListRecent :many
SELECT t.id, t.title, t.artist_name, t.album_title, t.cover_art_id, t.duration_sec, (t.navidrome_id IS NULL)::boolean AS remote,
       (l.user_id IS NOT NULL)::boolean AS liked, r.played_at AS sort_at
FROM music.recent_plays r
JOIN music.tracks t ON t.id = r.track_id AND t.deleted_at IS NULL
LEFT JOIN music.likes l ON l.user_id = r.user_id AND l.track_id = r.track_id
WHERE r.user_id = @user_id
  AND (sqlc.narg(cursor_at)::timestamptz IS NULL
       OR (r.played_at, r.track_id) < (sqlc.narg(cursor_at)::timestamptz, sqlc.narg(cursor_id)::uuid))
ORDER BY r.played_at DESC, r.track_id DESC
LIMIT @lim;

-- name: LikeTrack :execrows
INSERT INTO music.likes (user_id, track_id)
SELECT @user_id, t.id FROM music.tracks t WHERE t.id = @track_id AND t.deleted_at IS NULL
ON CONFLICT (user_id, track_id) DO NOTHING;

-- name: TrackActive :one
SELECT EXISTS (SELECT 1 FROM music.tracks WHERE id = @id AND deleted_at IS NULL);

-- name: UnlikeTrack :exec
DELETE FROM music.likes WHERE user_id = @user_id AND track_id = @track_id;

-- name: UpsertPosition :execrows
INSERT INTO music.playback_positions (user_id, track_id, position_sec, updated_at)
SELECT @user_id, t.id, @position_sec, @at FROM music.tracks t WHERE t.id = @track_id AND t.deleted_at IS NULL
ON CONFLICT (user_id, track_id) DO UPDATE SET position_sec = EXCLUDED.position_sec, updated_at = EXCLUDED.updated_at;

-- name: GetPosition :one
SELECT track_id, position_sec, updated_at FROM music.playback_positions
WHERE user_id = @user_id AND track_id = @track_id;

-- name: TouchRecent :exec
INSERT INTO music.recent_plays (user_id, track_id, played_at)
SELECT @user_id, t.id, @played_at FROM music.tracks t WHERE t.id = @track_id
ON CONFLICT (user_id, track_id) DO UPDATE SET played_at = GREATEST(music.recent_plays.played_at, EXCLUDED.played_at);

-- name: InsertPlayEvents :execrows
-- Batch from the batcher; unknown tracks are dropped; redelivery is a no-op.
INSERT INTO music.play_events (user_id, track_id, played_at, position_sec, completed, event_id)
SELECT e.user_id, e.track_id, e.played_at, e.position_sec, e.completed, e.event_id
FROM (SELECT unnest(@user_ids::uuid[]) AS user_id, unnest(@track_ids::uuid[]) AS track_id,
             unnest(@played_ats::timestamptz[]) AS played_at, unnest(@positions::real[]) AS position_sec,
             unnest(@completed_flags::boolean[]) AS completed, unnest(@event_ids::text[]) AS event_id) e
JOIN music.tracks t ON t.id = e.track_id
ON CONFLICT (event_id, played_at) DO NOTHING;

-- name: TouchRecentBatch :exec
INSERT INTO music.recent_plays (user_id, track_id, played_at)
SELECT e.user_id, e.track_id, max(e.played_at)
FROM (SELECT unnest(@user_ids::uuid[]) AS user_id, unnest(@track_ids::uuid[]) AS track_id,
             unnest(@played_ats::timestamptz[]) AS played_at) e
JOIN music.tracks t ON t.id = e.track_id
GROUP BY e.user_id, e.track_id
ON CONFLICT (user_id, track_id) DO UPDATE SET played_at = GREATEST(music.recent_plays.played_at, EXCLUDED.played_at);

-- name: ListPlaylists :many
SELECT p.id, p.title, p.created_at, (SELECT count(*) FROM music.playlist_tracks pt WHERE pt.playlist_id = p.id)::bigint AS track_count
FROM music.playlists p
WHERE p.user_id = @user_id
ORDER BY p.created_at DESC, p.id DESC
LIMIT 500;

-- name: CreatePlaylist :one
INSERT INTO music.playlists (user_id, id, title) VALUES (@user_id, @id, @title)
RETURNING id, title, created_at;

-- name: GetPlaylist :one
SELECT p.id, p.title, p.created_at, (SELECT count(*) FROM music.playlist_tracks pt WHERE pt.playlist_id = p.id)::bigint AS track_count
FROM music.playlists p WHERE p.user_id = @user_id AND p.id = @id;

-- name: RenamePlaylist :execrows
UPDATE music.playlists SET title = @title, updated_at = now()
WHERE user_id = @user_id AND id = @id;

-- name: DeletePlaylist :execrows
DELETE FROM music.playlists WHERE user_id = @user_id AND id = @id;

-- name: CountPlaylists :one
SELECT count(*) FROM music.playlists WHERE user_id = @user_id;

-- name: AddPlaylistTrack :execrows
INSERT INTO music.playlist_tracks (user_id, playlist_id, track_id, position)
SELECT p.user_id, p.id, t.id,
       coalesce((SELECT max(pt.position) FROM music.playlist_tracks pt WHERE pt.playlist_id = p.id), 0) + 1
FROM music.playlists p, music.tracks t
WHERE p.user_id = @user_id AND p.id = @playlist_id AND t.id = @track_id AND t.deleted_at IS NULL
ON CONFLICT (playlist_id, track_id) DO NOTHING;

-- name: PlaylistTrackExists :one
SELECT EXISTS (SELECT 1 FROM music.playlist_tracks WHERE playlist_id = @playlist_id AND track_id = @track_id);

-- name: RemovePlaylistTrack :exec
DELETE FROM music.playlist_tracks WHERE user_id = @user_id AND playlist_id = @playlist_id AND track_id = @track_id;

-- name: PlaylistTracks :many
SELECT t.id, t.title, t.artist_name, t.album_title, t.cover_art_id, t.duration_sec, (t.navidrome_id IS NULL)::boolean AS remote,
       (l.user_id IS NOT NULL)::boolean AS liked
FROM music.playlist_tracks pt
JOIN music.tracks t ON t.id = pt.track_id AND t.deleted_at IS NULL
LEFT JOIN music.likes l ON l.user_id = pt.user_id AND l.track_id = t.id
WHERE pt.user_id = @user_id AND pt.playlist_id = @playlist_id
ORDER BY pt.position, pt.added_at
LIMIT 2000;

-- name: ExportLikes :many
SELECT user_id, track_id, created_at FROM music.likes ORDER BY created_at;

-- name: ExportPlaylistAdds :many
SELECT p.user_id, pt.track_id, pt.added_at FROM music.playlist_tracks pt
JOIN music.playlists p ON p.id = pt.playlist_id
ORDER BY pt.added_at;

-- name: ExportPlayEvents :many
SELECT user_id, track_id, played_at, position_sec, completed, event_id FROM music.play_events
WHERE played_at >= @since ORDER BY played_at;
