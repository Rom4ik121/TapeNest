# ADR 0009: music-service — signed stream proxy, Navidrome catalog sync, wave MVP, play_events batching

- Status: Accepted. The Lidarr/torrent fill described below is withdrawn by [0014](0014-drop-cinema-and-torrent-stack.md). The local Navidrome catalog and signed stream proxy remain.
- Date: 2026-09-25
- Stage: 3 (spec §5.4, §3.1 #8/#11/#12, §8, §9)

## Context

Stage 3 replaces the WavePlayer music mocks with a real `services/music-service` (Go). The
contract is fixed by the frontend (`docs/api/waveplayer-contract.md`, `waveplayer.openapi.yaml`).
The spec (§5.4) requires:

- a UUID catalog filled by the Lidarr stack;
- streaming from Navidrome (Subsonic) with HTTP Range, `io.Copy` and a circuit breaker, and a
  catalog that stays alive when Navidrome is down;
- user data on the primary pool;
- `play_events` with monthly RANGE partitions and BRIN, accepted with 204 → Redis Streams →
  batch writer;
- a heuristic "wave";
- `pg_trgm` search;
- an optional read replica (`DB_REPLICA_URL`).

Several points are open. This ADR records the decisions.

## Decisions

1. **Audio and covers go through a signed proxy on the gateway, not a bearer-authenticated API.**
   An `<audio src>` or `<img src>` request cannot carry `Authorization`.
   - `GET /tracks/:id/stream-url` (JWT) returns
     `/api/v1/stream/tracks/:id?exp&u&sig`:
     - HMAC-SHA256 with `STREAM_SIGNING_KEY` over track + user + expiry;
     - TTL is `STREAM_URL_TTL` (≤ 1 h, spec §9).
   - Covers are `/api/v1/stream/covers/:coverId?size&sig`, signed without expiry. They are
     immutable, so `Cache-Control: public, max-age=31536000, immutable` and CDN-friendly.
   - api-gateway routes `/api/v1/stream/*` (GET/HEAD only) **without JWT and without the
     per-user limiter**. It strips client `X-User-Id`/`X-Telegram-Id` and injects the internal
     token. music-service verifies the signature and returns 403 on a bad or expired one.
   - music-service opens the original file from Navidrome (`/rest/stream?format=raw`, no
     transcoding), forwards `Range`/`If-Range`/conditional headers and `io.Copy`s the body.
     No server-side WriteTimeout, because a track can stream for minutes.
   - URLs are relative to the API origin. The client resolves them against `VITE_API_URL`
     when set (`STREAM_PUBLIC_BASE` can make them absolute server-side).
   - *Rejected: presigned MinIO links.* A comment in spec §5.5 mentions them for "music". §5.4
     explicitly says Navidrome + Range + `io.Copy`, and the library lives on Navidrome's
     `/music` volume, not in MinIO.
   - *Rejected: exposing Navidrome.* It must stay internal (§3.2), and its credentials would
     leak to the client.

2. **Navidrome is the catalog source; a worker mirrors it into `music.*`.**
   - `music-worker` pages Subsonic `search3` (empty query = all songs, 500 per page) every
     `CATALOG_SYNC_INTERVAL` (10 min, after `startScan`).
   - It upserts `artists/albums/tracks`. UUIDs are stable and keyed by `navidrome_id`.
   - Tracks missing from a complete listing are soft-deleted (`deleted_at`). An empty listing
     while tracks exist never mass-deletes: it is more likely a scan in progress or a wrong mount.
   - In production the `/music` volume is filled by the Lidarr stack (§5.4). It is not part of
     the dev stack (no Docker here; torrent clients are out of scope for a dev box).
   - In dev, `make music-seed` (`tools/dev/seed-music.py`) downloads **16 CC0 recordings from
     Musopen via Wikimedia Commons**. It re-checks each file's license through the Commons API
     (only CC0 / public domain is accepted), converts them to MP3 with ID3 tags, generates album
     covers and writes `CREDITS.md`.

3. **The Navidrome admin is created by music-service**, via `POST /auth/createAdmin`, which is
   accepted only while no user exists (idempotent), from `NAVIDROME_USER/NAVIDROME_PASSWORD`.
   - *Rejected: `ND_DEVAUTOCREATEADMINPASSWORD`.* It writes the password to Navidrome's log in
     clear text.
   - This happened once during stage 3 development. The dev password was rotated, and the log
     and data dir were wiped.

4. **Degradation (§8).**
   - The Navidrome client has its own breaker (3 consecutive failures, 15 s) and a health ping
     loop (`NAVIDROME_HEALTH_INTERVAL`). Aborted range requests (seek, track switch) are not
     failures.
   - When Navidrome is unhealthy, `stream-url` and the media routes return
     `503 {code: SERVICE_UNAVAILABLE, service: "streaming"}` with `X-Degraded-Dependency:
     navidrome` and `Retry-After: 15`.
   - api-gateway passes such 503s through **without counting them against the music-service
     breaker**. Otherwise a Navidrome outage would open the breaker and take down the catalog,
     likes, playlists and wave, which must stay alive.
   - `/readyz` reports `navidrome: degraded` but stays 200.

5. **Listen events and history.**
   - `POST /events/track-listened` validates the body, `XADD`s to the `music:play_events`
     stream (MAXLEN ~200k) and answers 204.
   - The worker's batcher (consumer group `music-batcher`, XREADGROUP up to 500 events / 1 s)
     inserts one batch per round trip and **acks only after commit**. It re-reads its own
     pending entries on restart and `XAUTOCLAIM`s entries idle for over 1 min from dead
     consumers.
   - `event_id` = stream entry id and `played_at` = the id's timestamp. With
     `UNIQUE(event_id, played_at)` + `ON CONFLICT DO NOTHING`, redelivery is idempotent.
     Malformed entries are acked and dropped.
   - `play_events` is `PARTITION BY RANGE (played_at)` with a default partition and BRIN on
     `played_at`. The worker keeps the current month + 2 months ahead
     (`music.ensure_play_events_partition`).
   - "Recent" (`/tracks/recent`) is a small per-user table `recent_plays(user_id, track_id,
     played_at)`, touched by listen events **and** saved playback positions. The home screen
     can then show a track right after it started, not only after the ≥ 50 % event.
   - "Popular" is a materialized view over 30 days (plays + 2·completed + 3·likes), refreshed
     `CONCURRENTLY` every `POPULARITY_REFRESH_INTERVAL` (5 min) and after each catalog sync.

6. **Wave MVP (heuristic, no ML).**
   - Candidates: up to 500 active tracks with popularity, the user's likes, liked artists and
     last play time.
   - Score = `ln(1+popularity) + 1.5·liked + 0.7·likedArtist + likes(artist in session)
     − 1.5·skips(artist in session) − 3 if played < 2 h ago (−1 if < 24 h) + jitter`.
   - Batches of 10 never repeat within a session. The same artist does not play twice in a row
     when avoidable.
   - Sessions live in Redis (`music:wave:{id}*`, TTL 6 h) and are owned by the user (foreign or
     expired → 404). A catalog smaller than the served set starts a new round, excluding the
     last batch, so a tiny library keeps playing.
   - Feedback `like/skip` adjusts the session's per-artist weights. A like also goes to
     `/tracks/:id/like` from the client, as before.

7. **Search:** `pg_trgm` `word_similarity` over a generated lowercase
   `search_text(title, artist, album)` with a GIN index, plus a `LIKE` substring for short
   queries. Keyset pagination by (score, id).

8. **Read-write splitting:** two pgx pools. Catalog reads (popular/search/wave candidates) use
   the replica when `DB_REPLICA_URL` is set; everything user-specific uses the primary.

9. **Stage 2 integration: none in this stage.**
   - The spec's stage 3 roadmap (§16) and §5.4 do not describe extracting audio from downloaded videos
     or a bot "add to music" option. The catalog is Navidrome/Lidarr-owned.
   - Writing user-downloaded audio into the shared `/music` library would also raise
     licensing/moderation questions the spec does not address.
   - Deferred. A future ADR can add a per-user "uploads" library (MinIO + ffmpeg in
     download-worker) if the product wants it.

10. **Frontend:**
    - Dev default `VITE_USE_MOCKS` is now *no mocks*. `music` / `all` stay available, and unit
      tests use the MSW handlers directly.
    - The player already maps a failed `stream-url` to the "stream unavailable" state and skips
      ahead.

## Consequences

- The gateway has one unauthenticated route family. It is safe because every URL is an
  unguessable HMAC, and stream URLs expire and are bound to a user. The cost: a leaked stream
  URL works for up to 1 h, and cover URLs never expire (covers are not sensitive).
- ngrok-free: `<audio>`/`<img>` requests cannot send `ngrok-skip-browser-warning`. They work
  after the one-time "Visit site" click, which sets the `abuse_interstitial` cookie. A static
  domain or production is unaffected.
- No gapless playback or transcoding (original files are served; the seed is MP3 192k).
- Lidarr/Prowlarr/qBittorrent are not deployed in dev. Production must mount a real library
  into Navidrome's `/music`.
