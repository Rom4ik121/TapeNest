#!/usr/bin/env bash
# DEV ONLY: native Lidarr + Prowlarr + qBittorrent-nox (the music acquisition stack,
# ADR 0011) for machines without Docker. Versions are pinned and every download is
# verified against the upstream SHA-256 (Servarr update API / GitHub asset digest).
# All three listen on 127.0.0.1 only. API keys / passwords come from .env and are
# never printed. NO indexers are configured here — the operator adds their own in
# Prowlarr (see services/acquisition-service/README.md).
# Usage: arr-native.sh [install|up|down|status]
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
LOGS="${LOGS_DIR:-/workspace/logs}"
DATA="${DEV_DATA_DIR:-/workspace/.devdata}"
OPT="${ARR_OPT_DIR:-$HOME/.local/share/arr}"
LIDARR_VERSION=3.1.0.4875
LIDARR_SHA256=20f175aec2b908de2ae06b72544dae504efddb5db2a97ea4e36866de53bc2fa5
PROWLARR_VERSION=2.6.5.5623
PROWLARR_SHA256=c0824e9f0e9c79e085882f6143041be5c2cde0519baa59e9477d9e4dcbe1b5e3
QBT_VERSION=5.2.3_v2.0.14
QBT_SHA256=c1839caf9b7dbddee09e9a4394bb5b17dc70ecd7c3a9b45e84331d5a1389a645
set -a; # shellcheck disable=SC1091
source "$ROOT/.env"; set +a
TORRENTS="${TORRENT_DIR:-$DATA/torrents}"
mkdir -p "$LOGS" "$DATA/lidarr" "$DATA/prowlarr" "$DATA/qbittorrent/qBittorrent/config" \
  "$TORRENTS/incomplete" "$TORRENTS/complete" "${MUSIC_DIR:-$DATA/music}/library"

fetch() { # fetch <url> <sha256> <out>
  local tmp; tmp="$(mktemp)"
  curl -fsSL --retry 3 -o "$tmp" "$1"
  if ! echo "$2  $tmp" | sha256sum -c --status; then rm -f "$tmp"; echo "checksum mismatch: $1" >&2; return 1; fi
  mv "$tmp" "$3"
}

install() {
  mkdir -p "$OPT"
  if [[ "$(cat "$OPT/Lidarr/.version" 2>/dev/null)" != "$LIDARR_VERSION" ]]; then
    fetch "https://github.com/Lidarr/Lidarr/releases/download/v$LIDARR_VERSION/Lidarr.master.$LIDARR_VERSION.linux-core-x64.tar.gz" "$LIDARR_SHA256" "$OPT/lidarr.tgz"
    rm -rf "$OPT/Lidarr"; tar xzf "$OPT/lidarr.tgz" -C "$OPT"; rm -f "$OPT/lidarr.tgz"
    echo "$LIDARR_VERSION" >"$OPT/Lidarr/.version"
  fi
  if [[ "$(cat "$OPT/Prowlarr/.version" 2>/dev/null)" != "$PROWLARR_VERSION" ]]; then
    fetch "https://github.com/Prowlarr/Prowlarr/releases/download/v$PROWLARR_VERSION/Prowlarr.master.$PROWLARR_VERSION.linux-core-x64.tar.gz" "$PROWLARR_SHA256" "$OPT/prowlarr.tgz"
    rm -rf "$OPT/Prowlarr"; tar xzf "$OPT/prowlarr.tgz" -C "$OPT"; rm -f "$OPT/prowlarr.tgz"
    echo "$PROWLARR_VERSION" >"$OPT/Prowlarr/.version"
  fi
  if ! echo "$QBT_SHA256  $OPT/qbittorrent-nox" | sha256sum -c --status 2>/dev/null; then
    fetch "https://github.com/userdocs/qbittorrent-nox-static/releases/download/release-$QBT_VERSION/x86_64-qbittorrent-nox" "$QBT_SHA256" "$OPT/qbittorrent-nox"
    chmod 755 "$OPT/qbittorrent-nox"
  fi
  echo "installed: Lidarr $LIDARR_VERSION, Prowlarr $PROWLARR_VERSION, qBittorrent-nox $QBT_VERSION (sha256 verified)"
}

# Servarr config.xml: 127.0.0.1 only, API key from .env, no auto-update/analytics.
servarr_config() { # servarr_config <file> <port> <env var with api key> <name>
  [[ -f "$1" ]] && grep -q "<ApiKey>" "$1" && return 0
  (umask 077; KEYVAR="$3" PORT="$2" NAME="$4" python3 - "$1" <<'PY'
import os, sys
key = os.environ[os.environ["KEYVAR"]]
open(sys.argv[1], "w").write(f"""<Config>
  <BindAddress>127.0.0.1</BindAddress>
  <Port>{os.environ['PORT']}</Port>
  <SslPort>0</SslPort>
  <EnableSsl>False</EnableSsl>
  <ApiKey>{key}</ApiKey>
  <AuthenticationMethod>Forms</AuthenticationMethod>
  <AuthenticationRequired>DisabledForLocalAddresses</AuthenticationRequired>
  <UrlBase></UrlBase>
  <UpdateMechanism>External</UpdateMechanism>
  <UpdateAutomatically>False</UpdateAutomatically>
  <AnalyticsEnabled>False</AnalyticsEnabled>
  <LaunchBrowser>False</LaunchBrowser>
  <LogLevel>info</LogLevel>
  <InstanceName>{os.environ['NAME']}</InstanceName>
</Config>
""")
PY
  )
}

