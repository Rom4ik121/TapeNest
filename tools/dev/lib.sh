#!/usr/bin/env bash
# DEV ONLY: process helpers shared by miniapp-up.sh / miniapp-down.sh / svc.sh.
# Every component runs detached (setsid nohup) with a pid file + log in $LOG_DIR.
# Secrets are loaded into the environment only (never printed or written).
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
LOG_DIR="${LOG_DIR:-/workspace/logs}"
BIN_DIR="${DEV_BIN_DIR:-/workspace/.devbin}"
VITE_PORT="${VITE_PORT:-5173}"
export PATH="$HOME/.local/bin:/usr/local/go/bin:$PATH"
mkdir -p "$LOG_DIR" "$BIN_DIR"

load_env() {
  [[ -f "$ROOT/.env" ]] || "$ROOT/tools/dev/gen-dev-env.sh"
  set -a
  # shellcheck disable=SC1091
  source "$ROOT/.env"
  set +a
  # shellcheck source=/dev/null
  . "$ROOT/tools/dev/load-secrets.sh"
  : "${TELEGRAM_BOT_TOKEN:?TELEGRAM_BOT_TOKEN missing}"
}

pid_of() { if [[ -f "$LOG_DIR/$1.pid" ]]; then cat "$LOG_DIR/$1.pid"; fi; }
is_running() { local p; p="$(pid_of "$1")"; [[ -n "$p" ]] && kill -0 "$p" 2>/dev/null; }

stop_proc() {
  local name="$1" p
  p="$(pid_of "$name")"
  if [[ -n "$p" ]] && kill -0 "$p" 2>/dev/null; then
    kill -TERM -- "-$p" 2>/dev/null || kill -TERM "$p" 2>/dev/null || true
    for _ in $(seq 1 50); do kill -0 "$p" 2>/dev/null || break; sleep 0.1; done
    kill -KILL -- "-$p" 2>/dev/null || true
    echo "stopped $name ($p)"
  fi
  rm -f "$LOG_DIR/$name.pid"
}

# spawn <name> <cmd...>: detached, own process group, log appended with a marker.
spawn() {
  local name="$1"; shift
  echo "=== $(date -Is) start $name ===" >>"$LOG_DIR/$name.log"
  setsid nohup "$@" >>"$LOG_DIR/$name.log" 2>&1 </dev/null &
  echo $! >"$LOG_DIR/$name.pid"
}

wait_http() { # wait_http <url> <seconds>
  for _ in $(seq 1 $(( $2 * 5 ))); do curl -fsS -o /dev/null "$1" 2>/dev/null && return 0; sleep 0.2; done
  return 1
}

ngrok_url() {
  curl -fsS http://127.0.0.1:4040/api/tunnels 2>/dev/null | python3 -c '
import sys, json
try:
    t = [x["public_url"] for x in json.load(sys.stdin)["tunnels"] if x["public_url"].startswith("https://")]
    print(t[0] if t else "")
except Exception:
    print("")'
}

build_go() { # build_go <service> [cmd=server] [binary name]
  (cd "$ROOT/services/$1" && GOTOOLCHAIN=local go build -o "$BIN_DIR/${3:-$1}" "./cmd/${2:-server}")
}

start_infra() { "$ROOT/tools/dev/infra-native.sh" up; }

start_gateway() {
  stop_proc api-gateway
  build_go api-gateway
  spawn api-gateway "$BIN_DIR/api-gateway"
  wait_http "http://127.0.0.1:${API_GATEWAY_PORT:-8080}/readyz" 20 || { echo "api-gateway not ready, see $LOG_DIR/api-gateway.log" >&2; return 1; }
  echo "api-gateway up (pid $(pid_of api-gateway))"
}

start_vite() {
  stop_proc vite
  local host="${1:-}"
  (cd "$ROOT/apps/waveplayer" && { [[ -d node_modules ]] || npm ci; } &&
    VITE_DEV_PUBLIC_HOST="$host" GATEWAY_URL="http://127.0.0.1:${API_GATEWAY_PORT:-8080}" \
    BOT_SERVICE_URL="http://127.0.0.1:${BOT_SERVICE_PORT:-8081}" \
    spawn vite npx vite --port "$VITE_PORT" --strictPort)
  wait_http "http://127.0.0.1:$VITE_PORT/" 40 || { echo "vite not ready" >&2; return 1; }
  echo "vite up (pid $(pid_of vite))"
}

