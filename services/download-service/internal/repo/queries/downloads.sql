-- name: InsertJob :one
INSERT INTO download.jobs (user_id, id, url, normalized_url, url_hash, source, status, priority,
                           media_id, title, chat_id, status_message_id, reply_to_message_id, lang, finished_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
RETURNING *;

-- name: GetJob :one
SELECT * FROM download.jobs WHERE id = $1;

-- name: GetUserJob :one
SELECT * FROM download.jobs WHERE user_id = $1 AND id = $2;

-- name: ListUserJobs :many
SELECT * FROM download.jobs
WHERE user_id = sqlc.arg(user_id)
  AND (sqlc.narg(before_created)::timestamptz IS NULL
       OR (created_at, id) < (sqlc.narg(before_created)::timestamptz, sqlc.narg(before_id)::uuid))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(lim);

-- name: FindActiveUserJob :one
SELECT * FROM download.jobs
WHERE user_id = $1 AND url_hash = $2 AND status IN ('queued', 'running')
ORDER BY created_at DESC
LIMIT 1;

-- name: CountUserJobs :one
SELECT count(*) FILTER (WHERE status IN ('queued', 'running'))::int AS active,
       count(*) FILTER (WHERE created_at > sqlc.arg(since)::timestamptz)::int AS recent
FROM download.jobs
WHERE user_id = sqlc.arg(user_id) AND (status IN ('queued', 'running') OR created_at > sqlc.arg(since)::timestamptz);

-- name: MarkRunning :one
UPDATE download.jobs
SET status = 'running', stage = $2, attempts = attempts + 1, updated_at = now()
WHERE id = $1 AND status IN ('queued', 'running')
RETURNING *;

-- name: SetStage :exec
UPDATE download.jobs SET stage = sqlc.arg(stage), title = CASE WHEN sqlc.arg(title)::text = '' THEN title ELSE sqlc.arg(title)::text END, updated_at = now()
WHERE id = sqlc.arg(id) AND status = 'running';

-- name: Requeue :one
UPDATE download.jobs
SET status = 'queued', stage = '', priority = $2, error_kind = $3, error_message = $4, updated_at = now()
WHERE id = $1 AND status IN ('queued', 'running')
RETURNING *;

-- name: FinishDone :one
UPDATE download.jobs
SET status = 'done', stage = '', media_id = $2, title = $3, error_kind = '', error_message = '',
    updated_at = now(), finished_at = now()
WHERE id = $1 AND status IN ('queued', 'running')
RETURNING *;

-- name: FinishFailed :one
UPDATE download.jobs
SET status = 'failed', stage = '', error_kind = $2, error_message = $3, updated_at = now(), finished_at = now()
WHERE id = $1 AND status IN ('queued', 'running')
RETURNING *;

-- name: GetMedia :one
SELECT * FROM download.media WHERE id = $1;

-- name: GetMediaByHash :one
SELECT * FROM download.media WHERE url_hash = $1 AND expires_at > now();

-- name: UpsertMedia :one
INSERT INTO download.media (id, url_hash, normalized_url, source, external_id, title, duration_sec, width, height,
                            format_id, object_key, size_bytes, mime_type, thumbnail_url, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
ON CONFLICT (url_hash) DO UPDATE
SET normalized_url = EXCLUDED.normalized_url, title = EXCLUDED.title,
    duration_sec = EXCLUDED.duration_sec, width = EXCLUDED.width, height = EXCLUDED.height,
    format_id = EXCLUDED.format_id, object_key = EXCLUDED.object_key, size_bytes = EXCLUDED.size_bytes,
    mime_type = EXCLUDED.mime_type, thumbnail_url = EXCLUDED.thumbnail_url,
    created_at = now(), expires_at = EXCLUDED.expires_at
RETURNING *;
