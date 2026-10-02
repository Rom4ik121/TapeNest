# ADR 0010: reco-service — a separate recommender for My Wave

- Status: Accepted
- Date: 2026-09-25
- Stage: "reco" (after stage 3; request: «улучшить по максимуму персональный подбор „Моя волна“ и вынести его в отдельный модуль»)
- Supersedes: nothing. ADR 0009 §6 (the wave heuristic) stays in place as the **fallback**.

## Context

In stage 3 My Wave was a heuristic inside music-service (ADR 0009 §6). It scored tracks by
popularity, "liked" and "liked artist", recency penalties and in-session artist
likes/skips. It had no long-term memory beyond likes and knew nothing about how tracks sound.
Nothing learned from other listeners. New tracks never surfaced unless they were popular.

The user asked for the best possible personal selection and for the feature to live in its
own module, so it can be worked on independently. Constraints:
- the dev box has no Docker and about 3 GB of free RAM;
- real interaction data is tiny (two dev users);
- the catalog is a legal CC0 / public-domain demo library.

## Decisions

### 1. A separate Go service with an API and a worker (`services/reco-service`)

- **reco-service** (API :8086) answers `POST /internal/v1/wave/next`. Debug endpoints:
  profile, similar tracks, model info (`docs/api/reco.openapi.yaml`). It holds an immutable
  in-memory **model snapshot** (catalog, embeddings, neighbours, factors). It polls
  `reco.state.model_version` every `RECO_MODEL_POLL` (20 s) and swaps the snapshot atomically.
  Per request it loads only the user's rows (taste, per-track state, Thompson arms, ALS
  factors), so a request costs a few indexed queries plus about 1 ms of ranking.
- **reco-worker** (health :8087) runs:
  - a one-off history backfill;
  - the event consumer;
  - catalog copy and audio analysis, every `RECO_CATALOG_INTERVAL`;
  - training (CF + ALS + content neighbours), every `RECO_TRAIN_INTERVAL` and whenever the
    catalog changed;
  - idempotency-key cleanup.
- Own schema `reco` (migrations embedded, sqlc):
  - `tracks` (copy), `track_features`;
  - `user_tracks`, `user_taste`, `source_stats`;
  - `item_neighbors` (`cf` | `content`), `item_factors`, `user_factors`;
  - `state`, `ingested`.
- reco never reads `music.*` tables directly. It talks to music-service only through
  internal HTTP exports and Redis streams. The two services can be deployed, scaled and
  migrated independently, and reco can move to its own database later.

**Go and no Python sidecar.** The rest of the backend is Go, and one image with ffmpeg
covers both binaries. The analyser does not need a Python ML stack (see §4).

### 2. Data flow

- **Catalog:** music-service `GET /internal/v1/catalog` (keyset pages; includes
  `genre`/`year`, now synced from Navidrome by migration `music/000002_genre`, plus the raw
  popularity score) → `reco.tracks`. Tracks missing from a full, non-empty sync are
  soft-deleted.
- **Live events:** music-service publishes to Redis streams, and reco-worker reads them with
  consumer group `reco`, starting at `$`:
  - `music:play_events`: existing listen events, ≥50 % or completed;
  - `music:user_events` (new): like / unlike / playlist add / remove / wave like / wave
    skip / skip.
  - `skip` comes from the new public `POST /events/track-skipped`. WavePlayer sends it when
    the user leaves a track before the 50 % listen event; it never goes to `play_events`.
  - Each entry is applied exactly once. `reco.ingested` stores its key in the same
    transaction as the profile update, and the entry is acked after commit. Pending entries
    are retried every minute, and unparseable ones are acked and dropped.
- **Backfill:** once (flag `backfill_done`), `GET /internal/v1/interactions?days=90`
  (NDJSON) replays likes, playlist adds and play events. Play events reuse their stream entry
  id as the key, so live and backfilled copies dedupe.
- **Audio:** `GET /internal/v1/tracks/{id}/audio` (the original file from Navidrome) →
  ffmpeg → PCM → analyser.
- All internal calls use `X-Internal-Token`. Errors are scrubbed of URLs and tokens
  (lesson of the two Navidrome incidents): transport errors are reduced to
  `op failed` / `timeout`.

### 3. Layer 1: long-term taste profile

Per (user, track) the service keeps a decayed affinity and counters:
- plays, completions, early skips, skips, playlist adds;
- liked flag, last played.

Signal weights (`internal/profile`):

