-- name: UpsertArtist :one
INSERT INTO music.artists (id, navidrome_id, name)
VALUES (@id, @navidrome_id, @name)
ON CONFLICT (navidrome_id) DO UPDATE SET name = EXCLUDED.name, updated_at = now()
    WHERE music.artists.name IS DISTINCT FROM EXCLUDED.name
RETURNING id;

-- name: ArtistIDByNavidrome :one
SELECT id FROM music.artists WHERE navidrome_id = @navidrome_id;

-- name: UpsertAlbum :one
INSERT INTO music.albums (id, navidrome_id, artist_id, title, year, cover_art_id)
VALUES (@id, @navidrome_id, sqlc.narg(artist_id), @title, @year, @cover_art_id)
ON CONFLICT (navidrome_id) DO UPDATE SET
    artist_id = EXCLUDED.artist_id, title = EXCLUDED.title, year = EXCLUDED.year,
    cover_art_id = EXCLUDED.cover_art_id, updated_at = now()
RETURNING id;

-- name: UpsertTrack :one
-- mb_recording_id (file tag or acquired-file size match) is attached only when no
-- other row owns it yet, so a MusicBrainz search never duplicates a library track.
INSERT INTO music.tracks (id, navidrome_id, album_id, artist_id, title, artist_name, album_title, track_no,
                          duration_sec, cover_art_id, content_type, size_bytes, bitrate, genre, year, synced_at,
                          mb_recording_id, mb_release_group_id)
VALUES (@id, @navidrome_id, sqlc.narg(album_id), sqlc.narg(artist_id), @title, @artist_name, @album_title, @track_no,
        @duration_sec, @cover_art_id, @content_type, @size_bytes, @bitrate, @genre, sqlc.narg(year), @synced_at,
        (SELECT sqlc.narg(mb_recording_id)::uuid WHERE NOT EXISTS (
            SELECT 1 FROM music.tracks x WHERE x.mb_recording_id = sqlc.narg(mb_recording_id)::uuid)),
        sqlc.narg(mb_release_group_id))
ON CONFLICT (navidrome_id) DO UPDATE SET
    mb_recording_id = COALESCE(music.tracks.mb_recording_id, EXCLUDED.mb_recording_id),
    mb_release_group_id = COALESCE(music.tracks.mb_release_group_id, EXCLUDED.mb_release_group_id),
    album_id = EXCLUDED.album_id, artist_id = EXCLUDED.artist_id, title = EXCLUDED.title,
    artist_name = EXCLUDED.artist_name, album_title = EXCLUDED.album_title, track_no = EXCLUDED.track_no,
    duration_sec = EXCLUDED.duration_sec, cover_art_id = EXCLUDED.cover_art_id,
    content_type = EXCLUDED.content_type, size_bytes = EXCLUDED.size_bytes, bitrate = EXCLUDED.bitrate,
    genre = EXCLUDED.genre, year = EXCLUDED.year,
    synced_at = EXCLUDED.synced_at, deleted_at = NULL, updated_at = now()
RETURNING id;

-- name: RevertUnsyncedToRemote :execrows
-- A vanished file whose MusicBrainz recording is known becomes a placeholder
-- again (same UUID: likes/playlists survive, playing re-acquires it).
UPDATE music.tracks SET navidrome_id = NULL, size_bytes = 0, bitrate = 0,
    cover_art_id = CASE WHEN mb_release_group_id IS NULL THEN cover_art_id ELSE 'caa-' || mb_release_group_id::text END,
    updated_at = now()
WHERE synced_at < @synced_before AND deleted_at IS NULL AND navidrome_id IS NOT NULL AND mb_recording_id IS NOT NULL;

-- name: MarkUnsyncedTracksDeleted :execrows
UPDATE music.tracks SET deleted_at = now(), updated_at = now()
WHERE synced_at < @synced_before AND deleted_at IS NULL AND navidrome_id IS NOT NULL;

-- name: CountActiveTracks :one
SELECT count(*) FROM music.tracks WHERE deleted_at IS NULL AND navidrome_id IS NOT NULL;

-- name: TrackForStream :one
SELECT id, navidrome_id, content_type, size_bytes, mb_recording_id, mb_release_group_id, title,
       coalesce(youtube_video_id, '')::text AS youtube_video_id
FROM music.tracks
WHERE id = @id AND deleted_at IS NULL;

-- name: CoverExists :one
SELECT EXISTS (SELECT 1 FROM music.tracks WHERE cover_art_id = @cover_art_id AND deleted_at IS NULL);

-- name: ListPopular :many
SELECT t.id, t.title, t.artist_name, t.album_title, t.cover_art_id, t.duration_sec,
       (l.user_id IS NOT NULL)::boolean AS liked,
       coalesce(p.score, 0)::double precision AS score
