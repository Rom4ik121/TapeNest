-- name: CatalogHealth :one
SELECT
    (SELECT count(*) FROM music.artists)::bigint AS artists,
    (SELECT count(*) FROM music.albums)::bigint AS albums,
    (SELECT count(*) FROM music.tracks WHERE deleted_at IS NULL)::bigint AS tracks,
    (SELECT count(*) FROM music.tracks WHERE deleted_at IS NOT NULL)::bigint AS deleted_tracks,
    (SELECT count(*) FROM music.tracks WHERE deleted_at IS NULL AND youtube_video_id IS NOT NULL)::bigint AS youtube_tracks,
    (SELECT count(*) FROM music.tracks WHERE deleted_at IS NULL AND youtube_video_id IS NULL)::bigint AS library_tracks,
    ls.last_synced_at
FROM (SELECT 1) AS seed
LEFT JOIN LATERAL (
    SELECT max(synced_at) AS last_synced_at
    FROM music.tracks
    WHERE deleted_at IS NULL
) AS ls ON true;

-- name: AdminUserLikes :many
SELECT t.id, t.title, t.artist_name, t.album_title, l.created_at
FROM music.likes l
JOIN music.tracks t ON t.id = l.track_id
WHERE l.user_id = $1
ORDER BY l.created_at DESC, t.id DESC
LIMIT 40;

-- name: AdminUserRecent :many
SELECT t.id, t.title, t.artist_name, r.played_at
FROM music.recent_plays r
JOIN music.tracks t ON t.id = r.track_id
WHERE r.user_id = $1
ORDER BY r.played_at DESC, t.id DESC
LIMIT 20;

-- name: AdminUserArtists :many
SELECT t.artist_name AS name, count(*)::bigint AS n
FROM music.likes l
JOIN music.tracks t ON t.id = l.track_id
WHERE l.user_id = $1 AND t.artist_name <> ''
GROUP BY t.artist_name
ORDER BY n DESC, t.artist_name
LIMIT 8;

-- name: AdminUserGenres :many
SELECT t.genre AS name, count(*)::bigint AS n
FROM music.likes l
JOIN music.tracks t ON t.id = l.track_id
WHERE l.user_id = $1 AND t.genre <> ''
GROUP BY t.genre
ORDER BY n DESC, t.genre
LIMIT 8;