| Signal | Weight |
|---|---|
| completed listen | +1.0 |
| each replay | +0.5 more |
| ≥ 50 % listen | +0.5 |
| early skip (< 30 s) | −1.0 |
| late skip | −0.3 |
| like / unlike | ±3 |
| playlist add / remove | +2 / −1 |
| wave like / wave skip | +1 / −0.5 |

- **Exponential time decay:** half-life 30 days, stored as `(value, at)` and decayed lazily
  on read. Out-of-order events are decayed to the stored reference time, so ordering does
  not matter.
- Every delta also propagates to **taste weights** per artist (×1), album (×0.6), genre
  (×0.5) and audio mood tag (×0.25), in `reco.user_taste`, also decayed.
- **Session feedback:** wave like/skip feeds both horizons. The short-term profile is the
  session's feedback list, which music-service sends with each request: artist, genre and
  embedding boosts with recency weighting. The long-term profile gets the `wave_like` /
  `wave_skip` events.
- **Cold start:** a user with no signals gets popularity + freshness + exploration. A user
  with only likes gets profile + content neighbours of the likes immediately.

### 4. Layer 3: audio content features in pure Go (instead of essentia / librosa)

`internal/audio`:
- ffmpeg decodes to mono 22 050 Hz float32 (first 120 s). A gonum FFT runs with frame 2048 /
  hop 512.
- **31-dim descriptor:**
  - tempo: onset-strength autocorrelation, harmonic enhancement and a log-Gaussian prior
    at 110 BPM;
  - RMS energy, loudness dB, dynamic range;
  - zero-crossing rate, spectral centroid, roll-off, flatness, flux;
  - 20 log-mel bands;
  - chroma → Krumhansl key profile: mode (valence proxy) and key clarity.
- The model z-scores descriptors across the catalog into unit **embeddings** (mel group
  weighted 0.55, tempo 0.7). Content neighbours are the top-20 cosine matches.
- Catalog-relative percentiles give **mood tags**: energetic/calm, fast/slow, bright/warm,
  major/minor. They drive the modes and the "mood you like" explanation.
- Speed: about 0.35 s per track on the dev box (132 tracks in about 45 s with 2 workers).
  Failures are recorded, scrubbed and retried up to 3 times.

Why not essentia or librosa:
- **essentia** is AGPL-3.0: a licensing problem for a closed service.
- **librosa** would need a Python sidecar: numpy/scipy/numba, roughly 500 MB more image, a
  second runtime, IPC and its own health checks.
- The features My Wave needs are classic DSP descriptors, and they compute cheaply and
  deterministically in Go.

A learned audio embedding (e.g. a CLAP/musicnn model through ONNX) is the natural upgrade
if the catalog grows. `features.version` makes re-analysis automatic.

### 5. Layer 2: collaborative filtering (worker job)

The training input is positive decayed affinities per (user, track).

- **Item-item co-occurrence:** cosine with shrinkage 2, top-20 neighbours → "because you
  liked X".
- **Implicit ALS (Hu–Koren–Volinsky):** k = 16, 12 iterations, λ = 0.1, confidence 1 + 8·r.
  Dense normal equations solved with gonum Cholesky. It gives "similar listeners" candidates
  the user has not heard. User factors are stored per user; item factors are part of the
  snapshot.
- Training is full-batch in about 15 ms at the current scale. Rough cost: O(iterations ·
  (nnz · k² + (users + items) · k³)), which is fine up to about 10⁵ users/items on one core.
  Beyond that, switch to conjugate-gradient ALS or an external trainer.

### 6. Ranking

`internal/rank`. Candidates from each source are scored and blended with **tunable weights**
(`RECO_WEIGHTS` JSON overrides the defaults).

| Weight | Default |
|---|---|
| profile | 1.0 |
| cf | 1.0 |
| als | 0.8 |
| content | 0.6 (centroid of positive seeds) |
| popular | 0.4 |
| fresh | 0.3 (tracks < 14 days) |
| session | 1.3 |
| novelty | 0.15 |

Then:
- **Cold items:** tracks without CF/ALS data get a content stand-in (×0.5), so new tracks
  surface from day one.
- **Penalties** on the normalized blend score:
  - played < 2 h (−1.0) or < 24 h (−0.35);
  - −0.35 per earlier early skip (up to 3);
  - strongly negative affinity (−0.5).

  Tracks already served in the session are excluded (no repeats).
