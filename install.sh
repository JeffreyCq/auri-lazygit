#!/usr/bin/env bash
# AURI installer (macOS / Linux).
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/JeffreyCq/auri-tui/main/install.sh | bash
#
# This builds auri-tui from source instead of downloading a prebuilt binary
# — on macOS that means no Gatekeeper "unidentified developer" prompt, and
# it guarantees the binary matches your actual OS/arch (amd64 vs arm64)
# without needing a release for every combination.
set -euo pipefail

REPO_URL="https://github.com/JeffreyCq/auri-tui.git"
SRC_DIR="$HOME/.local/share/auri-tui"   # private cache clone, owned by this script
BIN_DIR="$HOME/.local/bin"
BIN_NAME="auri-tui"

info() { printf '  \033[32m==>\033[0m %s\n' "$1"; }
warn() { printf '  \033[33m!!\033[0m %s\n' "$1"; }
err()  { printf '  \033[31mxx\033[0m %s\n' "$1" >&2; }
have() { command -v "$1" >/dev/null 2>&1; }

echo ""
echo "  AURI installer"
echo "  --------------"
echo ""

# ---- requirements check --------------------------------------------------
# Check everything up front and report the full picture in one pass, instead
# of failing on the first missing tool, having the user fix it, rerun, and
# hit the next one.

info "Checking requirements..."

MIN_GO_MAJOR=1
MIN_GO_MINOR=24
MIN_NODE_MAJOR=18
hard_failures=()
node_warning=""

check() {
  # check <ok:0|1> <label> <detail>
  if [ "$1" -eq 0 ]; then
    printf '      \033[32m[ok]\033[0m  %-10s %s\n' "$2" "$3"
  else
    printf '      \033[31m[--]\033[0m  %-10s %s\n' "$2" "$3"
  fi
}

ver_ge() { # ver_ge a.b a.b  -> 0 if first >= second
  [ "$(printf '%s\n%s\n' "$1" "$2" | sort -V | head -n1)" = "$2" ]
}

if have git; then
  check 0 git "$(git --version 2>/dev/null)"
else
  check 1 git "not found on PATH"
  hard_failures+=("git is required to fetch the source, but wasn't found on your PATH.")
fi

if have go; then
  go_raw="$(go version 2>/dev/null)"  # "go version go1.27.0 darwin/arm64"
  # \{0,1\} instead of the GNU-only \? — BSD sed (macOS's /usr/bin/sed) treats
  # \? as a literal, so this silently matched nothing on every Mac and made
  # the check reject every Go install, including valid ones.
  go_num="$(printf '%s' "$go_raw" | sed -n 's/.*go\([0-9]*\.[0-9]*\(\.[0-9]*\)\{0,1\}\).*/\1/p')"
  if [ -n "$go_num" ] && ver_ge "$go_num" "${MIN_GO_MAJOR}.${MIN_GO_MINOR}"; then
    check 0 Go "$go_raw"
  else
    check 1 Go "$go_raw (need >= ${MIN_GO_MAJOR}.${MIN_GO_MINOR})"
    hard_failures+=("Go ${MIN_GO_MAJOR}.${MIN_GO_MINOR}+ is required to build auri-tui (found: $go_raw).")
  fi
else
  check 1 Go "not found on PATH"
  hard_failures+=("Go ${MIN_GO_MAJOR}.${MIN_GO_MINOR}+ is required to build auri-tui, but wasn't found on your PATH.")
fi

if have npx; then
  node_raw="$(node --version 2>/dev/null)"  # "v24.14.1"
  node_major="$(printf '%s' "$node_raw" | sed -n 's/^v\([0-9]*\).*/\1/p')"
  if [ -n "$node_major" ] && [ "$node_major" -ge "$MIN_NODE_MAJOR" ]; then
    check 0 Node.js "$node_raw"
  else
    check 1 Node.js "$node_raw (need >= v${MIN_NODE_MAJOR}, for npx)"
    node_warning="Node.js v${MIN_NODE_MAJOR}+ is recommended (found: $node_raw) — auri-tui launches the Endor Labs MCP server via npx."
  fi
else
  check 1 Node.js "not found on PATH (needed to run auri-tui, not to build it)"
  node_warning="Node.js (npx) wasn't found. auri-tui needs it to launch the Endor Labs MCP server."
fi

echo ""

if [ "${#hard_failures[@]}" -gt 0 ]; then
  err "Missing requirements — install these first, then rerun this script:"
  echo ""
  for f in "${hard_failures[@]}"; do
    err "$f"
  done
  case "$(uname -s)" in
    Darwin) err "macOS:  brew install go git" ;;
    *)      err "Linux:  use your package manager, or see https://go.dev/dl/ and https://git-scm.com/" ;;
  esac
  echo ""
  exit 1
fi

if [ -n "$node_warning" ]; then
  warn "$node_warning"
  case "$(uname -s)" in
    Darwin) warn "Install it with:  brew install node" ;;
    *)      warn "Install it with your package manager, or see https://nodejs.org/" ;;
  esac
  echo ""
fi

info "All required tools are present. Continuing..."
echo ""

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

echo ""
info "Done. Next steps:"
echo "    1. npx -y endorctl init     # free Endor Labs Developer Edition login"
echo "    2. Run auri-tui from inside any git repository"
echo ""
