#!/usr/bin/env bash
# E2E (stage "reco", ADR 0010): My Wave through the public tunnel
#   ngrok → Vite → api-gateway → music-service → reco-service
# Covers:
#   - cold start;
#   - listening signals → reco-worker (Redis streams) → taste profile;
#   - personalised batch with "because you liked" reasons;
#   - modes (calm vs energetic by audio energy, discover, favorites);
#   - the breaker: reco stopped → heuristic fallback, reco back → reco again.
# Prints statuses and track names only (no tokens). Uses a synthetic Telegram
# user (TG_ID); no messages are sent to anyone.
set -euo pipefail
cd "$(dirname "$0")/../.."
# shellcheck source=tools/dev/lib.sh disable=SC1091
source tools/dev/lib.sh
load_env
set +u
URL=${URL:-$(cat "$LOG_DIR/miniapp-url.txt")}
RECO="http://127.0.0.1:${RECO_SERVICE_PORT:-8086}"
TG_ID=${TG_ID:-$((200000000 + RANDOM))} # fresh user each run → real cold start
INIT=$(cd tools/initdata-mock && GOTOOLCHAIN=go1.22.12 go run . -format raw -lang en -user-id "$TG_ID" 2>/dev/null)
H=(-s -H 'Content-Type: application/json' -H 'ngrok-skip-browser-warning: 1')
LOGIN=$(curl "${H[@]}" -X POST "$URL/api/v1/auth/telegram" -d "$(python3 -c 'import json,sys;print(json.dumps({"initData":sys.argv[1]}))' "$INIT")")
AT=$(python3 -c 'import json,sys;print(json.load(sys.stdin)["accessToken"])' <<<"$LOGIN")
UID_=$(python3 -c 'import json,sys;print(json.load(sys.stdin)["user"]["id"])' <<<"$LOGIN")
A=(-H "Authorization: Bearer $AT")
api() { curl "${H[@]}" "${A[@]}" -X "$1" "$URL/api/v1$2" ${3:+-d "$3"} -D /tmp/e2e-reco.h -w '\n%{http_code}'; }
reco() { curl -s -H "X-Internal-Token: $INTERNAL_API_TOKEN" "$RECO$1"; }
status() { tail -n1; }
body() { sed '$d'; }
js() { python3 -c "import json,sys;d=json.load(sys.stdin);print($1)"; }
ok=0; fail=0
check() { if [[ "$2" == "$3" ]]; then echo "  ok   $1 ($2)"; ok=$((ok+1)); else echo "  FAIL $1: got $2, want $3"; fail=$((fail+1)); fi; }
wave() { api POST /wave/sessions "{\"mode\":\"$1\"}"; }
show() { body <<<"$1" | js '"\n".join("       "+t["artist"][:22].ljust(22)+" "+t["title"][:34].ljust(34)+" ← "+(t.get("reason") or {}).get("kind","-")+(" «"+t["reason"]["refTitle"][:24]+"»" if (t.get("reason") or {}).get("refTitle") else "") for t in d["tracks"][:'"${2:-4}"'])'; }
energy() { # mean energy percentile of a batch (reco /similar per track)
  body <<<"$1" | js '" ".join(t["id"] for t in d["tracks"])' | tr ' ' '\n' |
    while read -r id; do reco "/internal/v1/tracks/$id/similar?limit=1" | js 'd["energyPct"]'; done |
    python3 -c 'import sys;v=[float(x) for x in sys.stdin];print(round(sum(v)/len(v),2))'
}

echo "login: ok (synthetic user tg=$TG_ID)"
check "reco-service ready" "$(curl -s -o /dev/null -w '%{http_code}' "$RECO/readyz")" 200
echo "       model: $(reco /internal/v1/model | js '"v"+str(d["version"])+", "+str(d["tracks"])+" tracks, "+str(d["withAudioFeatures"])+" analysed"')"

# 1. cold start
W=$(wave default); check "cold start: wave 200" "$(status <<<"$W")" 200
check "cold start: strategy reco" "$(body <<<"$W" | js 'd["strategy"]')" reco
check "cold start: header X-Wave-Strategy" "$(grep -i '^x-wave-strategy' /tmp/e2e-reco.h | awk '{print $2}' | tr -d '\r')" reco
check "cold start: 10 tracks, every one explained" "$(body <<<"$W" | js 'sum(1 for t in d["tracks"] if t.get("reason"))')" 10
check "cold start: no same artist back to back" "$(body <<<"$W" | js 'all(a["artist"]!=b["artist"] for a,b in zip(d["tracks"],d["tracks"][1:]))')" True
show "$W" 3

