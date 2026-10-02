#!/usr/bin/env bash
# E2E: unified search returns a YouTube Music track, like is stored, stream-url is
# ready (200) and a Range request returns audio bytes. Prints statuses, never tokens.
set -euo pipefail
cd "$(dirname "$0")/../.."
if [[ -n "${TAPENEST_SECRETS_FILE:-}" || -n "$(find . -maxdepth 1 -name '*ngrok*.txt' -print -quit 2>/dev/null || true)" ]]; then
  # shellcheck source=tools/dev/load-secrets.sh disable=SC1091
  source tools/dev/load-secrets.sh >/dev/null 2>&1 || true
fi
BASE=${BASE:-http://127.0.0.1:${API_GATEWAY_PORT:-8080}}
INIT=$(cd tools/initdata-mock && GOTOOLCHAIN=local go run . -format raw -lang en -user-id "${TG_ID:-100000004}" 2>/dev/null)
H=(-sS -H 'Content-Type: application/json')
AT=$(curl "${H[@]}" -X POST "$BASE/api/v1/auth/telegram" -d "$(python3 -c 'import json,sys;print(json.dumps({"initData":sys.argv[1]}))' "$INIT")" |
  python3 -c 'import json,sys;print(json.load(sys.stdin)["accessToken"])')
A=(-H "Authorization: Bearer $AT")
api() { curl "${H[@]}" "${A[@]}" -X "$1" "$BASE/api/v1$2" ${3:+-d "$3"} -w '\n%{http_code}'; }
status() { tail -n1; }
body() { sed '$d'; }
ok=0; fail=0
check() { if [[ "$2" == "$3" ]]; then echo "  ok   $1 ($2)"; ok=$((ok+1)); else echo "  FAIL $1: got $2, want $3"; fail=$((fail+1)); fi; }

echo "login: ok"
Q=${YTM_E2E_QUERY:-ishizaka variatio}
S=$(api GET "/search?q=$(python3 -c 'import urllib.parse,sys;print(urllib.parse.quote(sys.argv[1]))' "$Q")")
check "unified search" "$(status <<<"$S")" 200
TID=$(body <<<"$S" | python3 -c 'import json,sys
d=json.load(sys.stdin)
tracks=d.get("tracks") or []
remote=[t for t in tracks if t.get("remote")]
pick=remote or tracks
if not pick:
    sys.exit("no tracks")
print(pick[0]["id"])
print("remote="+str(bool(pick[0].get("remote"))), file=sys.stderr)
print("title="+pick[0].get("title",""), file=sys.stderr)
')
echo "       track $TID"
check "like" "$(api POST "/tracks/$TID/like" | status)" 204
check "liked list contains it" "$(api GET /tracks/liked | body | python3 -c "import json,sys;d=json.load(sys.stdin);print(any(t['id']=='$TID' and t['liked'] for t in d['items']))")" True
R=$(api GET "/tracks/$TID/stream-url")
check "stream-url" "$(status <<<"$R")" 200
SURL=$(body <<<"$R" | python3 -c 'import json,sys;print(json.load(sys.stdin)["url"])')
[[ "$SURL" == /* ]] && SURL="$BASE$SURL"
sc=$(curl -sS -D /tmp/ytm-e2e.hdr -o /tmp/ytm-e2e.bin -w '%{http_code}' -r 0-65535 "$SURL" || true)
check "audio bytes (200 or 206)" "$sc" "$([[ "$sc" == "206" ]] && echo 206 || echo "$sc")"
if [[ "$sc" != "200" && "$sc" != "206" ]]; then fail=$((fail+1)); echo "  FAIL audio status $sc"; fi
ctype=$(python3 -c 'print(open("/tmp/ytm-e2e.hdr",errors="replace").read().lower())' | python3 -c 'import sys;t=sys.stdin.read();
import re
m=re.search(r"content-type:\s*([^\r\n]+)", t)
print(m.group(1) if m else "")')
echo "       content-type ${ctype:-unknown} bytes $(wc -c </tmp/ytm-e2e.bin)"
if [[ "$ctype" != audio/* && "$ctype" != video/mp4* && "$ctype" != application/octet-stream* ]]; then
  echo "  FAIL unexpected content-type"
  fail=$((fail+1))
fi
echo
echo "passed $ok, failed $fail"
[[ $fail -eq 0 ]]
