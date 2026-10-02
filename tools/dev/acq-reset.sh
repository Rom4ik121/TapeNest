#!/usr/bin/env bash
# Dev/e2e only: forget everything acquisition-service fetched so the next play
# of those tracks goes through the full invisible acquisition again.
#   - deletes TapeNest torrents (+data) in qBittorrent, imported library files
#     and all acquisition requests (acqctl purge);
#   - drops music.acquired_files and asks music-service to rescan: vanished
#     tracks become placeholders again (same ids: likes/playlists survive).
# Prints counts only.
set -euo pipefail
cd "$(dirname "$0")/../.."
# shellcheck source=tools/dev/lib.sh disable=SC1091
source tools/dev/lib.sh
load_env
(cd services/acquisition-service && GOTOOLCHAIN=local go build -o "$BIN_DIR/acqctl" ./cmd/acqctl)
"$BIN_DIR/acqctl" purge -yes
psql "$DATABASE_URL" -qtAc "DELETE FROM music.acquired_files" >/dev/null
code=$(curl -s -o /dev/null -w '%{http_code}' -X POST -H "X-Internal-Token: $INTERNAL_API_TOKEN" \
  -H 'Content-Type: application/json' "http://127.0.0.1:${MUSIC_SERVICE_PORT:-8084}/internal/v1/catalog/refresh" -d '{"files":[]}')
echo "music refresh requested ($code)"
for _ in $(seq 1 30); do
  n=$(psql "$DATABASE_URL" -qtAc "SELECT count(*) FROM music.tracks t WHERE t.navidrome_id IS NOT NULL AND t.mb_recording_id IS NOT NULL AND t.deleted_at IS NULL")
  [[ $n == 0 ]] && { echo "library back to placeholders"; exit 0; }
  sleep 1
done
echo "warning: $n acquired tracks still linked (Navidrome scan slow?)" >&2
