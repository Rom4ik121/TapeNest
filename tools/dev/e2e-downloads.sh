#!/usr/bin/env bash
# E2E (stage 2): real Telegram login through the public URL → POST /api/v1/downloads
# → SSE progress → done → presigned link via /media → dedup. No tokens are printed.
#   VIDEO_URL=https://youtu.be/ScMzIvxBSi4 tools/dev/e2e-downloads.sh
set -euo pipefail
cd "$(dirname "$0")/../.."
# shellcheck source=tools/dev/load-secrets.sh disable=SC1091
source tools/dev/load-secrets.sh >/dev/null 2>&1
URL=${URL:-$(cat "${LOG_DIR:-/workspace/logs}/miniapp-url.txt")}
VIDEO_URL=${VIDEO_URL:-https://youtu.be/ScMzIvxBSi4}
INIT=$(cd tools/initdata-mock && GOTOOLCHAIN=go1.22.12 go run . -format raw -lang ru 2>/dev/null)
H=(-H 'Content-Type: application/json' -H 'ngrok-skip-browser-warning: 1' -s)
j() { python3 -c "import json,sys;d=json.load(sys.stdin);print($1)"; }
AT=$(curl "${H[@]}" -X POST "$URL/api/v1/auth/telegram" -d "$(python3 -c 'import json,sys;print(json.dumps({"initData":sys.argv[1]}))' "$INIT")" | j 'd["accessToken"]')
A=(-H "Authorization: Bearer $AT")
echo "unauthenticated: $(curl "${H[@]}" -o /dev/null -w '%{http_code}' "$URL/api/v1/downloads")"
echo "bad url: $(curl "${H[@]}" "${A[@]}" -X POST "$URL/api/v1/downloads" -d '{"url":"https://example.com/x"}' -w ' %{http_code}')"
now() { date +%s.%N; }
since() { python3 -c "import sys;print(round(float(sys.argv[2])-float(sys.argv[1]),2))" "$1" "$(now)"; }
T0=$(now)
C=$(curl "${H[@]}" "${A[@]}" -X POST "$URL/api/v1/downloads" -d "{\"url\":\"$VIDEO_URL\"}" -w '\n%{http_code}')
ID=$(echo "$C" | head -1 | j 'd["id"]')
echo "create: HTTP $(echo "$C" | tail -1) status=$(echo "$C" | head -1 | j 'd["status"]') id=$ID"
echo "SSE (through ngrok → Vite → gateway → download-service):"
curl -sN --max-time 180 "${A[@]}" -H 'ngrok-skip-browser-warning: 1' "$URL/api/v1/downloads/$ID/events" | python3 -c '
import sys, json
for line in sys.stdin:
    if line.startswith("data: "):
        d = json.loads(line[6:]); p = d.get("progress") or {}
        print("  event:", d["status"], d.get("stage",""), (str(p.get("pct"))+"%") if p else "", d.get("title",""), d.get("errorKind",""))'
echo "elapsed: $(since "$T0")s"
G=$(curl "${H[@]}" "${A[@]}" "$URL/api/v1/downloads/$ID")
echo "get: $(echo "$G" | j 'd["status"], d.get("file"), d.get("errorKind","")')"
[[ "$(echo "$G" | j 'd["status"]')" == "done" ]] || { echo "download did not finish"; exit 1; }
LINK=$(curl "${H[@]}" "${A[@]}" "$URL/api/v1/downloads/$ID/file?redirect=false" | j 'd["url"]')
echo "file link host/path: $(python3 -c 'import sys,urllib.parse as u;p=u.urlparse(sys.argv[1]);print(p.scheme+"://"+p.netloc+p.path, "expires="+dict(u.parse_qsl(p.query)).get("X-Amz-Expires",""))' "$LINK")"
curl -s -H 'ngrok-skip-browser-warning: 1' -o /tmp/e2e-download.mp4 -w "public download: HTTP %{http_code} %{size_download} bytes %{content_type}\n" "$LINK"
ffprobe -v error -show_entries stream=codec_name,width,height -of csv=p=0 /tmp/e2e-download.mp4 | tr '\n' ' '; echo
echo "redirect: $(curl "${H[@]}" "${A[@]}" -o /dev/null -w '%{http_code}' "$URL/api/v1/downloads/$ID/file")"
T1=$(now)
D=$(curl "${H[@]}" "${A[@]}" -X POST "$URL/api/v1/downloads" -d "{\"url\":\"$VIDEO_URL\"}" -w '\n%{http_code}')
echo "dedup (same URL again): HTTP $(echo "$D" | tail -1) status=$(echo "$D" | head -1 | j 'd["status"]') in $(since "$T1")s"
echo "list: $(curl "${H[@]}" "${A[@]}" "$URL/api/v1/downloads?limit=5" | j 'len(d["items"]), "items; nextCursor:", d["nextCursor"] is not None')"
