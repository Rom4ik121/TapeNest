#!/usr/bin/env bash
# DEV ONLY: native PostgreSQL 16 + Redis 7.4 + MinIO + Navidrome for machines without Docker
# (the AI dev box). On machines with Docker use `make infra-up` instead.
# Idempotent: starts services if down, creates role/db/schemas from .env and
# deploy/pg/init/*.sql. Usage: infra-native.sh [up|down|status]
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
LOGS="${LOGS_DIR:-/workspace/logs}"
DATA="${DEV_DATA_DIR:-/workspace/.devdata}"
if [[ -z "${REDIS_BIN:-}" ]]; then
  if [[ -x /opt/redis-7.4/bin/redis-server ]]; then REDIS_BIN=/opt/redis-7.4/bin
  else REDIS_BIN="$(dirname "$(command -v redis-server)")"; fi
fi
if [[ -z "${PG_VER:-}" ]]; then
  PG_VER="$(pg_lsclusters -h 2>/dev/null | awk '$2=="main"{print $1; exit}')"
  PG_VER="${PG_VER:-16}"
fi
MINIO_BIN="${MINIO_BIN:-$HOME/.local/bin/minio}"
NAVIDROME_BIN="${NAVIDROME_BIN:-$HOME/.local/share/navidrome/navidrome}"
mkdir -p "$LOGS" "$DATA/redis" "$DATA/minio" "$DATA/navidrome"
export PGOPTIONS="-c client_min_messages=warning"
set -a; # shellcheck disable=SC1091
source "$ROOT/.env"; set +a

minio_up() { curl -fsS -m 2 "http://127.0.0.1:${MINIO_API_PORT:-9000}/minio/health/live" >/dev/null 2>&1; }
navidrome_up() { curl -fsS -m 2 "http://127.0.0.1:${NAVIDROME_PORT:-4533}/ping" >/dev/null 2>&1; }
redis_cli() { "$REDIS_BIN/redis-cli" -p "${REDIS_PORT:-6379}" "$@"; }

