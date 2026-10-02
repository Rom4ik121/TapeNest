#!/usr/bin/env bash
# DEV-ONLY. Source this file (". tools/dev/load-secrets.sh") to export
# TELEGRAM_BOT_TOKEN and NGROK_AUTHTOKEN from the local, git-ignored secrets
# text file in the repo root (any "*ngrok*.txt", or $TAPENEST_SECRETS_FILE).
# Values are never printed. Lines look like "<label> = <value>".
_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
_file="${TAPENEST_SECRETS_FILE:-}"
if [ -z "$_file" ]; then
  _file="$(find "$_root" -maxdepth 1 -type f -name '*ngrok*.txt' | head -n1)"
fi
if [ -z "$_file" ] || [ ! -f "$_file" ]; then
  echo "load-secrets: secrets file not found (set TAPENEST_SECRETS_FILE)" >&2
  # shellcheck disable=SC2317
  return 1 2>/dev/null || exit 1
fi
while IFS= read -r _line || [ -n "$_line" ]; do
  _val="$(printf '%s' "${_line#*=}" | tr -d '[:space:]')"
  [ -z "$_val" ] && continue
  if printf '%s' "$_val" | grep -Eq '^[0-9]{6,}:[A-Za-z0-9_-]{30,}$'; then
    export TELEGRAM_BOT_TOKEN="$_val"
  elif printf '%s' "$_val" | grep -Eq '^[A-Za-z0-9_]{30,}$'; then
    export NGROK_AUTHTOKEN="$_val"
  fi
done < "$_file"
unset _root _file _line _val
[ -n "${TELEGRAM_BOT_TOKEN:-}" ] || echo "load-secrets: TELEGRAM_BOT_TOKEN not found" >&2
[ -n "${NGROK_AUTHTOKEN:-}" ] || echo "load-secrets: NGROK_AUTHTOKEN not found" >&2