# qBittorrent: WebUI on 127.0.0.1 with a PBKDF2 password from .env. Everything else
# (limits, paths, ratio) is applied through the WebUI API by acquisition-worker.
qbt_config() {
  local f="$DATA/qbittorrent/qBittorrent/config/qBittorrent.conf"
  [[ -f "$f" ]] && grep -q "Password_PBKDF2" "$f" && return 0
  (umask 077; F="$f" T="$TORRENTS" python3 <<'PY'
import base64, hashlib, os
salt = os.urandom(16)
dk = hashlib.pbkdf2_hmac("sha512", os.environ["QBITTORRENT_PASSWORD"].encode(), salt, 100000, 64)
pw = base64.b64encode(salt).decode() + ":" + base64.b64encode(dk).decode()
t = os.environ["T"]
open(os.environ["F"], "w").write(f"""[LegalNotice]
Accepted=true

[BitTorrent]
Session\\DefaultSavePath={t}/complete
Session\\TempPath={t}/incomplete
Session\\TempPathEnabled=true
Session\\Port=6881

[Preferences]
General\\Locale=en
WebUI\\Address=127.0.0.1
WebUI\\Port={os.environ.get('QBT_PORT', '8092')}
WebUI\\Username={os.environ.get('QBITTORRENT_USER', 'tapenest')}
WebUI\\Password_PBKDF2="@ByteArray({pw})"
WebUI\\LocalHostAuth=true
WebUI\\CSRFProtection=true
WebUI\\HostHeaderValidation=true
""")
PY
  )
}

port_of() { python3 -c 'import sys,urllib.parse;print(urllib.parse.urlparse(sys.argv[1]).port)' "$1"; }
LIDARR_PORT="$(port_of "${LIDARR_URL:-http://127.0.0.1:8686}")"
PROWLARR_PORT="$(port_of "${PROWLARR_URL:-http://127.0.0.1:9696}")"
QBT_PORT="$(port_of "${QBITTORRENT_URL:-http://127.0.0.1:8092}")"
export QBT_PORT
lidarr_up() { curl -fsS -m 2 "http://127.0.0.1:$LIDARR_PORT/ping" >/dev/null 2>&1; }
prowlarr_up() { curl -fsS -m 2 "http://127.0.0.1:$PROWLARR_PORT/ping" >/dev/null 2>&1; }
qbt_up() { curl -sS -m 2 "http://127.0.0.1:$QBT_PORT/api/v2/app/version" -o /dev/null -w '%{http_code}' 2>/dev/null | grep -qE '^(200|403)$'; }
alive() { [[ -f "$LOGS/$1.pid" ]] && kill -0 "$(cat "$LOGS/$1.pid")" 2>/dev/null; }
start() { # start <name> <cmd...>
  local name="$1"; shift
  echo "=== $(date -Is) start $name ===" >>"$LOGS/$name.log"
  setsid nohup "$@" >>"$LOGS/$name.log" 2>&1 </dev/null &
  echo $! >"$LOGS/$name.pid"
}
wait_up() { for _ in $(seq 1 150); do "$1" && return 0; sleep 0.4; done; echo "$1: not up (see $LOGS)" >&2; return 1; }

up() {
  [[ -x "$OPT/Lidarr/Lidarr" && -x "$OPT/Prowlarr/Prowlarr" && -x "$OPT/qbittorrent-nox" ]] || install
  servarr_config "$DATA/lidarr/config.xml" "$LIDARR_PORT" LIDARR_API_KEY "TapeNest Lidarr"
  servarr_config "$DATA/prowlarr/config.xml" "$PROWLARR_PORT" PROWLARR_API_KEY "TapeNest Prowlarr"
  qbt_config
  if ! qbt_up; then start qbittorrent "$OPT/qbittorrent-nox" --profile="$DATA/qbittorrent"; fi
  if ! prowlarr_up; then start prowlarr "$OPT/Prowlarr/Prowlarr" -nobrowser -data="$DATA/prowlarr"; fi
  if ! lidarr_up; then start lidarr "$OPT/Lidarr/Lidarr" -nobrowser -data="$DATA/lidarr"; fi
  wait_up qbt_up; wait_up prowlarr_up; wait_up lidarr_up
  status
}
down() {
  for n in lidarr prowlarr qbittorrent; do
    if alive "$n"; then kill "$(cat "$LOGS/$n.pid")" 2>/dev/null || true; fi
    rm -f "$LOGS/$n.pid"
  done
}
status() {
  echo "lidarr:      $(lidarr_up && echo "up :$LIDARR_PORT" || echo down) ($LIDARR_VERSION)"
  echo "prowlarr:    $(prowlarr_up && echo "up :$PROWLARR_PORT" || echo down) ($PROWLARR_VERSION)"
  echo "qbittorrent: $(qbt_up && echo "up :$QBT_PORT" || echo down) ($QBT_VERSION)"
  echo "disk:        torrents $(du -sh "$TORRENTS" 2>/dev/null | cut -f1), library $(du -sh "${MUSIC_DIR:-$DATA/music}" 2>/dev/null | cut -f1), free $(df -h --output=avail "$DATA" | tail -1 | tr -d ' ')"
}
case "${1:-up}" in install) install ;; up) up ;; down) down ;; status) status ;; *) echo "usage: $0 [install|up|down|status]"; exit 2 ;; esac
