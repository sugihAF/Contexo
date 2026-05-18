#!/usr/bin/env sh
# install.sh — install the Contexo CLI and put it on PATH.
#
# Run from anywhere:
#   curl -fsSL https://raw.githubusercontent.com/sugihAF/Contexo/main/scripts/install.sh | sh
#
# Requires: Go 1.25+. Refuses (without installing anything) if Go is missing.
# Idempotent: safe to re-run; PATH entries are added at most once.

set -eu

bold() { printf '\033[1m%s\033[0m\n' "$1"; }
warn() { printf '\033[33m%s\033[0m\n' "$1" >&2; }
die()  { printf '\033[31m%s\033[0m\n' "$1" >&2; exit 1; }

bold "Contexo CLI installer"

# 1. Go check ----------------------------------------------------------------
if ! command -v go >/dev/null 2>&1; then
  die "Go is not installed. Install Go 1.25+ from https://go.dev/dl, then re-run this script."
fi
printf "  Found Go: %s\n" "$(go version)"

# 2. Install -----------------------------------------------------------------
bold "Installing ctx (go install github.com/sugihAF/Contexo/cmd/ctx@latest)"
go install github.com/sugihAF/Contexo/cmd/ctx@latest

# 3. Resolve install directory ----------------------------------------------
INSTALL_DIR="$(go env GOBIN)"
if [ -z "$INSTALL_DIR" ]; then
  INSTALL_DIR="$(go env GOPATH)/bin"
fi
if [ ! -x "$INSTALL_DIR/ctx" ]; then
  die "ctx was not written to $INSTALL_DIR (unexpected — check 'go install' output above)."
fi
printf "  Installed to: %s\n" "$INSTALL_DIR/ctx"

# 4. Already on PATH? --------------------------------------------------------
case ":$PATH:" in
  *":$INSTALL_DIR:"*)
    bold "ctx is already on your PATH. Try: ctx --help"
    exit 0
    ;;
esac

# 5. Detect shell + the line we'd add ---------------------------------------
SHELL_NAME="$(basename "${SHELL:-sh}")"
RC=""
LINE=""
case "$SHELL_NAME" in
  zsh)
    RC="$HOME/.zshrc"
    LINE="export PATH=\"\$PATH:$INSTALL_DIR\""
    ;;
  bash)
    if [ "$(uname -s)" = "Darwin" ]; then
      RC="$HOME/.bash_profile"
    else
      RC="$HOME/.bashrc"
    fi
    LINE="export PATH=\"\$PATH:$INSTALL_DIR\""
    ;;
  fish)
    RC="$HOME/.config/fish/config.fish"
    LINE="set -gx PATH \$PATH $INSTALL_DIR"
    ;;
  *)
    warn "Unrecognised shell: $SHELL_NAME"
    warn "Add this line to your shell's startup file by hand:"
    warn "  export PATH=\"\$PATH:$INSTALL_DIR\""
    warn "Then open a new terminal and run: ctx --help"
    exit 0
    ;;
esac

# 6. Append idempotently -----------------------------------------------------
mkdir -p "$(dirname "$RC")"
touch "$RC"
if grep -F -q "$INSTALL_DIR" "$RC" 2>/dev/null; then
  bold "$RC already references $INSTALL_DIR — nothing to do."
else
  {
    printf '\n# Added by Contexo installer (%s)\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    printf '%s\n' "$LINE"
  } >> "$RC"
  printf "  Added '%s' to %s\n" "$LINE" "$RC"
fi

# 7. Done --------------------------------------------------------------------
bold "Done."
printf "Open a new terminal (or 'source %s'), then run:\n  ctx --help\n" "$RC"
