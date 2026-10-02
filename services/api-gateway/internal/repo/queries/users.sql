-- name: UpsertTelegramUser :one
INSERT INTO gateway.users (
    id, telegram_id, first_name, last_name, username, photo_url, language_code, is_premium, role, last_login_at
) VALUES (
    sqlc.arg(id), sqlc.arg(telegram_id), sqlc.arg(first_name), sqlc.narg(last_name), sqlc.narg(username),
    sqlc.narg(photo_url), sqlc.narg(language_code), sqlc.arg(is_premium), sqlc.arg(role), sqlc.narg(last_login_at)
)
ON CONFLICT (telegram_id) DO UPDATE SET
    first_name    = EXCLUDED.first_name,
    last_name     = EXCLUDED.last_name,
    username      = EXCLUDED.username,
    photo_url     = COALESCE(EXCLUDED.photo_url, gateway.users.photo_url),
    language_code = COALESCE(EXCLUDED.language_code, gateway.users.language_code),
    is_premium    = EXCLUDED.is_premium,
    role          = EXCLUDED.role,
    last_login_at = COALESCE(EXCLUDED.last_login_at, gateway.users.last_login_at),
    updated_at    = now()
RETURNING *;

-- name: GetUserByID :one
SELECT * FROM gateway.users WHERE id = $1;

-- name: GetUserByTelegramID :one
SELECT * FROM gateway.users WHERE telegram_id = $1;
