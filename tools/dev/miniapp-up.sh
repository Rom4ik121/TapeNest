#!/usr/bin/env bash
# DEV ONLY: bring up the whole local stack and expose it to Telegram:
#   PostgreSQL 16 + Redis 7.4 + MinIO + Navidrome (native, tools/dev/infra-native.sh)
#   api-gateway :8080, bot-service :8081, download-service :8082 + worker,
#   music-service :8084 + worker :8085 (built from source)
#   reco-service :8086 + worker :8087 (My Wave recommender, ADR 0010)
#   Lidarr :8686 + Prowlarr :9696 + qBittorrent-nox :8092 (native, tools/dev/arr-native.sh)
#   acquisition-service :8088 + worker :8089 (invisible acquisition, ADR 0011)
#   streaming-service :8094 + worker :8095 (CineNest, ADR 0013)
#   (the CC0 test indexer is NOT started here: `tools/dev/svc.sh restart legal-indexer`)
#   Vite dev server :5173 — proxies /api → gateway, /tg → bot-service, /media → MinIO
#   Video library is WavePlayer's /#/videos (no separate cinema mini app)
#   ngrok HTTPS tunnel → :5173 (one public URL for the mini app, API and webhook)
#   url-watch — re-registers webhook/menu button if the ngrok URL changes
# bot-service calls setWebhook(secret_token) + setChatMenuButton + setMyCommands on start.
# ngrok is kept running between invocations so the URL stays stable
# (NGROK_RESTART=1 forces a new tunnel; NGROK_DOMAIN pins a static domain).
# shellcheck source=lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
load_env
# the old long-polling responder conflicts with the webhook
stop_proc bot-dev
start_infra
start_music
start_reco
start_arr
start_acquisition
start_streaming
start_gateway
start_ngrok
URL="$(ngrok_url)"
start_vite "${URL#https://}"
start_bot "$URL"
start_download "$URL"
start_watch
echo
echo "Mini app:  $URL   videos: $URL/#/videos"
echo "Webhook:   $URL/tg/webhook"
for n in api-gateway bot-service download-service download-worker music-service music-worker reco-service reco-worker acquisition-service acquisition-worker streaming-service streaming-worker vite ngrok url-watch; do printf '  %-16s pid %-8s log %s\n' "$n" "$(pid_of "$n")" "$LOG_DIR/$n.log"; done