# 2. listening signals: like three rags, finish one, skip a baroque piece early
RAGS=$(api GET '/tracks/search?q=rag&limit=10' | body | js '" ".join(t["id"] for t in d["items"][:3])')
for id in $RAGS; do api POST "/tracks/$id/like" >/dev/null; done
FIRST=${RAGS%% *}
check "track-listened (completed)" "$(api POST /events/track-listened "{\"trackId\":\"$FIRST\",\"positionSec\":180,\"completed\":true}" | status)" 204
BAROQUE=$(api GET '/tracks/search?q=goldberg&limit=1' | body | js 'd["items"][0]["id"]')
check "track-skipped (early, 8 s)" "$(api POST /events/track-skipped "{\"trackId\":\"$BAROQUE\",\"positionSec\":8}" | status)" 204
check "track-skipped bad body (400)" "$(api POST /events/track-skipped '{"trackId":"x","positionSec":1}' | status)" 400
sleep 3
PROF=$(reco "/internal/v1/users/$UID_/profile")
check "profile: 3 likes ingested" "$(js 'd["likes"]' <<<"$PROF")" 3
check "profile: early skip ingested" "$(js 'd["earlySkips"]' <<<"$PROF")" 1
echo "       top genres: $(js '", ".join(g["label"]+" "+str(g["weight"]) for g in d["top"].get("genre",[])[:3])' <<<"$PROF")"

# 3. personalised batch
W=$(wave default); check "personal: strategy reco" "$(body <<<"$W" | js 'd["strategy"]')" reco
check "personal: reasons point at the new taste" "$(body <<<"$W" | js 'any((t.get("reason") or {}).get("kind") in ("because_you_liked","artist_you_like","genre_you_like","favorite") for t in d["tracks"])')" True
rag=$(body <<<"$W" | js '" ".join(t["id"] for t in d["tracks"])' | tr ' ' '\n' | while read -r id; do reco "/internal/v1/tracks/$id/similar?limit=1" | js 'd["genre"]'; done | grep -c '^Ragtime$' || true)
echo "       ragtime in batch: $rag/10 (catalog share 28/134)"
check "personal: ragtime over-represented (≥ 4/10)" "$(( rag >= 4 ))" 1
check "personal: skipped track not served" "$(body <<<"$W" | js "all(t['id']!='$BAROQUE' for t in d['tracks'])")" True
show "$W" 5
WID=$(body <<<"$W" | js 'd["sessionId"]'); T1=$(body <<<"$W" | js 'd["tracks"][1]["id"]'); LAST=$(body <<<"$W" | js 'd["tracks"][-1]["id"]')
check "wave feedback like" "$(api POST "/wave/sessions/$WID/feedback" "{\"trackId\":\"$T1\",\"action\":\"like\"}" | status)" 204
N=$(api GET "/wave/sessions/$WID/tracks?after=$LAST"); check "next batch (reasons kept)" "$(body <<<"$N" | js 'all(t.get("reason") for t in d) and len(d)>0')" True
sleep 2
check "Thompson arm updated from wave feedback" "$(reco "/internal/v1/users/$UID_/profile" | js 'sum(v["Alpha"] for v in d["sources"].values())>0')" True

# 4. modes
C=$(wave calm); E=$(wave energetic)
ce=$(energy "$C"); ee=$(energy "$E")
echo "       mean energy percentile: calm $ce vs energetic $ee"
check "modes: energetic batch louder/denser than calm" "$(python3 -c "print($ee > $ce + 0.2)")" True
check "mode discover: no liked tracks" "$(wave discover | body | js 'any(t["liked"] for t in d["tracks"])')" False
check "mode favorites: liked tracks first" "$(wave favorites | body | js 'd["tracks"][0]["liked"]')" True
check "unknown mode (400)" "$(wave party | status)" 400

# 5. breaker: reco down → heuristic fallback, reco back → reco
stop_proc reco-service >/dev/null
t0=$(date +%s%N)
F=$(wave default); dt=$(( ($(date +%s%N) - t0) / 1000000 ))
check "reco down: wave still 200" "$(status <<<"$F")" 200
check "reco down: strategy fallback" "$(body <<<"$F" | js 'd["strategy"]')" fallback
check "reco down: 10 tracks with heuristic reasons" "$(body <<<"$F" | js 'sum(1 for t in d["tracks"] if t.get("reason"))')" 10
echo "       fallback latency ${dt} ms"
for _ in 1 2 3; do wave default >/dev/null; done
check "breaker open → music readyz reco degraded" "$(curl -s -H "X-Internal-Token: $INTERNAL_API_TOKEN" "http://127.0.0.1:${MUSIC_SERVICE_PORT:-8084}/readyz" | js 'd.get("reco")')" degraded
start_reco >/dev/null
echo "       reco restarted; waiting for the breaker half-open probe (≤ 35 s)…"
for _ in $(seq 1 40); do
  s=$(wave default | body | js 'd["strategy"]'); [[ "$s" == reco ]] && break; sleep 1
done
check "reco back: strategy reco" "$s" reco
echo
echo "passed $ok, failed $fail"
[[ $fail -eq 0 ]]
