#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
OUT_DIR="${OUT_DIR:-$ROOT_DIR/dist}"
VERSION="${VERSION:-0.2.0}"
VERSION_VALUE="${VERSION#v}"
TARGETS="${TARGETS:-}"
mkdir -p "$OUT_DIR"

build_tarball() {
  local goos="$1"
  local goarch="$2"
  local ext=""
  local binary_name="billy"
  local workdir archive_name

  if [[ "$goos" == "windows" ]]; then
    ext=".exe"
    binary_name="billy.exe"
  fi

  workdir="$(mktemp -d)"
  trap 'rm -rf "$workdir"' RETURN

  GOOS="$goos" GOARCH="$goarch" CGO_ENABLED=0 \
    go build -trimpath \
      -ldflags="-s -w -X main.version=$VERSION_VALUE -X main.commit=release -X main.date=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
      -o "$workdir/$binary_name" ./cmd/billy

  if [[ "$goos" == "windows" ]]; then
    archive_name="billy_${goos}_${goarch}.zip"
    (
      cd "$workdir"
      zip -q "$OUT_DIR/$archive_name" "$binary_name"
    )
  else
    archive_name="billy_${goos}_${goarch}.tar.gz"
    tar -czf "$OUT_DIR/$archive_name" -C "$workdir" "$binary_name"
  fi
}

cd "$ROOT_DIR"
go test ./...

if [[ -z "$TARGETS" ]]; then
  case "$(uname -s)" in
    Darwin) TARGETS="darwin-amd64 darwin-arm64 windows-amd64" ;;
    Linux) TARGETS="linux-amd64 linux-arm64 windows-amd64" ;;
    *) TARGETS="darwin-arm64 windows-amd64" ;;
  esac
fi

for target in $TARGETS; do
  build_tarball "${target%-*}" "${target##*-}"
done

if command -v shasum >/dev/null 2>&1; then
  (
    cd "$OUT_DIR"
    shasum -a 256 billy_* > checksums.txt
  )
elif command -v sha256sum >/dev/null 2>&1; then
  (
    cd "$OUT_DIR"
    sha256sum billy_* > checksums.txt
  )
fi

printf '[release] Wrote CLI assets to %s\n' "$OUT_DIR"