up() {
  if ! pg_lsclusters -h | awk -v v="$PG_VER" '$1==v && $2=="main" {print $4}' | grep -q online; then
    sudo pg_ctlcluster "$PG_VER" main start
  fi
  if ! redis_cli ping >/dev/null 2>&1; then
    "$REDIS_BIN/redis-server" --daemonize yes --bind 127.0.0.1 --port "${REDIS_PORT:-6379}" \
      --dir "$DATA/redis" --appendonly yes --pidfile "$LOGS/redis.pid" --logfile "$LOGS/redis.log"
    for _ in $(seq 1 20); do redis_cli ping >/dev/null 2>&1 && break; sleep 0.2; done
  fi
  # MinIO (S3) for download-service media; creds from .env (never printed)
  if [[ -x "$MINIO_BIN" ]] && ! minio_up; then
    MINIO_ROOT_USER="$MINIO_ROOT_USER" MINIO_ROOT_PASSWORD="$MINIO_ROOT_PASSWORD" MINIO_BROWSER=on \
      setsid nohup "$MINIO_BIN" server "$DATA/minio" --quiet --address "127.0.0.1:${MINIO_API_PORT:-9000}" \
      --console-address "127.0.0.1:${MINIO_CONSOLE_PORT:-9001}" >>"$LOGS/minio.log" 2>&1 </dev/null &
    echo $! >"$LOGS/minio.pid"
    for _ in $(seq 1 50); do minio_up && break; sleep 0.2; done
  fi
  # Navidrome (Subsonic API) for music-service streaming; internal only (127.0.0.1).
  # The admin user is created once via /auth/createAdmin from NAVIDROME_USER/PASSWORD
  # (not ND_DEVAUTOCREATEADMINPASSWORD: that one logs the password in clear text).
  if [[ -x "$NAVIDROME_BIN" ]] && ! navidrome_up; then
    mkdir -p "${MUSIC_DIR:-$DATA/music}"
    ND_MUSICFOLDER="${MUSIC_DIR:-$DATA/music}" ND_DATAFOLDER="$DATA/navidrome" ND_ADDRESS=127.0.0.1 \
      ND_PORT="${NAVIDROME_PORT:-4533}" ND_SCANSCHEDULE="@every 1m" ND_LOGLEVEL=info \
      ND_ENABLEINSIGHTSCOLLECTOR=false ND_ENABLEEXTERNALSERVICES=false ND_LASTFM_ENABLED=false \
      ND_SPOTIFY_ID="" \
      setsid nohup "$NAVIDROME_BIN" >>"$LOGS/navidrome.log" 2>&1 </dev/null &
    echo $! >"$LOGS/navidrome.pid"
    for _ in $(seq 1 50); do navidrome_up && break; sleep 0.2; done
  fi
  if navidrome_up; then
    # 200 = created; 403 = an admin already exists (idempotent). Body via stdin, never printed.
    python3 -c 'import json,os;print(json.dumps({"username":os.environ.get("NAVIDROME_USER","admin"),"password":os.environ["NAVIDROME_PASSWORD"]}))' |
      curl -s -o /dev/null -X POST -H 'Content-Type: application/json' --data-binary @- \
        "http://127.0.0.1:${NAVIDROME_PORT:-4533}/auth/createAdmin" || true
  fi
  # role + database (password from .env; never printed)
  sudo -u postgres psql -qtA -v ON_ERROR_STOP=1 -v u="$POSTGRES_USER" -v p="$POSTGRES_PASSWORD" <<'SQL' >/dev/null
SELECT format('CREATE ROLE %I LOGIN', :'u') WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = :'u') \gexec
ALTER ROLE :"u" WITH LOGIN PASSWORD :'p';
SQL
  # "<db>_test" is used by Go integration tests (TEST_DATABASE_URL)
  for db in "$POSTGRES_DB" "${POSTGRES_DB}_test"; do
    if ! sudo -u postgres psql -qtA -c "SELECT 1 FROM pg_database WHERE datname='$db'" | grep -q 1; then
      sudo -u postgres createdb -O "$POSTGRES_USER" "$db"
    fi
  done
  for f in "$ROOT"/deploy/pg/init/*.sql; do
    sudo -u postgres psql -q -v ON_ERROR_STOP=1 -d "$POSTGRES_DB" -f "$f" >/dev/null
  done
  # schemas are owned by the app role (docker init runs as that role already)
  sudo -u postgres psql -q -d "$POSTGRES_DB" -c \
    "DO \$\$DECLARE s text; BEGIN FOREACH s IN ARRAY ARRAY['gateway','bot','download','music','streaming','acquisition'] LOOP IF EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = s) THEN EXECUTE format('ALTER SCHEMA %I OWNER TO %I', s, '$POSTGRES_USER'); END IF; END LOOP; END\$\$;" >/dev/null
  status
}
down() {
  if [[ -f "$LOGS/navidrome.pid" ]]; then kill "$(cat "$LOGS/navidrome.pid")" 2>/dev/null || true; rm -f "$LOGS/navidrome.pid"; fi
  if [[ -f "$LOGS/minio.pid" ]]; then kill "$(cat "$LOGS/minio.pid")" 2>/dev/null || true; rm -f "$LOGS/minio.pid"; fi
  redis_cli shutdown >/dev/null 2>&1 || true
  sudo pg_ctlcluster "$PG_VER" main stop || true
}
status() {
  echo "postgres: $(pg_lsclusters -h | awk -v v="$PG_VER" '$1==v {print $4}')"
  echo "redis:    $(redis_cli ping 2>/dev/null || echo down) ($("$REDIS_BIN/redis-server" --version | awk '{print $3}'))"
  echo "minio:    $(minio_up && echo up || echo down)"
  echo "navidrome: $(navidrome_up && echo up || echo down)"
}
case "${1:-up}" in up) up ;; down) down ;; status) status ;; *) echo "usage: $0 [up|down|status]"; exit 2 ;; esac
