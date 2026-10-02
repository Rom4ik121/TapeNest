#!/usr/bin/env bash
# DEV ONLY: stop everything started by miniapp-up.sh (KEEP_NGROK=1 keeps the tunnel/URL).
# PostgreSQL/Redis/MinIO keep running; stop them with tools/dev/infra-native.sh down.
# shellcheck source=lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
for n in url-watch legal-indexer acquisition-worker acquisition-service reco-worker reco-service music-worker music-service download-worker download-service bot-service vite cinenest api-gateway bot-dev; do stop_proc "$n"; done
[[ "${KEEP_NGROK:-0}" == 1 ]] || stop_proc ngrok
