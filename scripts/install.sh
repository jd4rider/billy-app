#!/usr/bin/env bash
set -euo pipefail

BILLY_INSTALL_DIR="${BILLY_INSTALL_DIR:-$HOME/.billy/bin}"
REPO="jd4rider/billy-app"
FULL=false

# Parse flags
for arg in "$@"; do
  case "$arg" in
    --full) FULL=true ;;
    --help|-h)
      echo "Usage: install.sh [--full]"
      echo "  --full   Install Billy Full (Ollama bundled, auto-starts)"
      exit 0 ;;
  esac
done

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'
BLUE='\033[0;34m'; BOLD='\033[1m'; NC='\033[0m'
info()    { echo -e "${BLUE}[billy]${NC} $*"; }
success() { echo -e "${GREEN}[billy]${NC} $*"; }
warn()    { echo -e "${YELLOW}[billy]${NC} $*"; }
error()   { echo -e "${RED}[billy]${NC} $*" >&2; exit 1; }

detect_platform() {
  local os arch
  os=$(uname -s | tr '[:upper:]' '[:lower:]')
  arch=$(uname -m)
  case "$arch" in
    x86_64)        arch="amd64" ;;
    aarch64|arm64) arch="arm64" ;;
    *) error "Unsupported architecture: $arch" ;;
  esac
  case "$os" in
    linux|darwin) ;;
    *) error "Unsupported OS: $os — download manually from https://github.com/$REPO/releases" ;;
  esac
  echo "${os}_${arch}"
}

get_latest_version() {
  curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" \
    | grep '"tag_name"' \
    | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/'
}

main() {
  echo ""
  echo -e "${BOLD}  Billy.sh installer${NC}"
  if $FULL; then
    echo -e "  Installing: ${GREEN}Billy Full${NC} (Ollama bundled)"
  else
    echo -e "  Installing: ${BLUE}Billy Slim${NC} (requires Ollama)"
  fi
  echo -e "  ${YELLOW}⚠  Pre-Alpha — expect rough edges${NC}"
  echo ""

  local platform version binary_name url tmpdir

  platform=$(detect_platform)
  info "Platform: $platform"

  version=$(get_latest_version)
  [[ -z "$version" ]] && error "Could not determine latest version"
  info "Version:  $version"

  if $FULL; then
    binary_name="billy-full"
  else
    binary_name="billy"
  fi

  url="https://github.com/$REPO/releases/download/$version/${binary_name}_${platform}.tar.gz"
  info "Downloading from: $url"

  tmpdir=$(mktemp -d)
  trap 'rm -rf "$tmpdir"' EXIT

  curl -fsSL --progress-bar "$url" -o "$tmpdir/archive.tar.gz" \
    || error "Download failed. Check https://github.com/$REPO/releases for available builds."

  tar -xzf "$tmpdir/archive.tar.gz" -C "$tmpdir"

  mkdir -p "$BILLY_INSTALL_DIR"

  # The binary in the archive may be named billy-full or billy depending on variant
  local extracted_bin
  extracted_bin=$(find "$tmpdir" -maxdepth 1 -type f -name "${binary_name}*" ! -name "*.tar.gz" | head -1)
  [[ -z "$extracted_bin" ]] && error "Binary not found in archive"

  mv "$extracted_bin" "$BILLY_INSTALL_DIR/$binary_name"
  chmod +x "$BILLY_INSTALL_DIR/$binary_name"
  success "Installed $binary_name → $BILLY_INSTALL_DIR/$binary_name"

  # Add to PATH if needed
  if [[ ":$PATH:" != *":$BILLY_INSTALL_DIR:"* ]]; then
    local shell_config
    case "${SHELL:-bash}" in
      */zsh)  shell_config="$HOME/.zshrc" ;;
      */fish) shell_config="$HOME/.config/fish/config.fish" ;;
      *)      shell_config="$HOME/.bashrc" ;;
    esac
    { echo ""; echo "# Billy"; echo "export PATH=\"\$HOME/.billy/bin:\$PATH\""; } >> "$shell_config"
    warn "Added ~/.billy/bin to PATH in $shell_config"
    warn "Run: source $shell_config  (or open a new terminal)"
  fi

  # If slim, check for ollama
  if ! $FULL; then
    echo ""
    if command -v ollama &>/dev/null; then
      success "Ollama found: $(command -v ollama)"
    else
      warn "Ollama not found."
      warn "Install it at: https://ollama.com"
      warn "Or reinstall with --full to get Ollama bundled:"
      echo ""
      echo -e "  ${BLUE}curl -fsSL https://raw.githubusercontent.com/$REPO/main/scripts/install.sh | bash -s -- --full${NC}"
    fi
  fi

  echo ""
  echo -e "${BOLD}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
  echo -e "${GREEN}${BOLD}  Ready! 🎉${NC}"
  echo -e "${BOLD}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
  echo ""
  echo -e "  ${BOLD}Start Billy:${NC}              ${BLUE}$binary_name${NC}"
  echo -e "  ${BOLD}Pull a model:${NC}             ${BLUE}/pull llama3.2${NC}"
  echo -e "  ${BOLD}Help:${NC}                     ${BLUE}/help${NC}"
  echo ""
  echo -e "  Source: https://github.com/$REPO"
  echo ""
}

main "$@"