FROM music.tracks t
LEFT JOIN music.track_popularity p ON p.track_id = t.id
LEFT JOIN music.likes l ON l.user_id = @user_id AND l.track_id = t.id
WHERE t.deleted_at IS NULL AND t.navidrome_id IS NOT NULL
  AND (sqlc.narg(cursor_score)::double precision IS NULL
       OR (coalesce(p.score, 0), t.id) < (sqlc.narg(cursor_score)::double precision, sqlc.narg(cursor_id)::uuid))
ORDER BY coalesce(p.score, 0) DESC, t.id DESC
LIMIT @lim;

-- name: SearchTracks :many
-- word_similarity (pg_trgm, GIN) for fuzzy matches + ILIKE for short substrings.
SELECT t.id, t.title, t.artist_name, t.album_title, t.cover_art_id, t.duration_sec,
       (l.user_id IS NOT NULL)::boolean AS liked, (t.navidrome_id IS NULL)::boolean AS remote,
       word_similarity(@q::text, t.search_text)::double precision AS score
FROM music.tracks t
LEFT JOIN music.likes l ON l.user_id = @user_id AND l.track_id = t.id
WHERE t.deleted_at IS NULL
  AND (@q::text <% t.search_text OR t.search_text LIKE @pattern::text)
  AND (sqlc.narg(cursor_score)::double precision IS NULL
       OR (word_similarity(@q::text, t.search_text)::double precision, t.id)
          < (sqlc.narg(cursor_score)::double precision, sqlc.narg(cursor_id)::uuid))
ORDER BY word_similarity(@q::text, t.search_text)::double precision DESC, t.id DESC
LIMIT @lim;

-- name: TracksByIDs :many
SELECT t.id, (t.navidrome_id IS NULL)::boolean AS remote, coalesce(t.artist_id, '00000000-0000-0000-0000-000000000000'::uuid)::uuid AS artist_id, t.title, t.artist_name, t.album_title, t.cover_art_id, t.duration_sec,
       (l.user_id IS NOT NULL)::boolean AS liked
FROM music.tracks t
LEFT JOIN music.likes l ON l.user_id = @user_id AND l.track_id = t.id
WHERE t.id = ANY(@ids::uuid[]) AND t.deleted_at IS NULL;

-- name: WaveCandidates :many
-- "My wave" MVP (spec §4.1, §5.4): popularity + likes + artist affinity + recency.
SELECT t.id, coalesce(t.artist_id, '00000000-0000-0000-0000-000000000000'::uuid)::uuid AS artist_id,
       coalesce(p.score, 0)::double precision AS popularity,
       (l.user_id IS NOT NULL)::boolean AS liked,
       EXISTS (SELECT 1 FROM music.likes l2 JOIN music.tracks t2 ON t2.id = l2.track_id
               WHERE l2.user_id = @user_id AND t2.artist_id = t.artist_id)::boolean AS liked_artist,
       r.played_at AS last_played
FROM music.tracks t
LEFT JOIN music.track_popularity p ON p.track_id = t.id
LEFT JOIN music.likes l ON l.user_id = @user_id AND l.track_id = t.id
LEFT JOIN music.recent_plays r ON r.user_id = @user_id AND r.track_id = t.id
WHERE t.deleted_at IS NULL AND t.navidrome_id IS NOT NULL
ORDER BY coalesce(p.score, 0) DESC, t.id
LIMIT @lim;

-- name: RefreshPopularity :exec
REFRESH MATERIALIZED VIEW CONCURRENTLY music.track_popularity;

-- name: EnsurePlayEventsPartition :one
SELECT music.ensure_play_events_partition(@month::date)::text;

-- name: ExportCatalog :many
-- Internal catalog export for reco-service (keyset by id).
SELECT t.id, t.title, coalesce(t.artist_id, '00000000-0000-0000-0000-000000000000'::uuid)::uuid AS artist_id,
       t.artist_name, t.album_id, t.album_title, t.genre, t.year, t.duration_sec,
       coalesce(p.score, 0)::double precision AS popularity, t.created_at
FROM music.tracks t
LEFT JOIN music.track_popularity p ON p.track_id = t.id
WHERE t.deleted_at IS NULL AND t.navidrome_id IS NOT NULL AND (sqlc.narg(after)::uuid IS NULL OR t.id > sqlc.narg(after)::uuid)
ORDER BY t.id
LIMIT @lim;

-- ── remote catalog (MusicBrainz placeholders, ADR 0011) ─────────────────────

-- name: UpsertRemoteArtist :one
INSERT INTO music.artists (id, mbid, name) VALUES (@id, @mbid, @name)
ON CONFLICT (mbid) DO UPDATE SET updated_at = music.artists.updated_at
RETURNING id;

