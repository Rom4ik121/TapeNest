# api-gateway

The single entry point for mini apps.

It handles:
- Telegram initData auth
- JWT access/refresh
- user registry (`gateway.users`)
- rate limiting, request id, JSON logs and CORS
- routing to backend services

Stack:
- Go 1.22, chi, pgx + sqlc, golang-migrate
- cleanenv, go-redis, golang-jwt, gobreaker

## Run

```bash
make infra-native            # or: make infra-up (Docker)
set -a; . ../../.env; set +a
go run ./cmd/server          # applies migrations on start (MIGRATE_ON_START=true)
go run ./cmd/server -migrate # only apply migrations and exit
```

## Endpoints

Spec: [`docs/api/gateway.openapi.yaml`](../../docs/api/gateway.openapi.yaml).

| Method | Path | Notes |
|---|---|---|
| GET | `/healthz` | liveness |
| GET | `/readyz` | Postgres + Redis ping |
| POST | `/api/v1/auth/telegram` | `{initData}` → `{accessToken, refreshToken, expiresIn, user}`; HMAC-SHA256 per the Telegram spec, `auth_date` ≤ `INITDATA_TTL` (24h), user upsert |
| POST | `/api/v1/auth/refresh` | rotation; reuse → `401 REFRESH_REUSED` and the session is revoked |
| POST | `/api/v1/auth/logout` | revokes the session (refresh family and access tokens) |
| GET | `/api/v1/me` | current user |
| * | `/api/v1/{tracks,playlists,wave,events}/*` | → music-service (`MUSIC_SERVICE_URL`, stage 3) |
| GET, HEAD | `/api/v1/stream/*` | → music-service **without JWT** (signed audio/cover links for `<audio>`/`<img>`; music-service checks the HMAC signature; client `X-User-Id` is stripped; ADR 0009) |
| * | `/api/v1/downloads*` | → download-service (`DOWNLOAD_SERVICE_URL`; SSE passes through; see `download.openapi.yaml`) |
| * | `/api/v1/cinema/*` | → streaming-service |
| POST | `/internal/v1/bot/downloads` | bot-service → gateway (Bearer `INTERNAL_API_TOKEN`) |

Rules for the proxied routes:
- An upstream that is not configured answers `501 NOT_IMPLEMENTED` (the service is not deployed
  yet).
- A failing upstream answers `503 SERVICE_UNAVAILABLE` (breaker). An upstream 503 with
  `X-Degraded-Dependency` (music-service while Navidrome is down) is passed through and is not
  counted as a breaker failure, so the catalog stays reachable.
- The gateway strips `Authorization` and `Cookie` and adds `X-User-Id`, `X-Telegram-Id`,
  `X-User-Role` and `X-Request-Id`. It also drops any client-supplied `X-Internal-Token` and sets
  its own (`INTERNAL_API_TOKEN`), which download-service and music-service require.

Errors are always `{message, code, service?}` (see ADR 0006).

Rate limits (Redis token bucket, fail-open):
- `/auth/*`: per IP, `AUTH_RATE_LIMIT_*`
- other routes: per user, `RATE_LIMIT_*`

## Environment

`APP_ENV`, `LOG_LEVEL`, `API_GATEWAY_PORT`, `DATABASE_URL`, `REDIS_URL`, `TELEGRAM_BOT_TOKEN`,
`INITDATA_TTL`, `JWT_SECRET`, `JWT_ACCESS_TTL`, `JWT_REFRESH_TTL`, `CORS_ALLOWED_ORIGINS`
(supports `https://*.ngrok-free.app`), `TRUST_PROXY_HEADERS`, `RATE_LIMIT_RPS/BURST`,
`AUTH_RATE_LIMIT_RPS/BURST`, `ADMIN_TELEGRAM_IDS`, `INTERNAL_API_TOKEN`,
`MUSIC_SERVICE_URL`, `DOWNLOAD_SERVICE_URL`, `STREAMING_SERVICE_URL`, `UPSTREAM_TIMEOUT`,
`MIGRATE_ON_START`.

All of them are documented in [`/.env.example`](../../.env.example).

## Tests

```bash
make go-test                                    # unit + race + coverage gate (≥70%)
TEST_DATABASE_URL=… TEST_REDIS_URL=… go test ./...   # + integration against real PG/Redis
```
