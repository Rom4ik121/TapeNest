# WavePlayer ↔ backend API contract (v1)

> Saved from the original `apps/waveplayer/README.md` + `src/api/*` **before** the
> frontend rewrite (2026-09-25). Source of truth for stage 1 (api-gateway) and
> stage 3 (music-service). Paths MUST NOT change. Machine-readable version:
> [`waveplayer.openapi.yaml`](./waveplayer.openapi.yaml).

Base URL: `${VITE_API_URL}/api/v1`. JSON everywhere, camelCase fields.
Auth: `Authorization: Bearer <accessToken>` on every endpoint except `/auth/*`.
Errors: non-2xx with body `{ "message": string, "code"?: string }`;
unavailable upstream → `503 { code: "SERVICE_UNAVAILABLE", service: "<name>" }`.

## Endpoints (original contract, unchanged paths)

| Method | Path | Request | Response |
|---|---|---|---|
| POST | `/auth/telegram` | `{ initData: string }` (raw Telegram initData) | `AuthTokens` |
| POST | `/auth/refresh` | `{ refreshToken: string }` | `AuthTokens` |
| GET | `/tracks/recent` | `?cursor=&limit=` | `Page<Track>` |
| GET | `/tracks/popular` | `?cursor=&limit=` | `Page<Track>` |
| GET | `/tracks/liked` | `?cursor=&limit=` | `Page<Track>` |
| GET | `/tracks/search` | `?q=&cursor=&limit=` (client debounce 300 ms) | `Page<Track>` |
| GET | `/tracks/:id/stream-url` | — | `StreamUrl` (presigned, short TTL ≤ 1 h) |
| POST | `/tracks/:id/like` | — | 204 |
| DELETE | `/tracks/:id/like` | — | 204 |
| GET | `/playlists` | — | `Playlist[]` |
| POST | `/playlists` | `{ title }` | `Playlist` (400 `INVALID` on empty title) |
| GET | `/playlists/:id` | — | `PlaylistWithTracks` (404 if missing) |
| PATCH | `/playlists/:id` | `{ title }` | `Playlist` |
| DELETE | `/playlists/:id` | — | 204 |
| POST | `/playlists/:id/tracks` | `{ trackId }` | 204 (idempotent) |
| DELETE | `/playlists/:id/tracks/:trackId` | — | 204 |
| POST | `/wave/sessions` | optional `{ mode }` (`default`, `calm`, `energetic`, `discover`, `favorites`) | `WaveSession` (first batch) |
| GET | `/wave/sessions/:id/tracks` | `?after=<lastTrackId>` | `WaveTrack[]` (404 unknown session) |
| POST | `/wave/sessions/:id/feedback` | `{ trackId, action: "like" \| "skip" }` | 204 |
| POST | `/events/track-listened` | `{ trackId, positionSec, completed }` | 204 (async, batched server-side) |
| POST | `/events/track-skipped` | `{ trackId, positionSec }` | 204 (stage "reco": left before 50 %, feeds the recommender) |

## Additions in the rewrite (backwards compatible)

| Method | Path | Request | Response | Why |
|---|---|---|---|---|
| GET | `/tracks/:id/position` | — | `{ trackId, positionSec, updatedAt }` or 404 | spec §5.4 `playback_positions`, §6 |
| PUT | `/tracks/:id/position` | `{ positionSec }` | 204 | forced save on pause / track switch / `visibilitychange` / `pagehide` + 5 s debounce |

* Pagination query params `cursor` (opaque string from `nextCursor`) and `limit`
  (default 20, max 100) are now **sent** by the client; the old client ignored `nextCursor`.
* `User.id` is a **UUID string** (spec §5.2); the Telegram numeric id is `telegramId`.

## Types