-- name: UpsertRemoteAlbum :one
INSERT INTO music.albums (id, mbid, artist_id, title, year, cover_art_id)
VALUES (@id, @mbid, sqlc.narg(artist_id), @title, @year, @cover_art_id)
ON CONFLICT (mbid) DO UPDATE SET artist_id = coalesce(music.albums.artist_id, EXCLUDED.artist_id)
RETURNING id;

-- name: UpsertRemoteTrack :one
-- First write wins for metadata; album/position are filled in when an album
-- view later provides them.
INSERT INTO music.tracks (id, mb_recording_id, mb_release_group_id, mb_artist_id, album_id, artist_id, title,
                          artist_name, album_title, track_no, duration_sec, cover_art_id, synced_at)
VALUES (@id, @mb_recording_id, @mb_release_group_id, sqlc.narg(mb_artist_id), sqlc.narg(album_id), sqlc.narg(artist_id), @title,
        @artist_name, @album_title, @track_no, @duration_sec, @cover_art_id, now())
ON CONFLICT (mb_recording_id) DO UPDATE SET
    album_id = coalesce(music.tracks.album_id, EXCLUDED.album_id),
    track_no = CASE WHEN music.tracks.navidrome_id IS NULL AND music.tracks.track_no = 0 THEN EXCLUDED.track_no ELSE music.tracks.track_no END,
    mb_release_group_id = coalesce(music.tracks.mb_release_group_id, EXCLUDED.mb_release_group_id)
RETURNING id;

-- name: SearchAlbums :many
SELECT a.id, a.title, a.year, a.cover_art_id, coalesce(ar.name, '')::text AS artist_name, a.artist_id, a.mbid,
       (a.navidrome_id IS NULL)::boolean AS remote,
       word_similarity(@q::text, lower(a.title || ' ' || coalesce(ar.name, '')))::double precision AS score
FROM music.albums a LEFT JOIN music.artists ar ON ar.id = a.artist_id
WHERE @q::text <% lower(a.title || ' ' || coalesce(ar.name, '')) OR lower(a.title) LIKE @pattern::text
ORDER BY score DESC, a.id LIMIT @lim;

-- name: SearchArtists :many
SELECT ar.id, ar.name, ar.mbid, (ar.navidrome_id IS NULL)::boolean AS remote,
       word_similarity(@q::text, lower(ar.name))::double precision AS score
FROM music.artists ar
WHERE @q::text <% lower(ar.name) OR lower(ar.name) LIKE @pattern::text
ORDER BY score DESC, ar.id LIMIT @lim;

-- name: AlbumByID :one
SELECT a.id, a.title, a.year, a.cover_art_id, a.mbid, a.artist_id, coalesce(ar.name, '')::text AS artist_name,
       (a.navidrome_id IS NULL)::boolean AS remote, coalesce(a.youtube_browse_id, '')::text AS youtube_browse_id
FROM music.albums a LEFT JOIN music.artists ar ON ar.id = a.artist_id WHERE a.id = @id;

-- name: AlbumTracks :many
SELECT t.id, t.title, t.artist_name, t.album_title, t.cover_art_id, t.duration_sec,
       (l.user_id IS NOT NULL)::boolean AS liked, (t.navidrome_id IS NULL)::boolean AS remote
FROM music.tracks t
LEFT JOIN music.likes l ON l.user_id = @user_id AND l.track_id = t.id
WHERE t.album_id = @album_id AND t.deleted_at IS NULL
ORDER BY t.track_no, t.title LIMIT 500;

-- name: ArtistByID :one
SELECT id, name, mbid, (navidrome_id IS NULL)::boolean AS remote,
       coalesce(youtube_browse_id, '')::text AS youtube_browse_id
FROM music.artists WHERE id = @id;

-- name: ArtistAlbums :many
SELECT a.id, a.title, a.year, a.cover_art_id, a.mbid, (a.navidrome_id IS NULL)::boolean AS remote
FROM music.albums a WHERE a.artist_id = @artist_id
ORDER BY a.year DESC, a.title LIMIT 200;

-- name: ArtistTracks :many
SELECT t.id, t.title, t.artist_name, t.album_title, t.cover_art_id, t.duration_sec,
       (l.user_id IS NOT NULL)::boolean AS liked, (t.navidrome_id IS NULL)::boolean AS remote
FROM music.tracks t
LEFT JOIN music.track_popularity p ON p.track_id = t.id
LEFT JOIN music.likes l ON l.user_id = @user_id AND l.track_id = t.id
WHERE t.artist_id = @artist_id AND t.deleted_at IS NULL
ORDER BY (t.navidrome_id IS NOT NULL) DESC, coalesce(p.score, 0) DESC, t.title LIMIT @lim;

