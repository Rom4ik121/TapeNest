# music-service

Stage 3: the WavePlayer backend. It provides:
- the catalog, fuzzy search, likes and playlists;
- playback positions and listening history;
- My Wave sessions: batches from **reco-service** ([ADR 0010](../../docs/adr/0010-reco-service-my-wave.md)),
  with the heuristic "wave" as the fallback;
- internal exports and domain events for reco-service;
- audio streaming from **Navidrome** (Subsonic API) with HTTP Range.

The contract is fixed by the frontend: [`docs/api/waveplayer.openapi.yaml`](../../docs/api/waveplayer.openapi.yaml)
plus [`waveplayer-contract.md`](../../docs/api/waveplayer-contract.md). Decisions are in
[ADR 0009](../../docs/adr/0009-music-service-streaming-catalog-wave.md).

Stack: Go 1.22, chi, pgx + sqlc (schema `music`), golang-migrate, go-redis (Streams),
gobreaker.

## Binaries

| Binary | Role |
|---|---|
| `cmd/server` (`music-service`, :8084) | HTTP API behind api-gateway (`X-Internal-Token` + `X-User-Id`). Runs migrations (`MIGRATE_ON_START`, or `-migrate` and exit). Creates the Navidrome admin on a fresh instance. |
| `cmd/worker` (`music-worker`, health :8085) | `play_events` batcher (Redis Stream → PostgreSQL). Navidrome → catalog sync. Monthly partitions. Popularity refresh. |

## How it works

- **Catalog.** Navidrome scans the `/music` volume. Dev fills it with `make music-seed`.
  The worker mirrors it into `music.artists/albums/tracks` (stable UUIDs, soft delete) every
  `CATALOG_SYNC_INTERVAL`.
- **YouTube Music (ADR 0012, amended by 0014).** `MUSIC_SOURCES` defaults to `youtube`.
  Search merges the local library with YouTube Music (songs, albums, artists). A hit is stored
  as a normal track (`youtube_video_id`) so likes persist. Play proxies audio via yt-dlp
  (`player_client=android`) and does not save the file. `MUSIC_SOURCES=library` skips YouTube.
  There is no torrent fallback. Metrolist/InnerTune are not vendored (GPL-3.0).
- **Streaming.**
  - `GET /api/v1/tracks/{id}/stream-url` returns a signed relative URL
    `/api/v1/stream/tracks/{id}?exp&u&sig` (HMAC-SHA256, TTL ≤ 1 h).
  - The gateway forwards `/api/v1/stream/*` without a JWT. This service checks the signature
    and proxies the original file from Navidrome with Range/HEAD (`io.Copy`, no transcoding).
  - Covers are signed without expiry and served `immutable`.
- **Degradation.**
  - Navidrome has its own breaker and health ping. When it is down, `stream-url` and the media
    routes return `503 {code: SERVICE_UNAVAILABLE, service: "streaming"}` with
    `X-Degraded-Dependency: navidrome`, which the gateway does not count against its breaker.
  - Everything else keeps working. `/readyz` shows `navidrome: degraded`.
- **Listens.**
  - `POST /events/track-listened` → `XADD music:play_events` → 204.
  - The worker batches up to 500 rows per insert and acks after commit. It re-reads its pending
    entries, and `XAUTOCLAIM`s from dead consumers.
  - Inserts are idempotent (`event_id` = stream id).
  - `play_events` is RANGE-partitioned by month (current + 2 ahead) with BRIN on `played_at`.
- **Lists.**
  - `recent`: tracks you listened to or saved a position for.
  - `popular`: a materialized view over 30 days (plays + 2·completed + 3·likes), refreshed
    `CONCURRENTLY`.
  - `liked`.
  - search: `pg_trgm` `word_similarity` + substring, GIN.
  - All lists use keyset cursors.
- **Wave.** Sessions live in Redis (6 h): served set, order, artist and source per track, and
  feedback. Each batch of 10 is requested from reco-service (`internal/reco`):
  - one POST per batch, bounded by `RECO_TIMEOUT` (400 ms);
  - a gobreaker opens after `RECO_BREAKER_FAILURES` (3) consecutive failures, for
    `RECO_BREAKER_OPEN` (30 s).
  - On error, open breaker or an empty answer, the **ADR 0009 §6 heuristic** serves the batch:
    popularity, likes, liked artists, recency, session like/skip per artist, jitter; no repeats,
    no same artist twice in a row.
  - `POST /wave/sessions` accepts an optional `{mode}` (`default`, `calm`, `energetic`,
    `discover`, `favorites`). Responses carry `strategy` (`reco` | `fallback`, also in the
    `X-Wave-Strategy` header), and every track carries a `reason` for the "because you liked …"
    caption.
  - When reco returns nothing because everything was served, a new round starts.
