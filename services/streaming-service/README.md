# streaming-service

CineNest backend (stage 4, spec §5.5). The gateway proxies `/api/v1/cinema/*` here.
HLS is served by this process at `/hls/{session}/…` (nginx and the Vite dev servers proxy that path).

## What it stores

The catalog is the project's fictional titles (the same cards the MSW mocks used), as real
Postgres rows in schema `streaming`. Posters are generated SVG data URLs on the TapeNest palette.
No film file is downloaded or stored.

Playback of those titles is a short **generated** HLS preview (ffmpeg `testsrc2` + a sine tone)
so the player, warm-up indicator and resume position work. `CONTENT_SOURCES` may include `p2p`:
TorrServer is called only when a `media_files.magnet` is already set. The seed sets none.
There is no TMDB key and the spec does not name one; the local catalog does not need it.

## Run

```bash
# from the repo root, with DATABASE_URL, INTERNAL_API_TOKEN, STREAM_SIGNING_KEY
make  # or:
(cd services/streaming-service && go run ./cmd/server)
```

Defaults: API `:8094`, worker `:8095`. `STREAMING_SERVICE_URL=http://127.0.0.1:8094` on the gateway.

Admin (metadata only):

```bash
go run ./cmd/admin -title "Локальная лента" -kind movie -year 2026
```

Or `POST /api/v1/cinema/admin/titles` with `X-User-Role: admin` (audit row in `streaming.audit_log`).
