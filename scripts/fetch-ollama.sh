#!/usr/bin/env bash
# Downloads ollama binary for the target platform into internal/launcher/ollama_bin
# Usage: bash scripts/fetch-ollama.sh <goos> <goarch>
set -euo pipefail

GOOS="${1:-}"
GOARCH="${2:-}"

[[ -z "$GOOS" || -z "$GOARCH" ]] && { echo "Usage: $0 <goos> <goarch>"; exit 1; }

case "$GOOS/$GOARCH" in
  linux/amd64)   URL="https://github.com/ollama/ollama/releases/latest/download/ollama-linux-amd64" ;;
  linux/arm64)   URL="https://github.com/ollama/ollama/releases/latest/download/ollama-linux-arm64" ;;
  darwin/amd64)  URL="https://github.com/ollama/ollama/releases/latest/download/ollama-darwin" ;;
  darwin/arm64)  URL="https://github.com/ollama/ollama/releases/latest/download/ollama-darwin" ;;
  windows/amd64) URL="https://github.com/ollama/ollama/releases/latest/download/ollama-windows-amd64.exe" ;;
  *) echo "Unsupported: $GOOS/$GOARCH"; exit 1 ;;
esac

DEST="internal/launcher/ollama_bin"
echo "⬇  Downloading Ollama ($GOOS/$GOARCH)..."
curl -fsSL --progress-bar "$URL" -o "$DEST"
chmod +x "$DEST"
echo "✓  Saved to $DEST ($(du -sh "$DEST" | cut -f1))"
