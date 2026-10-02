#!/usr/bin/env bash
# E2E: signed initData → POST /auth/telegram via ngrok → /me → refresh → reuse detection → logout.
set -euo pipefail
cd "$(dirname "$0")/../.."
# shellcheck source=tools/dev/load-secrets.sh disable=SC1091
source tools/dev/load-secrets.sh >/dev/null 2>&1
URL=${URL:-$(cat "${LOG_DIR:-/workspace/logs}/miniapp-url.txt")}
INIT=$(cd tools/initdata-mock && GOTOOLCHAIN=go1.22.12 go run . -format raw -lang en 2>/dev/null)
H=(-H 'Content-Type: application/json' -H 'ngrok-skip-browser-warning: 1' -s)
code() { python3 -c 'import json,sys;d=json.load(sys.stdin);print(d.get("code",""))'; }
LOGIN=$(curl "${H[@]}" -X POST "$URL/api/v1/auth/telegram" -d "$(python3 -c 'import json,sys;print(json.dumps({"initData":sys.argv[1]}))' "$INIT")")
echo "$LOGIN" | python3 -c 'import json,sys;d=json.load(sys.stdin);print("login: user.telegramId=",d["user"]["telegramId"],"role=",d["user"].get("role"),"expiresIn=",d.get("expiresIn"),"access=",len(d["accessToken"])>20,"refresh=",len(d["refreshToken"])>20)'
AT=$(echo "$LOGIN" | python3 -c 'import json,sys;print(json.load(sys.stdin)["accessToken"])')
RT=$(echo "$LOGIN" | python3 -c 'import json,sys;print(json.load(sys.stdin)["refreshToken"])')
echo "me: $(curl "${H[@]}" -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $AT" "$URL/api/v1/me")"
R2=$(curl "${H[@]}" -X POST "$URL/api/v1/auth/refresh" -d "{\"refreshToken\":\"$RT\"}")
AT2=$(echo "$R2" | python3 -c 'import json,sys;print(json.load(sys.stdin)["accessToken"])')
RT2=$(echo "$R2" | python3 -c 'import json,sys;print(json.load(sys.stdin)["refreshToken"])')
echo "refresh: rotated=$([ "$RT" != "$RT2" ] && echo yes || echo no)  me(new)=$(curl "${H[@]}" -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $AT2" "$URL/api/v1/me")"
echo "reuse old refresh: $(curl "${H[@]}" -w ' %{http_code}' -X POST "$URL/api/v1/auth/refresh" -d "{\"refreshToken\":\"$RT\"}" | sed 's/.*"code":"\([A-Z_]*\)".* \([0-9]*\)$/\2 \1/')"
echo "family revoked → rotated refresh: $(curl "${H[@]}" -w ' %{http_code}' -X POST "$URL/api/v1/auth/refresh" -d "{\"refreshToken\":\"$RT2\"}" | sed 's/.*"code":"\([A-Z_]*\)".* \([0-9]*\)$/\2 \1/')"
echo "access of revoked session: $(curl "${H[@]}" -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $AT2" "$URL/api/v1/me")"
L=$(curl "${H[@]}" -X POST "$URL/api/v1/auth/telegram" -d "$(python3 -c 'import json,sys;print(json.dumps({"initData":sys.argv[1]}))' "$INIT")")
RT3=$(echo "$L" | python3 -c 'import json,sys;print(json.load(sys.stdin)["refreshToken"])')
echo "logout: $(curl "${H[@]}" -o /dev/null -w '%{http_code}' -X POST "$URL/api/v1/auth/logout" -d "{\"refreshToken\":\"$RT3\"}")  refresh after logout: $(curl "${H[@]}" -o /dev/null -w '%{http_code}' -X POST "$URL/api/v1/auth/refresh" -d "{\"refreshToken\":\"$RT3\"}")"
BAD=${INIT/hash=/hash=00}
echo "tampered initData: $(curl "${H[@]}" -w ' %{http_code}' -X POST "$URL/api/v1/auth/telegram" -d "$(python3 -c 'import json,sys;print(json.dumps({"initData":sys.argv[1]}))' "$BAD")" | sed 's/.*"code":"\([A-Z_]*\)".* \([0-9]*\)$/\2 \1/')"
L4=$(curl "${H[@]}" -X POST "$URL/api/v1/auth/telegram" -d "$(python3 -c 'import json,sys;print(json.dumps({"initData":sys.argv[1]}))' "$INIT")")
AT4=$(echo "$L4" | python3 -c 'import json,sys;print(json.load(sys.stdin)["accessToken"])')
echo "music via gateway (music-service → 200): $(curl "${H[@]}" -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $AT4" "$URL/api/v1/tracks/popular")"
echo "cors preflight: $(curl -s -o /dev/null -w '%{http_code}' -X OPTIONS -H "Origin: $URL" -H 'Access-Control-Request-Method: POST' -H 'ngrok-skip-browser-warning: 1' "$URL/api/v1/auth/telegram")"
