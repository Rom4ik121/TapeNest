#!/usr/bin/env bash
# DEV ONLY: restart/stop/status one component of the dev stack.
#   tools/dev/svc.sh restart api-gateway|bot-service|download|music|reco|acquisition|streaming|arr|legal-indexer|vite|cinenest|ngrok|url-watch
#   tools/dev/svc.sh stop <name> | status
# shellcheck source=lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
load_env
cmd="${1:-status}"; name="${2:-}"
case "$cmd" in
  status)
    for n in api-gateway bot-service download-service download-worker music-service music-worker reco-service reco-worker acquisition-service acquisition-worker streaming-service streaming-worker legal-indexer vite cinenest ngrok url-watch; do
      if is_running "$n"; then s="running"; else s="stopped"; fi
      printf '  %-16s %-8s pid %-8s %s\n' "$n" "$s" "$(pid_of "$n")" "$LOG_DIR/$n.log"
    done
    echo "  url: $(ngrok_url)"
    "$ROOT/tools/dev/infra-native.sh" status
    "$ROOT/tools/dev/arr-native.sh" status ;;
  stop) stop_proc "$name" ;;
  restart)
    url="$(ngrok_url)"
    case "$name" in
      api-gateway) start_gateway ;;
      bot-service) start_bot "$url" ;;
      download|download-service|download-worker) start_download "$url" ;;
      music|music-service|music-worker) start_music ;;
      reco|reco-service|reco-worker) start_reco ;;
      acquisition|acquisition-service|acquisition-worker) start_acquisition ;;
      streaming|streaming-service|streaming-worker) start_streaming ;;
      arr) "$ROOT/tools/dev/arr-native.sh" down; start_arr ;;
      legal-indexer) start_legal_indexer ;;
      vite) start_vite "${url#https://}" ;;
      cinenest) start_cinenest "${url#https://}" ;;
      ngrok) NGROK_RESTART=1 start_ngrok; url="$(ngrok_url)"; start_vite "${url#https://}"; start_bot "$url"; start_download "$url" ;;
      url-watch) start_watch ;;
      *) echo "unknown component: $name" >&2; exit 2 ;;
    esac ;;
  *) echo "usage: $0 status|stop <name>|restart <name>" >&2; exit 2 ;;
esac