# CineNest (apps/cinenest, base /cinenest/) — reached via the WavePlayer Vite proxy.
start_cinenest() {
  stop_proc cinenest
  local host="${1:-}"
  (cd "$ROOT/apps/cinenest" && { [[ -d node_modules ]] || npm ci; } &&
    VITE_DEV_PUBLIC_HOST="$host" GATEWAY_URL="http://127.0.0.1:${API_GATEWAY_PORT:-8080}" \
    spawn cinenest npx vite --port "${CINENEST_PORT:-5174}" --strictPort)
  wait_http "http://127.0.0.1:${CINENEST_PORT:-5174}/cinenest/" 40 || { echo "cinenest not ready" >&2; return 1; }
  echo "cinenest up (pid $(pid_of cinenest))"
}

start_ngrok() {
  if is_running ngrok && [[ -n "$(ngrok_url)" && "${NGROK_RESTART:-0}" != 1 ]]; then
    echo "ngrok already running (pid $(pid_of ngrok)) — URL kept"; return 0
  fi
  stop_proc ngrok
  if [[ -n "${NGROK_AUTHTOKEN:-}" ]]; then ngrok config add-authtoken "$NGROK_AUTHTOKEN" >/dev/null; fi
  local args=(http "$VITE_PORT" --log=stdout --log-format=json)
  [[ -n "${NGROK_DOMAIN:-}" ]] && args+=(--url "https://${NGROK_DOMAIN}")
  spawn ngrok ngrok "${args[@]}"
  for _ in $(seq 1 30); do [[ -n "$(ngrok_url)" ]] && break; sleep 1; done
  [[ -n "$(ngrok_url)" ]] || { echo "ngrok did not come up; see $LOG_DIR/ngrok.log" >&2; return 1; }
  echo "ngrok up (pid $(pid_of ngrok))"
}

# bot-service registers webhook + menu button + commands itself on start.
start_bot() {
  local url="$1"
  stop_proc bot-service
  build_go bot-service
  TELEGRAM_WEBHOOK_URL="$url/tg/webhook" MINIAPP_WAVEPLAYER_URL="$url/" MINIAPP_VIDEOS_URL="$url/#/videos" \
    spawn bot-service "$BIN_DIR/bot-service"
  wait_http "http://127.0.0.1:${BOT_SERVICE_PORT:-8081}/readyz" 30 || { echo "bot-service not ready, see $LOG_DIR/bot-service.log" >&2; return 1; }
  echo "$url" >"$LOG_DIR/miniapp-url.txt"
  echo "bot-service up (pid $(pid_of bot-service)), webhook → $url/tg/webhook"
}

# download-service API :8082 + worker (health :8083). S3_PUBLIC_URL = tunnel URL:
# presigned links for files > 50 MB go through <url>/media/… → Vite proxy → MinIO.
start_download() {
  local url="$1"
  stop_proc download-worker
  stop_proc download-service
  build_go download-service server download-service
  build_go download-service worker download-worker
  S3_PUBLIC_URL="$url" spawn download-service "$BIN_DIR/download-service"
  wait_http "http://127.0.0.1:${DOWNLOAD_SERVICE_PORT:-8082}/readyz" 30 || { echo "download-service not ready, see $LOG_DIR/download-service.log" >&2; return 1; }
  S3_PUBLIC_URL="$url" MIGRATE_ON_START=false spawn download-worker "$BIN_DIR/download-worker"
  wait_http "http://127.0.0.1:${DOWNLOAD_WORKER_PORT:-8083}/healthz" 20 || { echo "download-worker not ready, see $LOG_DIR/download-worker.log" >&2; return 1; }
  echo "download-service up (pid $(pid_of download-service)), worker pid $(pid_of download-worker)"
}

# music-service API :8084 + worker (health :8085). Streams go through
# <url>/api/v1/stream/… (signed links, public gateway route) → Navidrome :4533.
start_music() {
  stop_proc music-worker
  stop_proc music-service
  build_go music-service server music-service
  build_go music-service worker music-worker
  spawn music-service "$BIN_DIR/music-service"
  wait_http "http://127.0.0.1:${MUSIC_SERVICE_PORT:-8084}/readyz" 30 || { echo "music-service not ready, see $LOG_DIR/music-service.log" >&2; return 1; }
  MIGRATE_ON_START=false spawn music-worker "$BIN_DIR/music-worker"
  wait_http "http://127.0.0.1:${MUSIC_WORKER_PORT:-8085}/healthz" 20 || { echo "music-worker not ready, see $LOG_DIR/music-worker.log" >&2; return 1; }
  echo "music-service up (pid $(pid_of music-service)), worker pid $(pid_of music-worker)"
}

