# ADR 0011: invisible acquisition of missing tracks

- Status: Accepted
- Date: 2026-10-02
- Stage: acquisition (after reco). User request: search and listen to any track in the app;
  the user does not add anything from outside. A missing file is fetched by torrent in the
  background. Play and like look like Yandex Music: no "Add" button and no "My requests" screen.

## Context

WavePlayer's catalog was a fixed CC0 library in Navidrome. Search only saw those files.
The product ask is a unified catalog: library tracks and MusicBrainz results look the same,
and pressing play or like on a track that is not on disk yet starts a download without a
separate action.

The stack already sketched in the spec is Lidarr + Prowlarr + qBittorrent. This box has no
Docker, so dev runs those three natively (`make arr-up`).

Legal constraint: TapeNest must not ship piracy indexers and must not download copyrighted
commercial music in tests. Verification uses a local Torznab stub that serves only Internet
Archive items whose licence is CC0, Public Domain Mark or CC BY (`tools/legal-indexer`).

## Decisions

### 1. A separate Go service (`services/acquisition-service`)

- API `:8088` is internal (`X-Internal-Token`). music-service is the only caller.
- Worker `:8089` bootstraps qBittorrent, Lidarr and Prowlarr (idempotent) and drives
  `queued → searching → downloading → importing → available` (or `failed`).
- Own schema `acquisition` (sqlc, golang-migrate).
- `CONTENT_SOURCES=licensed` turns the feature off without a rewrite. Search of MusicBrainz
  still works; acquire returns 503 `DISABLED`, which music-service shows as "not available".

### 2. Unified catalog lives in music-service

External hits are upserted as placeholders (`navidrome_id` null, stable UUID, `remote: true`).
They render as normal tracks, albums and artists. Likes and playlist rows are written
immediately; acquisition of the file is best-effort and does not roll the like back.

`GET /tracks/{id}/stream-url` returns **202** `{state, progress, retryAfterMs}` until the
wanted file has `ACQ_STREAM_START_BYTES` buffered (default 384 KiB) or the library file
exists. The player only shows its usual loading state and retries (`waitForStream`).

### 3. Wanted file first

One request per MusicBrainz release group (shared by every user). A play marks that
recording `wanted`. The worker grabs one release (quality × seeders × title match, size
caps), skips unrelated files, sets the wanted file to maximal priority and sequential
download, and streams it out of qBittorrent's incomplete directory with HTTP Range while
pieces arrive. Import prefers Lidarr manual import (hardlink, no tag rewrite) and falls
back to a direct hardlink into `MUSIC_DIR/library/<Artist>/<Album>/`.

### 4. Indexers are the operator's

Prowlarr starts with **zero** indexers. `acqctl add-test-indexer` is the only registration
TapeNest performs, and it points at the CC0 stub. Adding a public tracker is a manual step
in the Prowlarr UI. Redistributing other people's recordings to users of a public bot is
copyright infringement; complaints, bot bans and host takedowns are the operator's risk.
Personal use of a library you have the right to store is a different situation. This
codebase does not decide that for you.

### 5. Limits

Per user: `ACQ_USER_DAILY` new albums (default 30) and `ACQ_USER_ACTIVE` in flight (5).
Global: `ACQ_MAX_ACTIVE` downloads (3). Disk: `ACQ_TORRENT_MAX_GB` and `ACQ_LIBRARY_MAX_GB`.
A stalled torrent is deleted. Seeding stops at `ACQ_SEED_RATIO` or `ACQ_SEED_MINUTES`, then
the worker deletes the torrent data; the library hardlink remains.

Secrets (API keys, qBittorrent password, Prowlarr download URLs that embed `apikey`) are
scrubbed before logs (`internal/redact`).

## Consequences

- A second listener of the same album waits on the same torrent.
- Without any indexer, play ends in `NO_INDEXERS` after the search step (the UI shows that
  the track could not be found), not a fake success.
- MusicBrainz is rate-limited (~1 req/s, small burst) and cached in Redis.
- Docker Compose runs the API and worker; Lidarr, Prowlarr and qBittorrent stay on the host
  in dev (`make arr-up`) because their images are large and the native pins are already
  checksum-verified.
