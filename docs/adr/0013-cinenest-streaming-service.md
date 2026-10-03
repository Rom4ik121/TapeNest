# ADR 0013: CineNest streaming-service uses the fictional catalog and a generated preview

- Status: Superseded by [0014](0014-drop-cinema-and-torrent-stack.md). streaming-service, CineNest, and TorrServer are removed. Do not rebuild them from this record.
- Date: 2026-10-02

## Context

Stage 4 (spec §5.5) is TorrServer, an HLS proxy, catalog search, watch positions, a `SourceProvider`,
and an admin CLI. The CineNest UI was already built against `docs/api/cinema.openapi.yaml` and showed
a "demo data" badge while `/cinema/*` was mocked.

The mock titles are fictional. The spec's p2p path can point TorrServer at a magnet, which would
download that torrent onto disk. This repository must not fetch or store copyrighted films.

## Decision

1. `services/streaming-service` implements the cinema OpenAPI on schema `streaming` (API :8094,
   worker :8095). The gateway already proxies `/api/v1/cinema/*` when `STREAMING_SERVICE_URL` is set.
2. The catalog is the fictional CineNest titles, seeded as real rows (pg_trgm search, watch later,
   positions, continue). Posters stay generated SVG on the TapeNest palette. No metadata provider
   that needs a key is used; the spec does not name one.
3. Playback of a file **without** a magnet returns the existing warm-up session and then an HLS URL
   on `/hls/{id}/index.m3u8`. The playlist is a locally generated test pattern (color bars and a
   tone), rewritten to absolute paths on our domain with an HMAC query token (native HLS cannot send
   `Authorization`). Segment bytes are proxied from that local file with `http.ServeContent`.
4. `CONTENT_SOURCES` still accepts `p2p` and `licensed` (shared with acquisition-service). A magnet
   is sent to TorrServer only when the column is non-empty **and** p2p is enabled. TorrServer down
   answers `503` with `X-Degraded-Dependency: torrserver` on `POST /cinema/streams`; the catalog
   stays up. The seed has no magnets, and this service does not search public trackers.
5. Admin: `cmd/admin` and `POST /api/v1/cinema/admin/titles` (role `admin`) insert metadata and an
   audit row. They do not accept a magnet.
6. CineNest's dev default is no MSW cinema mock. `VITE_USE_MOCKS=cinema` keeps the old mock.

## Consequences

- The player can open, warm, and resume, but it does not play a movie.
- Turning on a real magnet is an operator action outside this seed, and it is the operator's
  responsibility (spec §14). The code path exists so the switch does not require a rewrite.
