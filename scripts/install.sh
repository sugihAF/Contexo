#!/usr/bin/env sh
# install.sh — install the Contexo CLI (prebuilt binary) and put it on PATH.
#
# Run from anywhere:
#   curl -fsSL https://raw.githubusercontent.com/sugihAF/Contexo/main/scripts/install.sh | sh
#
# No Go toolchain required — this downloads a prebuilt, checksum-verified binary
# from GitHub Releases. Idempotent: safe to re-run. To update later, just run
# `ctx update`.

set -eu

REPO="sugihAF/Contexo"
INSTALL_DIR="${CONTEXO_INSTALL_DIR:-$HOME/.local/bin}"

bold() { printf '\033[1m%s\033[0m\n' "$1"; }
warn() { printf '\033[33m%s\033[0m\n' "$1" >&2; }
die()  { printf '\033[31m%s\033[0m\n' "$1" >&2; exit 1; }

bold "Contexo CLI installer"

# 1. Detect OS + arch -------------------------------------------------------
OS="$(uname -s)"
case "$OS" in
  Linux)  OS="linux" ;;
  Darwin) OS="darwin" ;;
  *) die "Unsupported OS: $OS (this installer covers Linux and macOS; on Windows use install.ps1)." ;;
esac

ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64)  ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *) die "Unsupported architecture: $ARCH (prebuilt binaries cover amd64 and arm64)." ;;
esac
printf "  Platform: %s/%s\n" "$OS" "$ARCH"

# 2. Download helper --------------------------------------------------------
download() {
  _url="$1"; _out="$2"
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$_url" -o "$_out"
  elif command -v wget >/dev/null 2>&1; then
    wget -qO "$_out" "$_url"
  else
    die "Need curl or wget to download the binary."
  fi
}

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT INT TERM

# 3. Resolve the latest release tag -----------------------------------------
download "https://api.github.com/repos/${REPO}/releases/latest" "$TMP/latest.json"
TAG="$(sed -n 's/.*"tag_name"[ ]*:[ ]*"\([^"]*\)".*/\1/p' "$TMP/latest.json" | head -n1)"
[ -n "$TAG" ] || die "Could not determine the latest release (no tag_name). Has a release been published yet?"
VERSION="${TAG#v}"
printf "  Latest release: %s\n" "$TAG"

# 4. Download archive + checksums -------------------------------------------
ASSET="ctx_${VERSION}_${OS}_${ARCH}.tar.gz"
BASE="https://github.com/${REPO}/releases/download/${TAG}"
bold "Downloading $ASSET"
download "${BASE}/${ASSET}" "$TMP/$ASSET"
download "${BASE}/checksums.txt" "$TMP/checksums.txt"

# 5. Verify checksum --------------------------------------------------------
EXPECTED="$(grep " ${ASSET}\$" "$TMP/checksums.txt" | awk '{print $1}' | head -n1)"
[ -n "$EXPECTED" ] || die "No checksum listed for $ASSET; refusing to install."
if command -v sha256sum >/dev/null 2>&1; then
  ACTUAL="$(sha256sum "$TMP/$ASSET" | awk '{print $1}')"
elif command -v shasum >/dev/null 2>&1; then
  ACTUAL="$(shasum -a 256 "$TMP/$ASSET" | awk '{print $1}')"
else
  warn "No sha256 tool found; skipping checksum verification."
  ACTUAL="$EXPECTED"
fi
[ "$ACTUAL" = "$EXPECTED" ] || die "Checksum mismatch for $ASSET (got $ACTUAL, want $EXPECTED)."
printf "  Verified checksum.\n"

# 6. Extract + install ------------------------------------------------------
tar -xzf "$TMP/$ASSET" -C "$TMP"
[ -f "$TMP/ctx" ] || die "Archive did not contain a ctx binary."
mkdir -p "$INSTALL_DIR"
mv "$TMP/ctx" "$INSTALL_DIR/ctx"
chmod +x "$INSTALL_DIR/ctx"
printf "  Installed to: %s\n" "$INSTALL_DIR/ctx"

# 6a. Warn if a different ctx already shadows this one ----------------------
EXISTING="$(command -v ctx 2>/dev/null || true)"
if [ -n "$EXISTING" ] && [ "$EXISTING" != "$INSTALL_DIR/ctx" ]; then
  warn "Note: another ctx is already on your PATH at $EXISTING."
  warn "Which one runs depends on PATH order — remove the old one if it's stale."
fi

# 7. Already on PATH? -------------------------------------------------------
case ":$PATH:" in
  *":$INSTALL_DIR:"*)
    bold "ctx is on your PATH. Try: ctx --help"
    exit 0
    ;;
esac

# 8. Detect shell + the line we'd add ---------------------------------------
SHELL_NAME="$(basename "${SHELL:-sh}")"
RC=""
LINE=""
case "$SHELL_NAME" in
  zsh)
    RC="$HOME/.zshrc"
    LINE="export PATH=\"\$PATH:$INSTALL_DIR\""
    ;;
  bash)
    if [ "$(uname -s)" = "Darwin" ]; then RC="$HOME/.bash_profile"; else RC="$HOME/.bashrc"; fi
    LINE="export PATH=\"\$PATH:$INSTALL_DIR\""
    ;;
  fish)
    RC="$HOME/.config/fish/config.fish"
    LINE="set -gx PATH \$PATH $INSTALL_DIR"
    ;;
  *)
    warn "Unrecognised shell: $SHELL_NAME"
    warn "Add this directory to your PATH by hand: $INSTALL_DIR"
    warn "Then open a new terminal and run: ctx --help"
    exit 0
    ;;
esac

# 9. Append idempotently ----------------------------------------------------
mkdir -p "$(dirname "$RC")"
touch "$RC"
if grep -F -q "$INSTALL_DIR" "$RC" 2>/dev/null; then
  bold "$RC already references $INSTALL_DIR — nothing to do."
else
  {
    printf '\n# Added by Contexo installer\n'
    printf '%s\n' "$LINE"
  } >> "$RC"
  printf "  Added '%s' to %s\n" "$LINE" "$RC"
fi

# 10. Done ------------------------------------------------------------------
bold "Done."
printf "Open a new terminal (or 'source %s'), then run:\n  ctx --help\n" "$RC"
