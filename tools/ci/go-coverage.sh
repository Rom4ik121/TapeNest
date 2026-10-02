#!/usr/bin/env bash
# Runs `go test -race` for the Go module in the current directory with coverage
# of ./internal/... (generated sqlc code excluded) and enforces the spec §12
# threshold (≥ 70 % of business logic). Usage: go-coverage.sh [min_percent]
set -euo pipefail
min="${1:-70}"
profile="$(mktemp)"
pkgs="./internal/..."
[[ -d internal ]] || pkgs="./..."
go test -race -count=1 -coverpkg="$pkgs" -coverprofile="$profile" ./...
grep -v -E '/repo/db/|/testutil/|\.pb\.go:' "$profile" >"$profile.f" || true
total="$(go tool cover -func="$profile.f" | awk '/^total:/ {sub("%","",$3); print $3}')"
rm -f "$profile" "$profile.f"
echo "coverage: ${total}% (min ${min}%)"
awk -v t="$total" -v m="$min" 'BEGIN { exit (t + 0 >= m + 0) ? 0 : 1 }' || { echo "coverage below ${min}%"; exit 1; }
