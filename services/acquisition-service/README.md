# acquisition-service

Fills the music library in the background when someone plays or likes a track that is not
on the server yet ([ADR 0011](../../docs/adr/0011-acquisition-service.md)).
The app does not show an "Add" button. Search results from the library and from MusicBrainz
look the same.

Contract: [`docs/api/acquisition.openapi.yaml`](../../docs/api/acquisition.openapi.yaml).
Only music-service calls this API (`X-Internal-Token`).

## Binaries

| Binary | Role |
|---|---|
| `cmd/server` (`acquisition-service`, :8088) | Search, acquire, partial-file stream, admin reads. Applies migrations. |
| `cmd/worker` (`acquisition-worker`, health :8089) | qBittorrent / Lidarr / Prowlarr bootstrap, grab, wanted-file-first download, import, cleanup. |
| `cmd/acqctl` | Operator tool: `indexers`, `bootstrap`, `add-test-indexer` (CC0 stub only), `purge -yes`. |

`CONTENT_SOURCES=licensed` disables acquire (503 `DISABLED`) and leaves the worker idle.

## What you configure

TapeNest does **not** add torrent indexers. After `make arr-up`:

1. Open Prowlarr at `http://127.0.0.1:9696` (API key is `PROWLARR_API_KEY` in `.env`).
2. Add the indexers you choose. They must be reachable from this machine.
3. Leave them enabled and on the torrent protocol. The worker searches audio category 3000.

For a legal end-to-end check only:

```bash
tools/dev/svc.sh restart legal-indexer
```

That starts `tools/legal-indexer` (Internet Archive items whose licence was verified as
CC0, Public Domain Mark or CC BY) and registers it in Prowlarr as
"TapeNest Legal Test (CC0)". It refuses any other licence.

## Legal risk

A public bot that fetches commercial recordings from public trackers and plays them to
other people is copyright infringement. Rightsholders can complain to Telegram and to the
host; the bot and the server can be blocked. That risk is yours if you add those indexers.
Tests in this repo never do that: they use the CC0 stub or unit-test fakes.

## Limits

`ACQ_USER_DAILY`, `ACQ_USER_ACTIVE`, `ACQ_MAX_ACTIVE`, `ACQ_MAX_RELEASE_GB`,
`ACQ_TORRENT_MAX_GB`, `ACQ_LIBRARY_MAX_GB`, `ACQ_STALL_TIMEOUT`, `ACQ_SEED_RATIO`,
`ACQ_SEED_MINUTES`. See `.env.example`.

Logs never contain API keys or qBittorrent passwords. Errors pass through `internal/redact`.