- **Events for reco-service.**
  - Listen events keep going to `music:play_events`.
  - Likes/unlikes, playlist adds/removes, wave like/skip (with the source the track was
    served from) and early skips go to `music:user_events`. Early skips come from the new
    public `POST /events/track-skipped`; they never reach `play_events`.
  - Publishing is best effort: a Redis hiccup never fails the user's request.
- **Internal exports** (`X-Internal-Token`, no user), documented in
  [`reco.openapi.yaml`](../../docs/api/reco.openapi.yaml):
  - `GET /internal/v1/catalog` (keyset pages with genre, year and popularity);
  - `GET /internal/v1/interactions?days=N` (NDJSON backfill);
  - `GET /internal/v1/tracks/{id}/audio` (original file, for audio analysis).
- **Genre/year** come from Navidrome (migration `000002_genre`).
- **Replica.** `DB_REPLICA_URL` (optional) serves catalog reads; user data always uses the primary.

## Run (dev, no Docker)

```bash
tools/dev/infra-native.sh up          # PG + Redis + MinIO + Navidrome (127.0.0.1:4533)
make music-seed                       # 16 CC0 recordings (Musopen via Wikimedia Commons) → $MUSIC_DIR
make miniapp-up                       # builds and starts music-service + worker with the rest of the stack
tools/dev/svc.sh restart music        # rebuild and restart both
make e2e-music                        # public-URL e2e: 30 checks (catalog, stream Range, likes, playlists, wave, position, events)
```

Navidrome 0.53.3 runs natively (`~/.local/share/navidrome/navidrome`, `NAVIDROME_BIN`).
Seeded licenses are listed in `$MUSIC_DIR/CREDITS.md`.

Docker: `docker build -t tapenest/music-service services/music-service`. One distroless image
with two entrypoints: `/music-service` (default) and `/music-worker`. Compose services
`music-service` and `music-worker` are in profile `app`. The image is hadolint-clean but was not
built here (no Docker on the dev box).

## Environment

- DB/Redis: `DATABASE_URL`, `DB_REPLICA_URL` (optional), `REDIS_URL`, `MIGRATE_ON_START`.
- Navidrome: `NAVIDROME_URL` (`http://127.0.0.1:4533`), `NAVIDROME_USER` (`admin`),
  `NAVIDROME_PASSWORD` (required), `NAVIDROME_HEALTH_INTERVAL` (15s).
- Streaming: `STREAM_SIGNING_KEY` (≥ 32 bytes, required), `STREAM_URL_TTL` (1h max),
  `STREAM_PUBLIC_BASE` (optional absolute origin for media URLs; empty = relative).
- Worker: `CATALOG_SYNC_INTERVAL` (10m), `POPULARITY_REFRESH_INTERVAL` (5m).
- Recommender: `RECO_SERVICE_URL` (empty = heuristic only), `RECO_TIMEOUT` (400ms, 50ms–5s),
  `RECO_BREAKER_FAILURES` (3), `RECO_BREAKER_OPEN` (30s). `/readyz` reports `reco` as optional.
- Ports: `MUSIC_SERVICE_PORT` (8084), `MUSIC_WORKER_PORT` (8085).
- Auth: `INTERNAL_API_TOKEN` (required `X-Internal-Token`).

## Tests

- `make go-test` runs with `-race`. Postgres is replaced by an in-memory store
  (`internal/testutil`), Navidrome by a fake Subsonic server (Range via `http.ServeContent`),
  Redis by miniredis.
- Integration tests run when `TEST_DATABASE_URL` (repo, app) and `TEST_REDIS_URL` are set.
- CI checks `sqlc diff`, golangci-lint and the coverage gate (≥ 70%). Coverage is 75.5% without
  a DB and 92.7% with a DB.
- Tests cover the reco client (httptest: ok / 500 / timeout / breaker open → half-open → closed
  / junk / scrubbed transport errors), the wave with a fake recommender (reco, fallback, new round,
  feedback events), the internal exports and track-skipped.
