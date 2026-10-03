#!/usr/bin/env bash
# DEV ONLY: keeps Telegram pointed at the current ngrok URL. Every 30 s it reads
# the tunnel URL from the local ngrok API; when it changes (ngrok restarted
# without a static domain) it restarts bot-service, which re-registers the
# webhook and the web_app menu button on startup, and download-service
# (its presigned public links embed the URL). Started by miniapp-up.sh.
# shellcheck source=lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
load_env
while true; do
  sleep "${URL_WATCH_INTERVAL:-30}"
  cur="$(ngrok_url)"
  last="$(cat "$LOG_DIR/miniapp-url.txt" 2>/dev/null || true)"
  if [[ -n "$cur" && "$cur" != "$last" ]]; then
    echo "$(date -Is) ngrok URL changed → re-registering bot ($cur)"
    start_bot "$cur" || echo "$(date -Is) bot restart failed"
    start_download "$cur" || echo "$(date -Is) download-service restart failed"
    start_video_editor "$cur" || echo "$(date -Is) video-editor restart failed"
    start_photo_editor "$cur" || echo "$(date -Is) photo-editor restart failed"
  elif ! is_running bot-service && [[ -n "$cur" ]]; then
    echo "$(date -Is) bot-service not running → restarting"
    start_bot "$cur" || echo "$(date -Is) bot restart failed"
  fi
done