start_reco() {
  stop_proc reco-worker
  stop_proc reco-service
  build_go reco-service server reco-service
  build_go reco-service worker reco-worker
  local music="http://127.0.0.1:${MUSIC_SERVICE_PORT:-8084}"
  MUSIC_SERVICE_URL="$music" spawn reco-service "$BIN_DIR/reco-service"
  wait_http "http://127.0.0.1:${RECO_SERVICE_PORT:-8086}/healthz" 30 || { echo "reco-service not ready, see $LOG_DIR/reco-service.log" >&2; return 1; }
  MIGRATE_ON_START=false MUSIC_SERVICE_URL="$music" spawn reco-worker "$BIN_DIR/reco-worker"
  wait_http "http://127.0.0.1:${RECO_WORKER_PORT:-8087}/healthz" 20 || { echo "reco-worker not ready, see $LOG_DIR/reco-worker.log" >&2; return 1; }
  echo "reco-service up (pid $(pid_of reco-service)), worker pid $(pid_of reco-worker)"
}

# Lidarr + Prowlarr + qBittorrent-nox (pinned, checksum-verified, 127.0.0.1 only).
start_arr() { "$ROOT/tools/dev/arr-native.sh" up; }

# DEV/E2E ONLY: Torznab stub serving licence-verified CC0 Internet Archive torrents.
start_legal_indexer() {
  stop_proc legal-indexer
  (cd "$ROOT/tools/legal-indexer" && GOTOOLCHAIN=local go build -o "$BIN_DIR/legal-indexer" .)
  spawn legal-indexer "$BIN_DIR/legal-indexer"
  wait_http "http://127.0.0.1:8093/healthz" 60 || { echo "legal-indexer not ready, see $LOG_DIR/legal-indexer.log" >&2; return 1; }
  build_go acquisition-service acqctl acqctl
  "$BIN_DIR/acqctl" add-test-indexer -url http://127.0.0.1:8093
  echo "legal-indexer up (pid $(pid_of legal-indexer))"
}

start_acquisition() {
  stop_proc acquisition-worker
  stop_proc acquisition-service
  build_go acquisition-service server acquisition-service
  build_go acquisition-service worker acquisition-worker
  local music="http://127.0.0.1:${MUSIC_SERVICE_PORT:-8084}"
  MUSIC_SERVICE_URL="$music" spawn acquisition-service "$BIN_DIR/acquisition-service"
  wait_http "http://127.0.0.1:${ACQUISITION_SERVICE_PORT:-8088}/healthz" 30 || { echo "acquisition-service not ready, see $LOG_DIR/acquisition-service.log" >&2; return 1; }
  MIGRATE_ON_START=false MUSIC_SERVICE_URL="$music" spawn acquisition-worker "$BIN_DIR/acquisition-worker"
  wait_http "http://127.0.0.1:${ACQUISITION_WORKER_PORT:-8089}/healthz" 20 || { echo "acquisition-worker not ready, see $LOG_DIR/acquisition-worker.log" >&2; return 1; }
  echo "acquisition-service up (pid $(pid_of acquisition-service)), worker pid $(pid_of acquisition-worker)"
}

start_watch() {
  stop_proc url-watch
  spawn url-watch "$ROOT/tools/dev/url-watch.sh"
  echo "url-watch up (pid $(pid_of url-watch))"
}

# streaming-service API :8094 + worker (health :8095). Cinema catalog is local;
# /hls is proxied by Vite/nginx straight here (signed playlists).
start_streaming() {
  stop_proc streaming-worker
  stop_proc streaming-service
  build_go streaming-service server streaming-service
  build_go streaming-service worker streaming-worker
  CINEMA_PREVIEW_DIR="${CINEMA_PREVIEW_DIR:-$ROOT/apps/cinenest/dev-assets/mock-hls}" \
    spawn streaming-service "$BIN_DIR/streaming-service"
  wait_http "http://127.0.0.1:${STREAMING_SERVICE_PORT:-8094}/readyz" 40 || { echo "streaming-service not ready, see $LOG_DIR/streaming-service.log" >&2; return 1; }
  MIGRATE_ON_START=false spawn streaming-worker "$BIN_DIR/streaming-worker"
  wait_http "http://127.0.0.1:${STREAMING_WORKER_PORT:-8095}/healthz" 20 || { echo "streaming-worker not ready, see $LOG_DIR/streaming-worker.log" >&2; return 1; }
  echo "streaming-service up (pid $(pid_of streaming-service)), worker pid $(pid_of streaming-worker)"
}
