# TapeNest

Telegram bot plus Mini Apps (WavePlayer for music, `#/videos` for downloaded clips).

- Spec: [`AI_DEVELOPMENT_INSTRUCTION.md`](AI_DEVELOPMENT_INSTRUCTION.md)
- Progress: [`STATUS.md`](STATUS.md)
- Decisions: [`docs/adr`](docs/adr)
- API: [`docs/api`](docs/api) (`gateway.openapi.yaml`, `download.openapi.yaml`, `waveplayer.openapi.yaml`, `cinema.openapi.yaml`)

```bash
cp .env.example .env         # fill values (never commit .env); tools/dev/gen-dev-env.sh generates dev secrets
make help                    # all targets
make infra-up                # Docker: Postgres 16, Redis 7.4, MinIO, Navidrome, TorrServer
make infra-native            # …or without Docker: native PostgreSQL 16 + Redis 7.4 + MinIO + Navidrome
make music-seed              # demo library: 16 CC0 recordings (Musopen via Wikimedia Commons) for Navidrome
make app-up                  # Docker: + api-gateway, bot-service, download-service/worker, music-service/worker
make fe-install && make fe-dev   # WavePlayer on :5173 (real gateway + music-service via /api proxy; VITE_USE_MOCKS=music to mock)
make cn-dev                  # leftover CineNest dev server (not linked from the bot)
make lint test build
make miniapp-up              # DEV: native infra + music + gateway + bot + download + Vite + ngrok, one public URL
make miniapp-down
```

Layout:
- `services/*`: Go services
  - [`api-gateway`](services/api-gateway/README.md)
  - [`bot-service`](services/bot-service/README.md)
  - [`download-service`](services/download-service/README.md) (API + worker, yt-dlp → MinIO)
  - [`music-service`](services/music-service/README.md) (API + worker: catalog from Navidrome, signed streaming, likes, playlists, wave, listens)
- `apps/*`: React mini apps — [`waveplayer`](apps/waveplayer/README.md), [`cinenest`](apps/cinenest/README.md)
- `deploy/`: compose, pg, nginx, prometheus, grafana, k8s
- `tools/`: initdata-mock, dev stack scripts, CI helpers, cookie-refresher
- `docs/`

Dev routing (a single ngrok URL, see ADR 0005):
- `/` → WavePlayer (Vite). Downloaded videos: `/#/videos`
- `/api/*` → api-gateway :8080 (`/api/v1/stream/*`: signed audio/covers → music-service → Navidrome, ADR 0009)
- `/tg/webhook` → bot-service :8081
- `/media/*` → MinIO :9000 (presigned download links, ADR 0008)

Downloads (stage 2): send a YouTube / VK Video / RuTube link to @tapenest_bot.
- The bot acknowledges the link, then edits that message with progress.
- A file up to 50 MB (≤720p, sized to fit when possible) is sent as a video.
- A larger file comes as a 1 h download link.
- Limits: 3 active and 30 downloads per day per user; no playlists, live streams or DRM.

`tools/dev/e2e-downloads.sh` checks the mini-app path end to end through the public URL.

Music (stage 3): WavePlayer runs on the real music-service.
- Home, search, liked, playlists, "My wave" and positions are all real.
- Audio streams from Navidrome through signed links with HTTP Range.
- The dev library is 16 public-domain/CC0 classical recordings (`make music-seed`; credits in
  `$MUSIC_DIR/CREDITS.md`). In production, mount your library (or the Lidarr stack) as
  Navidrome's `/music`.

`make e2e-music` checks the whole music API (30 checks) through the public URL.
# TapeNest
