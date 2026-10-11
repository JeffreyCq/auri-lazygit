#!/usr/bin/env bash
# AURI x Lazygit installer (macOS / Linux).
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/JeffreyCq/auri-lazygit/main/install.sh | bash
#
# This builds auri-tui from source instead of downloading a prebuilt binary
# — on macOS that means no Gatekeeper "unidentified developer" prompt, and
# it guarantees the binary matches your actual OS/arch (amd64 vs arm64)
# without needing a release for every combination.
set -euo pipefail

REPO_URL="https://github.com/JeffreyCq/auri-lazygit.git"
SRC_DIR="$HOME/.local/share/auri-lazygit"   # private cache clone, owned by this script
BIN_DIR="$HOME/.local/bin"
BIN_NAME="auri-tui"

info() { printf '  \033[32m==>\033[0m %s\n' "$1"; }
warn() { printf '  \033[33m!!\033[0m %s\n' "$1"; }
err()  { printf '  \033[31mxx\033[0m %s\n' "$1" >&2; }
have() { command -v "$1" >/dev/null 2>&1; }

echo ""
echo "  AURI x Lazygit installer"
echo "  ------------------------"
echo ""

# ---- prerequisites -----------------------------------------------------

if ! have go; then
  err "Go is required to build auri-tui, but wasn't found on your PATH."
  case "$(uname -s)" in
    Darwin) err "Install it with:  brew install go" ;;
    *)      err "Install it with your package manager, or see https://go.dev/dl/" ;;
  esac
  exit 1
fi

if ! have git; then
  err "git is required to fetch the source, but wasn't found on your PATH."
  exit 1
fi

# ---- fetch source -------------------------------------------------------

info "Fetching source into $SRC_DIR"
rm -rf "$SRC_DIR"
mkdir -p "$(dirname "$SRC_DIR")"
git clone --depth 1 "$REPO_URL" "$SRC_DIR" >/dev/null 2>&1 || {
  err "Failed to clone $REPO_URL"
  exit 1
}

# ---- build + install -----------------------------------------------------

info "Building $BIN_NAME (this downloads Go module deps on first run)..."
mkdir -p "$BIN_DIR"
(cd "$SRC_DIR" && go build -ldflags "-s -w" -o "$BIN_DIR/$BIN_NAME" ./cmd/auri-tui)
chmod +x "$BIN_DIR/$BIN_NAME"
info "Installed $BIN_DIR/$BIN_NAME"

if ! have npx; then
  warn "Node.js (npx) wasn't found. auri-tui needs it to launch the Endor Labs MCP server."
  case "$(uname -s)" in
    Darwin) warn "Install it with:  brew install node" ;;
    *)      warn "Install it with your package manager, or see https://nodejs.org/" ;;
  esac
fi

case ":$PATH:" in
  *":$BIN_DIR:"*) ;;
  *)
    warn "$BIN_DIR is not on your PATH yet."
    case "${SHELL:-}" in
      */zsh)  warn "Add this to ~/.zshrc:   export PATH=\"\$HOME/.local/bin:\$PATH\"" ;;
      */bash) warn "Add this to ~/.bashrc:  export PATH=\"\$HOME/.local/bin:\$PATH\"" ;;
      *)      warn "Add $BIN_DIR to your shell's PATH." ;;
    esac
    ;;
esac

# ---- wire up Lazygit -----------------------------------------------------

AURI_MARKER="AURI: open Endor Labs scan menu"
ENTRY_BLOCK=$(cat <<EOF
  - key: "<c-a>"
    context: "global"
    description: "$AURI_MARKER"
    command: "$BIN_DIR/$BIN_NAME"
    output: terminal
EOF
)

if have lazygit; then
  cfg_dir="$(lazygit --print-config-dir 2>/dev/null || true)"
  if [ -n "$cfg_dir" ]; then
    mkdir -p "$cfg_dir"
    cfg="$cfg_dir/config.yml"
    if [ ! -f "$cfg" ]; then
      { echo "customCommands:"; printf '%s\n' "$ENTRY_BLOCK"; } > "$cfg"
      info "Created $cfg with the AURI keybinding (Ctrl+A)."
    elif grep -qF "$AURI_MARKER" "$cfg" 2>/dev/null; then
      info "Lazygit is already wired up ($cfg already has the AURI entry)."
    elif ! grep -q '^customCommands:' "$cfg" 2>/dev/null; then
      { echo ""; echo "customCommands:"; printf '%s\n' "$ENTRY_BLOCK"; } >> "$cfg"
      info "Added the AURI keybinding to $cfg."
    else
      warn "$cfg already has a customCommands: section with other entries."
      warn "Add this under it by hand (same indentation as the existing list):"
      echo ""
      printf '%s\n' "$ENTRY_BLOCK"
      echo ""
    fi
  else
    warn "Found lazygit but couldn't read its config dir — add the entry from lazygit/config.yml by hand."
  fi
else
  warn "lazygit wasn't found. Install it (https://github.com/jesseduffield/lazygit), then add:"
  echo ""
  echo "customCommands:"
  printf '%s\n' "$ENTRY_BLOCK"
  echo ""
  warn "to its config.yml (lazygit --print-config-dir shows where)."
fi

echo ""
info "Done. Next steps:"
echo "    1. npx -y endorctl init     # free Endor Labs Developer Edition login"
echo "    2. Open lazygit in a git repo and press Ctrl+A"
echo ""