```ts
interface User { id: string /* UUID v4 */; telegramId: number; firstName: string;
  lastName: string | null; username: string | null; photoUrl: string | null;
  languageCode: string | null }
interface AuthTokens { accessToken: string; refreshToken: string; user: User }
interface Track { id: string; title: string; artist: string; album: string | null;
  coverUrl: string | null; durationSec: number; liked: boolean }
interface Page<T> { items: T[]; nextCursor: string | null }   // cursor, never offset
interface Playlist { id: string; title: string; trackCount: number; createdAt: string /* ISO */ }
interface PlaylistWithTracks { playlist: Playlist; tracks: Track[] }
interface WaveSession { sessionId: string; tracks: WaveTrack[]; strategy?: 'reco' | 'fallback'; mode?: WaveMode }
// stage "reco" (ADR 0010): additive, optional fields
type WaveMode = 'default' | 'calm' | 'energetic' | 'discover' | 'favorites';
interface WaveTrack extends Track { reason?: WaveReason | null }
interface WaveReason { kind: string; refTrackId?: string; refTitle?: string; refArtist?: string; artist?: string; genre?: string; tag?: string }
interface StreamUrl { url: string; expiresAt: string /* ISO */ }
interface PlaybackPosition { trackId: string; positionSec: number; updatedAt: string }
```

## Client behaviour the backend can rely on

* One in-flight refresh on 401; other requests wait and retry once. Refresh failure → re-login with initData.
* Stored JWT is discarded when its `user.telegramId` differs from the current initData user.
* Wave: prefetch next batch when ≤ 5 tracks remain; manual skip → `skip` feedback; like → `like` feedback;
  wave unavailable → fallback to `/tracks/popular`.
* Wave explanations (ADR 0010): `reason.kind` is shown as a caption ("Because you liked …"); unknown kinds
  are ignored. `strategy` tells whether reco-service or the local heuristic produced the batch.
* Manual skip before the 50 % listen event (any queue) → `POST /events/track-skipped { trackId, positionSec }`
  (204; negative taste signal for the recommender).
* `track-listened`: sent once at ≥ 50 % and once on completion, per play (dedupe resets on every new play).

## Stage 3: music-service implementation notes (ADR 0009)

* **Media URLs are signed, not bearer-authenticated.** `<audio>`/`<img>` cannot send an
  `Authorization` header, so:
  * `stream-url` returns `/api/v1/stream/tracks/:id?exp=&u=&sig=` (HMAC-SHA256, TTL ≤ 1 h,
    bound to track + user). It serves the original file with HTTP `Range` (206) and `HEAD`.
  * `Track.coverUrl` is `/api/v1/stream/covers/:coverId?size=300&sig=` (signed, no expiry,
    `Cache-Control: public, immutable`) or `null`.
  * Both are **relative to the API origin**; the client resolves them against `VITE_API_URL`
    when it is set (`src/shared/api/media.ts`), same-origin otherwise.
  * Bad/expired signature → 403. The gateway routes `/api/v1/stream/*` (GET/HEAD only) without
    a JWT and strips client identity headers.
* **Degraded streaming:** when Navidrome is down, `stream-url` (and the media routes) answer
  `503 { code: "SERVICE_UNAVAILABLE", service: "streaming" }` with `X-Degraded-Dependency:
  navidrome` and `Retry-After: 15`. The catalog, likes, playlists and wave keep working; the
  gateway does not count these responses against the music-service circuit breaker.
* Validation: `limit` 1..100 (default 20); `q` 1..100 chars; playlist title 1..100 chars
  (trimmed); `positionSec` 0..86400; ids are UUIDs (400 `INVALID` otherwise); ≤ 200 playlists
  per user. Search is fuzzy (pg_trgm `word_similarity` + substring), best match first.
* `recent` = tracks the user played (listen events) or saved a position for, newest first.
* `popular` = score over 30 days (plays + 2 × completed + 3 × likes), refreshed every
  `POPULARITY_REFRESH_INTERVAL` (5 min) and after each catalog sync.
* `POST /playlists` answers **200** with the playlist (original contract), not 201.
