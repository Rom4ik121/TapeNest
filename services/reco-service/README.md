# reco-service

The personal recommender behind **My Wave** («Моя волна»). It is a separate module, so the
feature can evolve independently of the WavePlayer backend. Decisions:
[ADR 0010](../../docs/adr/0010-reco-service-my-wave.md). Internal API:
[`docs/api/reco.openapi.yaml`](../../docs/api/reco.openapi.yaml). Evaluation:
[`docs/reco/`](../../docs/reco/README.md).

Stack: Go 1.22, chi, pgx + sqlc (schema `reco`), golang-migrate, go-redis (Streams),
gonum (FFT, Cholesky), ffmpeg (decode only).

```
WavePlayer ──► gateway ──► music-service ──(POST /internal/v1/wave/next, 400 ms, breaker)──► reco-service (API :8086)
                               │   ▲                                   fallback: ADR 0009 heuristic │ snapshot poll
                               │   └── /internal/v1/{catalog,interactions,tracks/:id/audio} ◄───┐   ▼
                               └──► Redis streams music:play_events, music:user_events ──► reco-worker (:8087)
                                                                                         catalog · audio (ffmpeg → DSP)
                                                                                         events → taste profile
                                                                                         training: CF + ALS + content
```

## Layers

| Layer | Package | What it does |
|---|---|---|
| Taste profile | `internal/profile`, `repo.ApplySignal` | Per (user, track): decayed affinity and counters (completion, early skip < 30 s, likes, replays, playlist adds, wave feedback). Weights per artist / album / genre / mood tag. Half-life 30 d. Exactly-once via `reco.ingested`. |
| Collaborative filtering | `internal/algo` | Item-item co-occurrence (cosine with shrinkage, top-20) and implicit ALS (k = 16, Hu–Koren) for "similar listeners"; retrained by the worker. |
| Audio content | `internal/audio`, `internal/model` | Pure-Go DSP on ffmpeg-decoded PCM: tempo, loudness, dynamics, ZCR, centroid, roll-off, flatness, flux, 20 log-mel, chroma → key / mode. Z-scored unit embeddings, cosine neighbours, energy percentile, mood tags. Handles cold start for new tracks. |
| Ranking | `internal/rank` | Weighted blend of profile / CF / ALS / content / popular / fresh / session sources. Recency and skip penalties. Novelty, ε-exploration and Thompson sampling over sources. MMR diversity, ≤ 2 per artist, never the same artist back to back. Modes. A reason for every pick. |
| Evaluation | `internal/eval`, `cmd/reco-eval` | Synthetic users, time-split holdout, P/R/NDCG@10, coverage, ILD, novelty, cold-start recall, closed-loop sessions, ablations, weight tuning. |

## Binaries

- `cmd/server` → `reco-service`: the API. `-migrate` applies migrations and exits.
- `cmd/worker` → `reco-worker`. In order: one-off backfill, then the event consumer
  (group `reco`), the catalog + audio analysis loop (retrains when something changed),
  periodic training and idempotency-key cleanup. Health on `:8087` (`/readyz` reports
  counters).
- `cmd/reco-eval`: offline evaluation (`make reco-eval`).

## API (internal, `X-Internal-Token`)

- `POST /internal/v1/wave/next`:
  `{userId, sessionId, mode, limit, exclude[], recent[], feedback[]}` →
  `{modelVersion, tracks: [{trackId, score, source, reason}]}`.
- `GET /internal/v1/users/{id}/profile`: top artists / genres / tags, counters, Thompson arms.
- `GET /internal/v1/tracks/{id}/similar`: audio features plus content and CF neighbours.
- `GET /internal/v1/model`: snapshot summary and active weights.
- `/healthz`, `/readyz` (503 while the model is empty; music-service then falls back).

## Run (dev, no Docker)

```bash
make miniapp-up                       # starts reco-service + reco-worker after music-service
tools/dev/svc.sh restart reco         # rebuild and restart both
make e2e-reco                         # public-URL e2e: reco strategy + reasons, modes, ingest, breaker fallback
make reco-eval                        # offline evaluation → docs/reco/evaluation.md
```

On first start the worker:
1. backfills history from music-service;
2. copies the catalog;
3. analyses every track (about 0.35 s each with 2 workers; 132 tracks take about 45 s);
4. trains and publishes model version 1. The API picks it up within `RECO_MODEL_POLL`.

Docker: `docker build -t tapenest/reco-service services/reco-service`. It is Debian slim
because ffmpeg is needed for decoding. Entrypoints: `/reco-service` (default),
`/reco-worker`, `/reco-eval`. Compose services `reco-service` and `reco-worker` are in
profile `app`. The image is hadolint-clean but was not built here (no Docker on the dev box).

## Environment

- `DATABASE_URL`, `REDIS_URL`, `INTERNAL_API_TOKEN` (required), `MIGRATE_ON_START` (true; the
  worker runs with false).
- `MUSIC_SERVICE_URL` (`http://127.0.0.1:8084`), `FFMPEG_BIN` (`ffmpeg`).
- Ports: `RECO_SERVICE_PORT` (8086), `RECO_WORKER_PORT` (8087).
- Worker: `RECO_ANALYZE_WORKERS` (2), `RECO_CATALOG_INTERVAL` (10m), `RECO_TRAIN_INTERVAL`
  (15m), `RECO_BACKFILL_DAYS` (90), `RECO_INGESTED_RETENTION` (720h).
- API: `RECO_MODEL_POLL` (20s), `RECO_WEIGHTS`: JSON overrides of the ranking weights, e.g.
  `{"cf":1.2,"epsilon":0.1}`. Current values are shown by `GET /internal/v1/model`.

## Tuning notes

- The weights are in `rank.DefaultWeights()`. Tune them offline first:
  `go run ./cmd/reco-eval -tune 40` does a random search on a validation world. Then
  override in production with `RECO_WEIGHTS`.
- A change to the audio descriptor must bump `audio.Version`: stale rows are re-analysed
  automatically.
- Scale: the snapshot is in memory (≈ 1 KB per track), so ranking is O(pool). ALS is
  full-batch; beyond about 10⁵ users/items switch to CG-ALS or an external trainer and
  add an ANN index for neighbours.

## Tests

- `make go-test` runs with `-race`.
  - Postgres is replaced by in-memory fakes (`internal/testutil`), music-service by an
    httptest fake, Redis by miniredis.
  - The audio tests synthesise sines, click tracks and noise, and decode through ffmpeg when
    it is installed.
- `TEST_DATABASE_URL` enables the repo integration test (schema `reco`).
- Coverage is 85.0% without a DB and 93.4% with a DB (gate 70%). golangci-lint reports
  0 issues and `sqlc diff` is clean.