- **Exploration:** ε = 0.07 random picks from the top of the unheard pool, and **Thompson
  sampling** over sources. Each user has Beta(5 + likes, 5 + skips) arms per source, updated
  from wave feedback with the `src` the track was served from.
- **Diversity:** from a pool of 80, MMR (λ = 0.7, audio-embedding similarity plus a
  same-artist term), ≤ 2 tracks per artist per batch, and **never the same artist back to
  back** (the constraints relax only when the catalog cannot satisfy them).
- **Modes** (cheap, on the audio features):
  - `calm` / `energetic`: a bonus linear in the energy percentile;
  - `discover`: unheard tracks are boosted and heard ones penalized, popularity is off,
    ε ≥ 0.3;
  - `favorites`: liked and high-affinity tracks, no exploration.
- **Explainability:** every pick carries a reason:
  - `because_you_liked` with the reference track (needs similarity ≥ 0.2; one reference
    at most twice per batch);
  - `artist_you_like`, `genre_you_like`, `mood_you_like`;
  - `similar_listeners`, `popular`, `new_in_catalog`, `discovery`, `favorite`,
    `session_artist`.

  music-service passes reasons through, and WavePlayer shows them as captions
  («Потому что вам нравится …»).

### 7. music-service integration, timeout, breaker, fallback

- `internal/reco.Client`: one POST per batch.
  - Timeout `RECO_TIMEOUT`, default 400 ms.
  - gobreaker opens after `RECO_BREAKER_FAILURES` (3) consecutive failures, stays open for
    `RECO_BREAKER_OPEN` (30 s), then sends one half-open probe.
  - An empty `RECO_SERVICE_URL` disables reco.
- `service.Wave.next`: reco first. An error, open breaker or empty answer for a fresh
  session falls back to the **ADR 0009 §6 heuristic**, now with simple reasons.
  - The response carries `strategy: reco|fallback` (and the `X-Wave-Strategy` header).
  - When reco returns nothing because everything was served, a new round starts: the served
    set is reset, and only the last batch-size tracks stay excluded.
- Session state stays in music-service Redis (`served`, `order`, `artists`, `src`, `fb`).
  reco-service stays stateless per request and can be restarted at any time.
- `/readyz` of music-service reports reco as an optional dependency.

### 8. Offline evaluation

- `cmd/reco-eval` / `make reco-eval` generates `docs/reco/evaluation.md`. The narrative is
  in `docs/reco/README.md`.
- Real data is tiny, so a **synthetic world** simulates the users:
  - 300 users with latent genre / audio / artist taste, 720 tracks, 90 artists, 8 genres,
    8 % brand-new tracks;
  - a listen/skip/like process;
  - a time-based 25 % holdout.
- **Offline metrics:** P/R/NDCG/HitRate@10, coverage, ILD, artist diversity, novelty,
  cold-start recall.
- **Closed-loop sessions:** 3 × 10 tracks with simulated like/skip reactions, fed back into
  the recommender.
- Mean over 5 seeds; ablations for every layer.

Reco vs heuristic:

| Metric | Heuristic → reco |
|---|---|
| NDCG@10 | 0.083 → 0.098 (+18 %) |
| P@10 | 0.056 → 0.075 (+33 %) |
| R@10 | 0.084 → 0.112 (+34 %) |
| Catalog coverage | 0.40 → 0.78 |
| Cold-start recall (new tracks) | 0 → 0.19 |
| Session skip rate | 0.130 → 0.073 (−44 %) |
| Session like rate | 0.623 → 0.660 |
| Same artist back-to-back | 1.5 % → 0 |

The simulator models a per-track recording-level offset (mastering, 78 rpm transfers). That
is why the embedding uses loudness-invariant spectral shape (audio v2).

Weights were tuned by random search on a separate seed (`-tune`).

## Consequences

- My Wave now learns from listening behaviour, other listeners and the sound of the tracks.
  It explains its picks and degrades to the old behaviour when reco is unavailable.
- There is one more service to run (two processes). Dev: `tools/dev/svc.sh restart reco`,
  `make miniapp-up`. Compose: `reco-service`, `reco-worker`.
- The profile is eventually consistent: event → stream → worker, typically < 1 s. Training
  is periodic. Session feedback affects the very next batch through the request itself.
- The metrics are **synthetic**. With real traffic, add online metrics (like/skip rate per
  strategy, via `src` / `strategy`) and A/B the weights.
- Known edge: events published between consumer-group creation and the end of the one-off
  backfill can be counted twice (backfill keys vs stream keys differ for likes). Negligible,
  and it happens only on the first start.
