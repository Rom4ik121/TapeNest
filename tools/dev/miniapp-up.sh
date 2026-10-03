#!/usr/bin/env bash
# DEV ONLY: bring up the whole local stack and expose it to Telegram:
#   PostgreSQL 16 + Redis 7.4 + MinIO + Navidrome (native, tools/dev/infra-native.sh)
#   api-gateway :8080, bot-service :8081, download-service :8082 + worker,
#   music-service :8084 + worker :8085 (built from source)
#   reco-service :8086 + worker :8087 (My Wave recommender, ADR 0010)
#   Vite dev server :5173 — proxies /api → gateway, /tg → bot-service, /media → MinIO
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
start_gateway
start_ngrok
URL="$(ngrok_url)"
start_vite "${URL#https://}"
start_bot "$URL"
start_download "$URL"
start_watch
echo
echo "Mini app:  $URL"
echo "Webhook:   $URL/tg/webhook"
for n in api-gateway bot-service download-service download-worker music-service music-worker reco-service reco-worker vite ngrok url-watch; do printf '  %-16s pid %-8s log %s\n' "$n" "$(pid_of "$n")" "$LOG_DIR/$n.log"; done
