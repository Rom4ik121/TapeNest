# Architecture Decision Records

Only decisions **not** fixed in `AI_DEVELOPMENT_INSTRUCTION.md` §3.1 (spec §13).

| # | Title | Status |
|---|---|---|
| [0001](0001-background-audio-telegram-webview.md) | Background audio in Telegram WebView | Accepted |
| [0002](0002-waveplayer-contract-additions.md) | WavePlayer contract additions (positions, cursor params, UUID user) | Accepted |
| [0003](0003-minio-image-source.md) | MinIO container image source | Accepted |
| [0004](0004-dev-mocks-and-tunnel.md) | Dev mocks (MSW + fetch fallback) and ngrok dev tunnel | Accepted, amended by 0005 (per-domain mocks, bot-dev removed) |
| [0005](0005-dev-single-tunnel-and-native-infra.md) | One dev tunnel (Vite proxy /api, /tg) + native PG/Redis without Docker | Accepted |
| [0006](0006-auth-sessions-errors-and-integration-tests.md) | Refresh sessions in Redis, 501 vs 503, integration tests via env DSNs | Accepted |
| [0007](0007-cinenest-path-prefix-and-bot-entry.md) | CineNest under `/cinenest/` on the single origin; bot entry (/start buttons, /cinema; menu stays WavePlayer); copied shared FE code | Accepted |
| [0008](0008-downloads-storage-delivery.md) | Downloads: MinIO + presigned `/media` links, ≤720p profile fitting 50 MB, bot upload vs link, `download:events` stream, quotas, retries/proxies, cookies stand-in | Accepted |
| [0009](0009-music-service-streaming-catalog-wave.md) | music-service: signed stream proxy via gateway `/api/v1/stream/*`, Navidrome catalog sync, degraded 503 vs breaker, play_events batcher + partitions, wave heuristic, no stage-2 audio import | Accepted |
| [0010](0010-reco-service-my-wave.md) | reco-service for My Wave: Go service + worker, taste profile with decay, co-occurrence + implicit ALS, pure-Go audio features (no essentia/librosa sidecar), MMR/Thompson ranking with reasons, timeout + breaker + heuristic fallback, synthetic offline evaluation | Accepted |
