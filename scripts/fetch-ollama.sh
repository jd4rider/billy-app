#!/usr/bin/env bash
# Downloads and extracts the ollama binary for the target platform into internal/launcher/ollama_bin
# Usage: bash scripts/fetch-ollama.sh <goos> <goarch>
set -euo pipefail

GOOS="${1:-}"
GOARCH="${2:-}"

[[ -z "$GOOS" || -z "$GOARCH" ]] && { echo "Usage: $0 <goos> <goarch>"; exit 1; }

DEST="internal/launcher/ollama_bin"
TMPDIR="$(mktemp -d)"
trap 'rm -rf "$TMPDIR"' EXIT

case "$GOOS/$GOARCH" in
  linux/amd64)
    ARCHIVE="ollama-linux-amd64.tar.zst"
    EXTRACT="zst"
    ;;
  linux/arm64)
    ARCHIVE="ollama-linux-arm64.tar.zst"
    EXTRACT="zst"
    ;;
  darwin/amd64|darwin/arm64)
    ARCHIVE="ollama-darwin.tgz"
    EXTRACT="tgz"
    ;;
  windows/amd64)
    # Windows ships a zip; fat builds for Windows are not yet supported
    echo "Windows fat builds not supported"; exit 1 ;;
  *) echo "Unsupported: $GOOS/$GOARCH"; exit 1 ;;
esac

URL="https://github.com/ollama/ollama/releases/latest/download/$ARCHIVE"
ARCHIVE_PATH="$TMPDIR/$ARCHIVE"

echo "⬇  Downloading $ARCHIVE ($GOOS/$GOARCH)..."
curl -fsSL --progress-bar "$URL" -o "$ARCHIVE_PATH"

echo "📦  Extracting..."
if [[ "$EXTRACT" == "zst" ]]; then
  # Install zstd if not present (Ubuntu CI runners)
  if ! command -v zstd &>/dev/null; then
    apt-get install -y zstd >/dev/null 2>&1 || true
  fi
  tar --zstd -xf "$ARCHIVE_PATH" -C "$TMPDIR"
else
  tar -xzf "$ARCHIVE_PATH" -C "$TMPDIR"
fi

# The archive contains `bin/ollama` or just `ollama` at root
if [[ -f "$TMPDIR/bin/ollama" ]]; then
  cp "$TMPDIR/bin/ollama" "$DEST"
elif [[ -f "$TMPDIR/ollama" ]]; then
  cp "$TMPDIR/ollama" "$DEST"
else
  echo "❌  Could not find ollama binary in archive. Contents:"
  find "$TMPDIR" -type f
  exit 1
fi

chmod +x "$DEST"
echo "✓  Saved to $DEST ($(du -sh "$DEST" | cut -f1))"
