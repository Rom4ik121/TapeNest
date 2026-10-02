# ADR 0008 — Downloads: storage, delivery to Telegram, queue and quotas (stage 2)

- Status: Accepted
- Date: 2026-09-25
- Scope: `services/download-service`, `services/bot-service/internal/downloads`, api-gateway `/api/v1/downloads*`

## Context

Stage 2 of the spec: a user sends a YouTube / VK Video / RuTube link to the bot (or pastes it in
the mini-app), the service downloads it with yt-dlp and delivers the video. The spec leaves several
points open or conflicting with Telegram's limits. This ADR records the decisions.

## Decisions

1. **Storage — MinIO, bucket `media`, keys `<source>/<yyyy>/<mm>/<media-id>.<ext>`.** Files are
   streamed from the yt-dlp temp dir to MinIO (multipart above `S3_MULTIPART_THRESHOLD`) and
   expire after `MEDIA_RETENTION` (7 days; bucket lifecycle rule set by `EnsureBucket`). A stored URL is a dedup hit: the next request for it is
   `done` immediately (layer 2), the active job for the same user+URL is reused (layer 1) and a
   Redis lock per canonical URL stops two workers downloading the same video (layer 3).
2. **Download links are presigned on the public origin (`S3_PUBLIC_URL` + `/media/…`), TTL
   `PRESIGN_TTL` = 1 h.** The signature covers the host, so the service signs with the public
   host and the edge (Vite proxy in dev, nginx in prod) forwards `/media/` to MinIO keeping the
   `Host` header. No extra public port or domain. Caveat: ngrok-free shows its interstitial on
   the first browser visit; production (own domain) is unaffected.
3. **One format profile.** Best mp4 (h264 + aac) ≤ `DOWNLOAD_MAX_HEIGHT` (720p), preferring the
   highest variant that fits `TELEGRAM_UPLOAD_LIMIT` (50 MB) at ≥ `DOWNLOAD_MIN_TELEGRAM_HEIGHT`
   (360p). Example: a 10.5-minute video came out as 480p / 38.8 MB and can be sent as a file.
   Choosing the quality in the bot was rejected for the MVP: it adds a round trip and a state
   machine, and in practice most short clips fit.
4. **Bot delivery respects the Bot API 50 MB upload limit.** ≤ 50 MB → bot-service fetches the
   object via an internal presigned URL and uploads it with `sendVideo` (streaming, with
   duration/size, replying to the user's message; non-mp4 → `sendDocument`). > 50 MB, or when
   Telegram rejects the upload → the status message is edited to show the size and a "Download"
   URL button with the 1 h public link. A local Bot API server (2 GB) is a later option.
5. **Ack-first bot flow.** The bot answers at once with "⏳ Ссылка принята…" and passes that
   message id to the job. Progress (probing → downloading bar → storing → retry) **edits** this
   one message (bot events only on 25 % steps; the SSE stream gets updates every 500 ms), so the chat isn't spammed. If the user deleted it,
   a new message is sent. If the ack itself can't be sent (bot blocked, chat not found), no job
   is created.
6. **Events: Redis Stream `download:events`** (MAXLEN ≈ 10 000) consumed by the `bot-service`
   group. At-least-once delivery: pending entries are re-read on start, XAUTOCLAIM after 2 min,
   and entries are acked only after handling. The upload is deduped with `SETNX
   bot:dl:done:<job>` (24 h). Unreachable chats (403 / "chat not found") drop the event without
   retries.
7. **Queue: Redis Streams `download:queue:{high,normal,low}`** with a consumer group. Retries go
   through a delayed ZSET into `low`. Per-source semaphores are YouTube 50 / VK 100 / RuTube 200.
   A job stuck in `running` after a worker crash is reclaimed (`MarkRunning` accepts
   `running`). Retryable kinds (`rate_limited`, `network`, `bot_check`, `geo_blocked`) climb the
   escalation ladder: direct → datacenter → residential → mobile proxy, plus cookies, with
   backoff. Final kinds (`private`, `drm_protected`, `live`, `unavailable`, `too_large`,
   `unsupported`) fail at once.
8. **Quotas instead of round-robin fairness.** Per user: `DOWNLOAD_QUOTA_ACTIVE` = 3 active jobs
   (429 `QUOTA_ACTIVE`, Retry-After 60) and `DOWNLOAD_QUOTA_DAILY` = 30 jobs / 24 h
   (`QUOTA_DAILY`, Retry-After 3600). This is simpler than per-user fair scheduling and good
   enough at MVP scale; the gateway rate limit still applies.
9. **Proxy pools** (`PROXY_POOL_DATACENTER|RESIDENTIAL|MOBILE`, comma-separated URLs) with a
   health loop in the worker. Empty pools mean direct downloads; the ladder skips missing tiers.
10. **Cookies.** The worker reads Netscape `cookies.txt` per source from Redis
    (`cookies:<source>`, `cookies:<source>:updated_at`) and warns after `COOKIES_MAX_AGE`.
    The Playwright sidecar is **not implemented yet**. `download-worker -import-cookies <source>
    <file>` is the manual stand-in, and the contract is in `tools/cookie-refresher/README.md`.
11. **Safety.** Only public http(s) URLs of the three sources are accepted (normalized; SSRF
    guard rejects private/loopback hosts, `FORBIDDEN_HOST`). Playlists, live streams and DRM are
    rejected; DRM is never bypassed. yt-dlp runs in its own process group and is killed with its
    children on cancel or timeout. Presigned query strings are redacted from errors and logs.
    CSAM hash matching is deferred to stage 5 (moderation).
12. **Packaging.** A single image holds both binaries (`download-service`, `download-worker`) on
    Debian slim with python3, ffmpeg, deno (yt-dlp's JS runtime for YouTube) and the yt-dlp
    zipapp pinned by sha256. Distroless can't be used because deno is glibc-only and yt-dlp needs
    Python. The image is linted with hadolint but **not built on the dev box (no Docker)**. In dev,
    the natives run directly (`make miniapp-up`, MinIO via `tools/dev/infra-native.sh`).

## Consequences

- Downloads work without any extra public endpoint. Links expire after 1 h, and files are kept
  for 7 days.
- Files over 50 MB reach Telegram users only as a link. That is acceptable for the MVP; a local
  Bot API server can lift the limit later.
- VK and RuTube were checked only through unit tests and fixtures, not with live downloads from
  the dev box.
