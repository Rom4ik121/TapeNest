# TapeNest

Telegram bot, WavePlayer (YouTube Music plus the local library), recommendations, and user-pasted video downloads.

- Spec: [`AI_DEVELOPMENT_INSTRUCTION.md`](AI_DEVELOPMENT_INSTRUCTION.md)
- Progress: [`STATUS.md`](STATUS.md)
- Decisions: [`docs/adr`](docs/adr) (current surface: [ADR 0014](docs/adr/0014-drop-cinema-and-torrent-stack.md))
- API: [`docs/api`](docs/api) (`gateway.openapi.yaml`, `download.openapi.yaml`, `waveplayer.openapi.yaml`, `reco.openapi.yaml`)

```bash
cp .env.example .env         # fill values (never commit .env); tools/dev/gen-dev-env.sh generates dev secrets
make help                    # all targets
make infra-up                # Docker: Postgres 16, Redis 7.4, MinIO, Navidrome
make infra-native            # …or without Docker: native PostgreSQL 16 + Redis 7.4 + MinIO + Navidrome
make music-seed              # demo library: CC0 recordings (Musopen via Wikimedia Commons) for Navidrome
make app-up                  # Docker: + api-gateway, bot-service, download-service/worker, music-service/worker, reco
make fe-install && make fe-dev   # WavePlayer on :5173 (real gateway + music-service via /api proxy; VITE_USE_MOCKS=music to mock)
make lint test build
make miniapp-up              # DEV: native infra + music + reco + gateway + bot + download + Vite + ngrok, one public URL
make miniapp-down
```

Layout:
- `services/*`: Go services
  - [`api-gateway`](services/api-gateway/README.md)
  - [`bot-service`](services/bot-service/README.md)
  - [`download-service`](services/download-service/README.md) (API + worker, yt-dlp → MinIO)
  - [`music-service`](services/music-service/README.md) (API + worker: Navidrome catalog, YouTube Music playback, likes, playlists, wave)
  - [`reco-service`](services/reco-service/README.md) (My Wave)
- `apps/*`: [`waveplayer`](apps/waveplayer/README.md)
- `deploy/`: compose, pg, nginx, prometheus, grafana
- `tools/`: initdata-mock, dev stack scripts, cookie-refresher
- `docs/`

Dev routing (a single ngrok URL, see ADR 0005):
- `/` → WavePlayer (Vite)
- `/api/*` → api-gateway :8080 (`/api/v1/stream/*`: signed audio/covers → music-service, ADR 0009 / 0012)
- `/tg/webhook` → bot-service :8081
- `/media/*` → MinIO :9000 (presigned download links, ADR 0008)

Downloads: send a YouTube / VK Video / RuTube link to the bot.
- The bot acknowledges the link, then edits that message with progress.
- A file up to 50 MB (≤720p, sized to fit when possible) is sent as a video.
- A larger file comes as a 1 h download link.
- Limits: 3 active and 30 downloads per day per user; no playlists, live streams or DRM.

`tools/dev/e2e-downloads.sh` checks the mini-app path end to end through the public URL.

Music: WavePlayer runs on music-service.
- Home, search, liked, playlists, "My wave" and positions are real.
- YouTube Music search plays through yt-dlp (`MUSIC_SOURCES=youtube`, ADR 0012 / 0014). The file is not saved.
- Library tracks stream from Navidrome through signed links with HTTP Range.
- The dev library is public-domain/CC0 recordings (`make music-seed`; credits in `$MUSIC_DIR/CREDITS.md`).

`make e2e-music` checks the music API through the public URL.