-- name: InsertAcquiredFile :exec
INSERT INTO music.acquired_files (path, mb_recording_id, mb_release_group_id, size_bytes)
VALUES (@path, @mb_recording_id, sqlc.narg(mb_release_group_id), @size_bytes)
ON CONFLICT (path) DO UPDATE SET mb_recording_id = EXCLUDED.mb_recording_id,
    mb_release_group_id = EXCLUDED.mb_release_group_id, size_bytes = EXCLUDED.size_bytes;

-- name: AcquiredFileSizes :many
SELECT size_bytes, mb_recording_id, mb_release_group_id FROM music.acquired_files;

-- name: SetAlbumMBID :exec
-- Library album of an acquired release group (unless another album owns the id).
UPDATE music.albums a SET mbid = @mbid, updated_at = now()
WHERE a.id = @id AND a.mbid IS NULL
  AND NOT EXISTS (SELECT 1 FROM music.albums x WHERE x.mbid = @mbid);

-- name: NavidromeTrackExists :one
SELECT EXISTS (SELECT 1 FROM music.tracks WHERE navidrome_id = @navidrome_id);

-- name: ClaimByRecording :one
-- Placeholder whose MusicBrainz recording id matches the file's tag.
UPDATE music.tracks SET navidrome_id = @navidrome_id, updated_at = now()
WHERE mb_recording_id = @mb_recording_id AND navidrome_id IS NULL
RETURNING id, album_id, artist_id;

-- name: ClaimBySize :one
-- Placeholder of a file acquisition-service imported with exactly this size.
UPDATE music.tracks t SET navidrome_id = @navidrome_id, updated_at = now()
WHERE t.navidrome_id IS NULL AND t.mb_recording_id = (
    SELECT f.mb_recording_id FROM music.acquired_files f
    JOIN music.tracks p ON p.mb_recording_id = f.mb_recording_id AND p.navidrome_id IS NULL
    WHERE f.size_bytes = @size_bytes
    ORDER BY f.created_at DESC LIMIT 1)
RETURNING t.id, t.album_id, t.artist_id;

-- name: ClaimAlbum :exec
UPDATE music.albums a SET navidrome_id = @navidrome_id, updated_at = now()
WHERE a.id = @id AND a.navidrome_id IS NULL
  AND NOT EXISTS (SELECT 1 FROM music.albums x WHERE x.navidrome_id = @navidrome_id);

-- name: ClaimArtist :exec
UPDATE music.artists a SET navidrome_id = @navidrome_id, updated_at = now()
WHERE a.id = @id AND a.navidrome_id IS NULL
  AND NOT EXISTS (SELECT 1 FROM music.artists x WHERE x.navidrome_id = @navidrome_id);

-- name: UpsertYouTubeArtist :one
INSERT INTO music.artists (id, name, youtube_browse_id)
VALUES (@id, @name, @youtube_browse_id)
ON CONFLICT (youtube_browse_id) DO UPDATE SET name = EXCLUDED.name, updated_at = now()
RETURNING id;

-- name: UpsertYouTubeAlbum :one
INSERT INTO music.albums (id, artist_id, title, year, cover_art_id, youtube_browse_id)
VALUES (@id, sqlc.narg(artist_id), @title, @year, @cover_art_id, @youtube_browse_id)
ON CONFLICT (youtube_browse_id) DO UPDATE SET
    artist_id = coalesce(music.albums.artist_id, EXCLUDED.artist_id),
    cover_art_id = CASE WHEN music.albums.cover_art_id = '' THEN EXCLUDED.cover_art_id ELSE music.albums.cover_art_id END
RETURNING id;

-- name: UpsertYouTubeTrack :one
INSERT INTO music.tracks (id, album_id, artist_id, title, artist_name, album_title, track_no, duration_sec, cover_art_id, youtube_video_id, synced_at)
VALUES (@id, sqlc.narg(album_id), sqlc.narg(artist_id), @title, @artist_name, @album_title, @track_no, @duration_sec, @cover_art_id, @youtube_video_id, now())
ON CONFLICT (youtube_video_id) DO UPDATE SET
    album_id = coalesce(music.tracks.album_id, EXCLUDED.album_id),
    artist_id = coalesce(music.tracks.artist_id, EXCLUDED.artist_id),
    duration_sec = CASE WHEN music.tracks.duration_sec = 0 THEN EXCLUDED.duration_sec ELSE music.tracks.duration_sec END,
    track_no = CASE WHEN music.tracks.track_no = 0 THEN EXCLUDED.track_no ELSE music.tracks.track_no END,
    cover_art_id = CASE WHEN music.tracks.cover_art_id = '' THEN EXCLUDED.cover_art_id ELSE music.tracks.cover_art_id END
RETURNING id;
