-- name: DeleteNeighbors :exec
DELETE FROM reco.item_neighbors WHERE source = @source;

-- name: InsertNeighbors :copyfrom
INSERT INTO reco.item_neighbors (track_id, source, neighbor_id, sim) VALUES (@track_id, @source, @neighbor_id, @sim);

-- name: ListNeighbors :many
SELECT track_id, source, neighbor_id, sim FROM reco.item_neighbors;

-- name: DeleteItemFactors :exec
DELETE FROM reco.item_factors;

-- name: InsertItemFactors :copyfrom
INSERT INTO reco.item_factors (track_id, factors) VALUES (@track_id, @factors);

-- name: ListItemFactors :many
SELECT track_id, factors FROM reco.item_factors;

-- name: DeleteUserFactors :exec
DELETE FROM reco.user_factors;

-- name: InsertUserFactors :copyfrom
INSERT INTO reco.user_factors (user_id, factors) VALUES (@user_id, @factors);

-- name: GetUserFactors :one
SELECT factors FROM reco.user_factors WHERE user_id = @user_id;

-- name: GetState :one
SELECT value FROM reco.state WHERE key = @key;

-- name: SetState :exec
INSERT INTO reco.state (key, value, updated_at) VALUES (@key, @value, now())
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now();
