#!/usr/bin/env bash
# E2E (stage 3): synthetic Telegram login via the public tunnel, then the whole
# WavePlayer music API through ngrok → Vite → api-gateway → music-service → Navidrome:
# catalog, search, signed stream with Range, cover, like, playlist CRUD, wave,
# position, play events → batcher → recent/popularity. Prints statuses only (no tokens).
set -euo pipefail
cd "$(dirname "$0")/../.."
# shellcheck source=tools/dev/load-secrets.sh disable=SC1091
source tools/dev/load-secrets.sh >/dev/null 2>&1
URL=${URL:-$(cat "${LOG_DIR:-/workspace/logs}/miniapp-url.txt")}
INIT=$(cd tools/initdata-mock && GOTOOLCHAIN=go1.22.12 go run . -format raw -lang en -user-id "${TG_ID:-100000001}" 2>/dev/null)
H=(-s -H 'Content-Type: application/json' -H 'ngrok-skip-browser-warning: 1')
AT=$(curl "${H[@]}" -X POST "$URL/api/v1/auth/telegram" -d "$(python3 -c 'import json,sys;print(json.dumps({"initData":sys.argv[1]}))' "$INIT")" |
  python3 -c 'import json,sys;print(json.load(sys.stdin)["accessToken"])')
A=(-H "Authorization: Bearer $AT")
api() { curl "${H[@]}" "${A[@]}" -X "$1" "$URL/api/v1$2" ${3:+-d "$3"} -w '\n%{http_code}'; }
status() { tail -n1; }
body() { sed '$d'; }
js() { python3 -c "import json,sys;d=json.load(sys.stdin);print($1)"; }
ok=0; fail=0
check() { if [[ "$2" == "$3" ]]; then echo "  ok   $1 ($2)"; ok=$((ok+1)); else echo "  FAIL $1: got $2, want $3"; fail=$((fail+1)); fi; }

echo "login: ok (synthetic initData → JWT)"
P=$(api GET '/tracks/popular?limit=5'); check "popular" "$(status <<<"$P")" 200
echo "       top: $(body <<<"$P" | js '", ".join(t["artist"].split()[-1]+" — "+t["title"] for t in d["items"][:3])')"
TID=$(body <<<"$P" | js 'd["items"][0]["id"]')
COVER=$(body <<<"$P" | js 'd["items"][0]["coverUrl"]')
S=$(api GET '/tracks/search?q=chopin&limit=20'); check "search chopin" "$(status <<<"$S")" 200
echo "       found: $(body <<<"$S" | js 'len(d["items"])') tracks"
check "search too short (400)" "$(api GET '/tracks/search?q=' | status)" 400
page2=$(body <<<"$P" | js 'd["nextCursor"]')
check "popular page 2 (cursor)" "$(api GET "/tracks/popular?limit=5&cursor=$page2" | status)" 200

SU=$(api GET "/tracks/$TID/stream-url"); check "stream-url" "$(status <<<"$SU")" 200
SURL=$(body <<<"$SU" | js 'd["url"]')
[[ "$SURL" == /* ]] && SURL="$URL$SURL"
check "stream full (no auth header)" "$(curl -s -o /dev/null -w '%{http_code}' -H 'ngrok-skip-browser-warning: 1' "$SURL")" 200
R=$(curl -s -o /dev/null -D - -H 'ngrok-skip-browser-warning: 1' -H 'Range: bytes=1000-1999' "$SURL")
check "stream range 206" "$(head -1 <<<"$R" | awk '{print $2}')" 206
echo "       $(grep -i '^content-range' <<<"$R" | tr -d '\r') / $(grep -i '^content-type' <<<"$R" | tr -d '\r')"
check "stream tampered sig (403)" "$(curl -s -o /dev/null -w '%{http_code}' -H 'ngrok-skip-browser-warning: 1' "${SURL/sig=/sig=x}")" 403
[[ "$COVER" == /* ]] && COVER="$URL$COVER"
check "cover image" "$(curl -s -o /dev/null -w '%{http_code} %{content_type}' -H 'ngrok-skip-browser-warning: 1' "$COVER")" "200 image/jpeg"

check "like" "$(api POST "/tracks/$TID/like" | status)" 204
check "liked contains track" "$(api GET /tracks/liked | body | js "any(t['id']=='$TID' and t['liked'] for t in d['items'])")" True
check "unlike" "$(api DELETE "/tracks/$TID/like" | status)" 204
check "like again (kept for demo)" "$(api POST "/tracks/$TID/like" | status)" 204
check "like unknown track (404)" "$(api POST /tracks/00000000-0000-4000-8000-000000000000/like | status)" 404

PL=$(api POST /playlists '{"title":"E2E mix"}'); check "playlist create" "$(status <<<"$PL")" 200
PID=$(body <<<"$PL" | js 'd["id"]')
check "playlist add track" "$(api POST "/playlists/$PID/tracks" "{\"trackId\":\"$TID\"}" | status)" 204
check "playlist rename" "$(api PATCH "/playlists/$PID" '{"title":"E2E mix (renamed)"}' | body | js 'd["title"]')" "E2E mix (renamed)"
check "playlist get (1 track)" "$(api GET "/playlists/$PID" | body | js 'len(d["tracks"])')" 1
check "playlist list" "$(api GET /playlists | status)" 200
check "playlist remove track" "$(api DELETE "/playlists/$PID/tracks/$TID" | status)" 204
check "playlist delete" "$(api DELETE "/playlists/$PID" | status)" 204
check "playlist gone (404)" "$(api GET "/playlists/$PID" | status)" 404
check "playlist empty title (400)" "$(api POST /playlists '{"title":"  "}' | status)" 400

W=$(api POST /wave/sessions); check "wave start" "$(status <<<"$W")" 200
WID=$(body <<<"$W" | js 'd["sessionId"]'); LAST=$(body <<<"$W" | js 'd["tracks"][-1]["id"]')
echo "       first batch: $(body <<<"$W" | js 'len(d["tracks"])') tracks"
check "wave feedback skip" "$(api POST "/wave/sessions/$WID/feedback" "{\"trackId\":\"$LAST\",\"action\":\"skip\"}" | status)" 204
N=$(api GET "/wave/sessions/$WID/tracks?after=$LAST"); check "wave next batch" "$(status <<<"$N")" 200
echo "       next batch: $(body <<<"$N" | js 'len(d)') tracks"

check "position save" "$(api PUT "/tracks/$TID/position" '{"positionSec":42.5}' | status)" 204
check "position get" "$(api GET "/tracks/$TID/position" | body | js 'd["positionSec"]')" 42.5
check "track-listened" "$(api POST /events/track-listened "{\"trackId\":\"$TID\",\"positionSec\":30,\"completed\":true}" | status)" 204
sleep 2
check "recent has track" "$(api GET /tracks/recent | body | js "d['items'][0]['id']=='$TID'")" True
echo
echo "passed $ok, failed $fail"
[[ $fail -eq 0 ]]
