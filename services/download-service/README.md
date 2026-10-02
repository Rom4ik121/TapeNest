# download-service

Stage 2: downloads public videos from **YouTube, VK Video and RuTube** with yt-dlp (+ ffmpeg for
muxing, deno as yt-dlp's JS runtime), stores them in MinIO and delivers them to the mini-app (SSE +
presigned links) and to the bot (Redis stream `download:events`). Decisions: ADR 0008.
Contract: [`docs/api/download.openapi.yaml`](../../docs/api/download.openapi.yaml).

Stack: Go 1.22, chi, pgx + sqlc (schema `download`), golang-migrate, go-redis (Streams, ZSET,
semaphores, redsync locks), minio-go.

## Binaries

| Binary | Role |
|---|---|
| `cmd/server` (`download-service`, :8082) | HTTP API behind api-gateway; runs migrations (`MIGRATE_ON_START`, or `-migrate` and exit); creates the bucket with its lifecycle rule |
| `cmd/worker` (`download-worker`, health :8083) | consumes `download:queue:{high,normal,low}`, runs yt-dlp, uploads to MinIO, publishes events; `-import-cookies <source> <file>` |

## Pipeline

1. **Create** (`POST /api/v1/downloads` or `/internal/v1/downloads`):
   - normalize the URL and apply the SSRF guard;
   - reject playlists and unsupported hosts;
   - check quotas (3 active / 30 per 24 h per user);
   - dedup: return the user's active job for the URL, or a stored file as `done` at once
     (with an event for the bot);
   - otherwise insert a job and enqueue it (normal priority).
2. **Worker**:
   - take a per-source semaphore (YT 50 / VK 100 / RuTube 200) and a lock per canonical URL;
   - `yt-dlp -J` probe (rejects live, DRM, over `DOWNLOAD_MAX_DURATION`) → choose a format
     (≤720p mp4, preferring what fits 50 MB at ≥360p);
   - download with a progress parser → stream the file to MinIO → `media` row → job `done`;
   - `video.downloaded` event.
3. **Errors**:
   - stderr is classified into `ErrorKind`;
   - retryable kinds go to a delayed ZSET → `low` queue, with the escalation ladder: direct →
     datacenter → residential → mobile proxy, plus cookies;
   - final kinds fail with `download.failed`.
   - Jobs orphaned by a crashed worker are reclaimed.
4. **Delivery**:
   - `GET /{id}/events` (SSE, a full Job per message);
   - `GET /{id}/file` → 302 to a presigned link on `S3_PUBLIC_URL/media/…` (TTL 1 h);
   - bot delivery is described in [bot-service](../bot-service/README.md).

## Run (dev, no Docker)

```bash
tools/dev/infra-native.sh start      # PG + Redis + MinIO (127.0.0.1:9000, console :9001)
make miniapp-up                       # builds and starts server + worker with the rest of the stack
tools/dev/svc.sh restart download     # rebuild and restart both
tools/dev/e2e-downloads.sh            # public-URL e2e: login → create → SSE → file → dedup
```

Requires `yt-dlp` (≥ 2026.08), `ffmpeg` and `deno` on PATH (or `YTDLP_PATH`, `FFMPEG_LOCATION`).

Docker: `docker build -t tapenest/download-service services/download-service`. One image; run
`download-service` or `download-worker`. Compose services `download-service` and
`download-worker` are in profile `app`. The image is hadolint-clean but was not built here (no
Docker on the dev box).

## Environment

- DB/Redis: `DATABASE_URL`, `REDIS_URL`, `MIGRATE_ON_START`.
- S3: `S3_ENDPOINT`, `S3_ACCESS_KEY`, `S3_SECRET_KEY`, `S3_USE_SSL`, `S3_REGION`,
  `S3_BUCKET_MEDIA` (`media`), `S3_PUBLIC_URL` (public origin; empty → no public links),
  `PRESIGN_TTL` (1h), `MEDIA_RETENTION` (168h), `S3_MULTIPART_THRESHOLD`.
- yt-dlp: `YTDLP_PATH`, `YTDLP_JS_RUNTIMES` (deno), `FFMPEG_LOCATION`, `DOWNLOAD_TMP_DIR`.
- Worker: `DOWNLOAD_WORKER_CONCURRENCY` (2), `DOWNLOAD_JOB_TIMEOUT` (30m), `DOWNLOAD_DRAIN_TIMEOUT`
  (60s), `DOWNLOAD_WORKER_PORT` (8083).
- Limits: `DOWNLOAD_MAX_FILESIZE` (2 GiB), `DOWNLOAD_MAX_DURATION` (3h), `DOWNLOAD_MAX_HEIGHT`
  (720), `DOWNLOAD_MIN_TELEGRAM_HEIGHT` (360), `TELEGRAM_UPLOAD_LIMIT` (50000000),
  `DOWNLOAD_QUOTA_ACTIVE` (3), `DOWNLOAD_QUOTA_DAILY` (30), `DOWNLOAD_LIMIT_{YOUTUBE,VK,RUTUBE}`.
- Network: `PROXY_POOL_{DATACENTER,RESIDENTIAL,MOBILE}` (comma-separated), `PROXY_HEALTH_URL`,
  `PROXY_HEALTH_INTERVAL`, `COOKIES_MAX_AGE` (12h).
- Auth: `INTERNAL_API_TOKEN` (required `X-Internal-Token`).

## Tests

- `make go-test` runs with `-race`. yt-dlp is replaced by a fake script
  (`internal/ytdlp/testdata/fake-yt-dlp`); Postgres, S3 and Redis by in-memory fakes and
  miniredis.
- Integration tests run when `TEST_DATABASE_URL` is set (repo) or when `TEST_S3_ENDPOINT` +
  `TEST_S3_ACCESS_KEY` + `TEST_S3_SECRET_KEY` are set (storage).
- CI checks `sqlc diff`, golangci-lint and the coverage gate (≥70%). Coverage here is 85.7%
  without a DB and 91.5% with a DB.
