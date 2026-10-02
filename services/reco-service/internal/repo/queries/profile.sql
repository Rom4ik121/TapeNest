-- name: GetUserTrack :one
SELECT * FROM reco.user_tracks WHERE user_id = @user_id AND track_id = @track_id FOR UPDATE;

-- name: UpsertUserTrack :exec
INSERT INTO reco.user_tracks (user_id, track_id, affinity, affinity_at, plays, completes, early_skips, skips, playlist_adds, liked, last_played)
VALUES (@user_id, @track_id, @affinity, @affinity_at, @plays, @completes, @early_skips, @skips, @playlist_adds, @liked, sqlc.narg('last_played'))
ON CONFLICT (user_id, track_id) DO UPDATE SET
    affinity = EXCLUDED.affinity, affinity_at = EXCLUDED.affinity_at, plays = EXCLUDED.plays, completes = EXCLUDED.completes,
    early_skips = EXCLUDED.early_skips, skips = EXCLUDED.skips, playlist_adds = EXCLUDED.playlist_adds,
    liked = EXCLUDED.liked, last_played = EXCLUDED.last_played;

-- AddTaste mirrors profile.Accumulate: decay the stored weight to the event time
-- and add the delta; an older event is decayed to the stored time instead.
-- name: AddTaste :exec
INSERT INTO reco.user_taste AS t (user_id, kind, key, weight, updated_at)
VALUES (@user_id, @kind, @key, @delta, @event_at)
ON CONFLICT (user_id, kind, key) DO UPDATE SET
    weight = CASE WHEN EXCLUDED.updated_at >= t.updated_at
        THEN t.weight * power(0.5, extract(epoch FROM (EXCLUDED.updated_at - t.updated_at))::float8 / @half_life_sec::float8) + EXCLUDED.weight
        ELSE t.weight + EXCLUDED.weight * power(0.5, extract(epoch FROM (t.updated_at - EXCLUDED.updated_at))::float8 / @half_life_sec::float8)
    END,
    updated_at = greatest(t.updated_at, EXCLUDED.updated_at);

-- name: ListUserTracks :many
SELECT * FROM reco.user_tracks WHERE user_id = @user_id;

-- name: ListUserTaste :many
SELECT kind, key, weight, updated_at FROM reco.user_taste WHERE user_id = @user_id;

-- name: ListSourceStats :many
SELECT source, alpha, beta FROM reco.source_stats WHERE user_id = @user_id;

-- name: AddSourceStat :exec
INSERT INTO reco.source_stats (user_id, source, alpha, beta) VALUES (@user_id, @source, @alpha, @beta)
ON CONFLICT (user_id, source) DO UPDATE SET alpha = reco.source_stats.alpha + EXCLUDED.alpha, beta = reco.source_stats.beta + EXCLUDED.beta;

-- name: MarkIngested :execrows
INSERT INTO reco.ingested (key) VALUES (@key) ON CONFLICT DO NOTHING;

-- name: CleanupIngested :execrows
DELETE FROM reco.ingested WHERE at < @before;

-- name: PositiveAffinities :many
SELECT user_id, track_id, affinity, affinity_at FROM reco.user_tracks WHERE affinity > 0;

-- name: ProfileCounts :one
SELECT count(*)::bigint AS tracks, COALESCE(sum(plays), 0)::bigint AS plays, count(*) FILTER (WHERE liked)::bigint AS likes,
       COALESCE(sum(early_skips), 0)::bigint AS early_skips
FROM reco.user_tracks WHERE user_id = @user_id;
