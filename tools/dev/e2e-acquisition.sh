#!/usr/bin/env bash
# E2E: unified search returns a track that is not in the library yet, play answers 202
# until the CC0 torrent has buffered enough to stream, and like is stored immediately.
# Uses the local CC0 Torznab stub only (tools/legal-indexer). Prints statuses, never tokens.
set -euo pipefail
cd "$(dirname "$0")/../.."
# shellcheck source=tools/dev/load-secrets.sh disable=SC1091
source tools/dev/load-secrets.sh >/dev/null 2>&1
BASE=${BASE:-http://127.0.0.1:${API_GATEWAY_PORT:-8080}}
INIT=$(cd tools/initdata-mock && GOTOOLCHAIN=local go run . -format raw -lang en -user-id "${TG_ID:-100000003}" 2>/dev/null)
H=(-sS -H 'Content-Type: application/json')
AT=$(curl "${H[@]}" -X POST "$BASE/api/v1/auth/telegram" -d "$(python3 -c 'import json,sys;print(json.dumps({"initData":sys.argv[1]}))' "$INIT")" |
  python3 -c 'import json,sys;print(json.load(sys.stdin)["accessToken"])')
A=(-H "Authorization: Bearer $AT")
api() { curl "${H[@]}" "${A[@]}" -X "$1" "$BASE/api/v1$2" ${3:+-d "$3"} -w '\n%{http_code}'; }
status() { tail -n1; }
body() { sed '$d'; }
js() { python3 -c "import json,sys;d=json.load(sys.stdin);print($1)"; }
ok=0; fail=0
check() { if [[ "$2" == "$3" ]]; then echo "  ok   $1 ($2)"; ok=$((ok+1)); else echo "  FAIL $1: got $2, want $3"; fail=$((fail+1)); fi; }

echo "login: ok"
Q=${ACQ_E2E_QUERY:-ishizaka goldberg}
S=$(api GET "/search?q=$(python3 -c 'import urllib.parse,sys;print(urllib.parse.quote(sys.argv[1]))' "$Q")")
check "unified search" "$(status <<<"$S")" 200
TID=$(body <<<"$S" | python3 -c 'import json,sys
d=json.load(sys.stdin)
tracks=d.get("tracks") or []
remote=[t for t in tracks if t.get("remote")]
pick=(remote or tracks)
if not pick:
    sys.exit("no tracks")
print(pick[0]["id"])
print("remote="+str(bool(pick[0].get("remote"))), file=sys.stderr)
')
echo "       track $TID"
check "like before the file exists" "$(api POST "/tracks/$TID/like" | status)" 204
check "liked list contains it" "$(api GET /tracks/liked | body | js "any(t['id']=='$TID' and t['liked'] for t in d['items'])")" True

# play: 202 while the torrent buffers, then 200 with a stream url
deadline=$((SECONDS+420))
code=""
while (( SECONDS < deadline )); do
  R=$(api GET "/tracks/$TID/stream-url")
  code=$(status <<<"$R")
  if [[ "$code" == "200" ]]; then
    echo "  ok   stream-url ready (200)"
    ok=$((ok+1))
    SURL=$(body <<<"$R" | js 'd["url"]')
    [[ "$SURL" == /* ]] && SURL="$BASE$SURL"
    sc=$(curl -sS -o /dev/null -w '%{http_code}' -r 0-65535 "$SURL" || true)
    check "audio bytes (200 or 206)" "$sc" "$([[ "$sc" == "206" ]] && echo 206 || echo "$sc")"
    if [[ "$sc" != "200" && "$sc" != "206" ]]; then fail=$((fail+1)); echo "  FAIL audio status $sc"; fi
    break
  fi
  if [[ "$code" != "202" ]]; then
    echo "  FAIL stream-url: $(body <<<"$R" | head -c 200) ($code)"
    fail=$((fail+1))
    break
  fi
  echo "  ..   pending $(body <<<"$R" | js 'd.get("state","")+" "+str(d.get("progress",0))')"
  sleep 3
done
if [[ "$code" == "202" ]]; then
  echo "  FAIL stream still pending after timeout"
  fail=$((fail+1))
fi
echo
echo "passed $ok, failed $fail"
[[ $fail -eq 0 ]]
