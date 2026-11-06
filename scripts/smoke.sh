#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BIN="${BIN:-$ROOT/nas-filehub}"
if [[ ! -x "$BIN" ]]; then
  (cd "$ROOT" && go build -o "$BIN" ./cmd/server)
fi
TMP=$(mktemp -d)
trap 'kill $PID 2>/dev/null || true; rm -rf "$TMP"' EXIT
mkdir -p "$TMP/files" "$TMP/thumbs"
DATA_ROOT="$TMP/files" DB_PATH="$TMP/db.sqlite" THUMB_CACHE_DIR="$TMP/thumbs" \
  ADMIN_USER=admin ADMIN_PASSWORD='SmokeTest!234' LISTEN_ADDR=127.0.0.1:18081 \
  "$BIN" &
PID=$!
for i in $(seq 1 50); do curl -sf http://127.0.0.1:18081/api/health >/dev/null && break; sleep 0.1; done
curl -sf -c "$TMP/c.jar" -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"SmokeTest!234"}' http://127.0.0.1:18081/api/login >/dev/null
curl -sf -b "$TMP/c.jar" http://127.0.0.1:18081/api/me | grep -q '"username":"admin"'
echo "smoke ok"
