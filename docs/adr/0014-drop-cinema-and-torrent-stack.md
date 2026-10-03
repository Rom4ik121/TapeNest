# ADR 0014: Keep the bot, YouTube music, recommendations, and user downloads

- Status: Accepted
- Date: 2026-10-03
- Supersedes: [0007](0007-cinenest-path-prefix-and-bot-entry.md), [0011](0011-acquisition-service.md), [0013](0013-cinenest-streaming-service.md)
- Amends: [0009](0009-music-service-streaming-catalog-wave.md) (no Lidarr fill), [0012](0012-youtube-music-primary.md) (no torrent fallback)

## Context

Movies were dropped. CineNest, the cinema catalog, streaming-service, acquisition-service, TorrServer, the Lidarr/Prowlarr/qBittorrent stack, and the legal indexer are not part of the product. A movie catalog is not a replacement. A video editor for downloads is not in this repository, and this change does not add one.

## Decision

The system is:

- **bot-service** — `/start`, `/help`, and user-pasted video links forwarded to download-service.
- **download-service** — yt-dlp downloads of those links into MinIO, with bot progress and delivery.
- **music-service** — WavePlayer catalog. Local files still sync from Navidrome. Search and playback of anything else use YouTube Music (`MUSIC_SOURCES` default `youtube`). Playback proxies yt-dlp and does not save the file. There is no torrent fallback and no acquisition client.
- **reco-service** — My Wave batches, with the music-service heuristic when reco is down.
- **api-gateway** — auth plus `/api/v1` routes for music, signed `/api/v1/stream/*`, and downloads. `/api/v1/cinema/*` is not a route.

Removed with this decision: `apps/cinenest`, `services/streaming-service`, `services/acquisition-service`, `tools/legal-indexer`, compose services and env for TorrServer, acquisition, streaming, Lidarr, qBittorrent, and cinema, the `/cinema` bot command, and gateway admin acquisition routes.

Also dropped because nothing reads them: `MINIAPP_CINENEST_URL`, `MINIAPP_MEDIAHUB_URL`, `CSAM_HASH_*`, and `CREATE SCHEMA bot` (bot-service has no database).

Navidrome, MinIO, Postgres, Redis, nginx, and Prometheus/Grafana stay. They back the library, downloads, and the dev edge. Observability is not a cinema leftover.

## Consequences

- Operators do not run TorrServer, *arr, or a cinema app.
- `MUSIC_SOURCES=library` is the local Navidrome catalog only. `youtube` does not call MusicBrainz or a torrent client.
- Historical ADR text stays so the record of what was built is intact. Those records are not instructions to rebuild the stack.
- Download storage remains the place a future editor would read from. This ADR does not add that editor.
