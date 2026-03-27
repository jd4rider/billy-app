#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_DIR="$(mktemp -d)"
BIN_PATH="$TMP_DIR/billy"
CONFIG_PATH="$TMP_DIR/config.toml"
HISTORY_PATH="$TMP_DIR/history.db"
SERVE_URL="http://127.0.0.1:7437"
BAD_OLLAMA_URL="http://127.0.0.1:65535"
SERVER_PID=""

cleanup() {
  if [[ -n "$SERVER_PID" ]] && kill -0 "$SERVER_PID" >/dev/null 2>&1; then
    kill "$SERVER_PID" >/dev/null 2>&1 || true
    wait "$SERVER_PID" >/dev/null 2>&1 || true
  fi
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "release-smoke: required command not found: $1" >&2
    exit 1
  fi
}

wait_for_server() {
  local attempts=30
  while (( attempts > 0 )); do
    if curl -fsS "$SERVE_URL/status" >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.2
    ((attempts--))
  done
  return 1
}

require_cmd go
require_cmd curl

cat >"$CONFIG_PATH" <<EOF
[backend]
type = "ollama"
url = "$BAD_OLLAMA_URL"

[ollama]
model = "qwen2.5-coder:14b"
temperature = 0.7
num_predict = 2048

[storage]
history_file = "$HISTORY_PATH"
EOF

echo "==> go test ./..."
(cd "$ROOT_DIR" && go test ./...)

echo "==> go build"
(cd "$ROOT_DIR" && go build -o "$BIN_PATH" ./cmd/billy)

echo "==> billy --version"
"$BIN_PATH" --version

echo "==> billy serve smoke test"
BILLY_CONFIG="$CONFIG_PATH" "$BIN_PATH" serve >"$TMP_DIR/serve.log" 2>&1 &
SERVER_PID=$!

if ! wait_for_server; then
  echo "release-smoke: serve mode did not become ready" >&2
  echo "--- $TMP_DIR/serve.log ---" >&2
  cat "$TMP_DIR/serve.log" >&2 || true
  exit 1
fi

STATUS_JSON="$(curl -fsS "$SERVE_URL/status")"
CONFIG_JSON="$(curl -fsS "$SERVE_URL/config")"
HISTORY_JSON="$(curl -fsS "$SERVE_URL/history")"

echo "status:  $STATUS_JSON"
echo "config:  $CONFIG_JSON"
echo "history: $HISTORY_JSON"

echo "$STATUS_JSON" | grep -q '"version"' || { echo "release-smoke: /status missing version" >&2; exit 1; }
echo "$STATUS_JSON" | grep -q '"ollama":false' || { echo "release-smoke: expected unreachable Ollama in /status" >&2; exit 1; }
echo "$CONFIG_JSON" | grep -q '"backendURL":"'"$BAD_OLLAMA_URL"'"' || { echo "release-smoke: /config backendURL mismatch" >&2; exit 1; }
echo "$HISTORY_JSON" | grep -q '^\[' || { echo "release-smoke: /history did not return a JSON array" >&2; exit 1; }
echo "$HISTORY_JSON" | grep -q '^\[\]' || { echo "release-smoke: expected isolated empty history" >&2; exit 1; }

kill "$SERVER_PID" >/dev/null 2>&1 || true
wait "$SERVER_PID" >/dev/null 2>&1 || true
SERVER_PID=""

if command -v goreleaser >/dev/null 2>&1; then
  echo "==> goreleaser release --snapshot --clean"
  (cd "$ROOT_DIR" && goreleaser release --snapshot --clean)
else
  echo "==> goreleaser not installed; skipping snapshot artifact build"
fi

cat <<'EOF'

Smoke test passed.

Recommended manual checks before tagging:
  1. ./billy
  2. Inside Billy: /backend, /model, /mode teach, /hint, /license
  3. If Ollama is running: billy "explain this repository"

EOF
