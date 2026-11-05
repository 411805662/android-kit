#!/bin/sh
# android-kit installer
#
#   curl -fsSL https://cdn.jsdelivr.net/gh/411805662/android-kit@main/install.sh | sh
#
# Environment:
#   ANDROID_KIT_VERSION   release tag, default: latest
#   ANDROID_KIT_PREFIX    install prefix, default: $HOME/.local
#   ANDROID_KIT_ALIASES   classic | none, default: none
#   ANDROID_KIT_NO_SETUP  set to 1 to skip shell integration

set -eu

REPO="411805662/android-kit"
VERSION="${ANDROID_KIT_VERSION:-latest}"
PREFIX="${ANDROID_KIT_PREFIX:-$HOME/.local}"
ALIASES="${ANDROID_KIT_ALIASES:-none}"
BIN_DIR="$PREFIX/bin"

say() { printf '%s\n' "$*"; }
err() { printf 'error: %s\n' "$*" >&2; exit 1; }

need() {
  command -v "$1" >/dev/null 2>&1 || err "missing required command: $1"
}

need uname
need tar
need curl

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$OS" in
  darwin) OS="darwin" ;;
  linux) OS="linux" ;;
  *) err "unsupported OS: $OS (macOS and Linux are supported)" ;;
esac

ARCH="$(uname -m)"
case "$ARCH" in
  arm64|aarch64) ARCH="arm64" ;;
  x86_64|amd64) ARCH="amd64" ;;
  *) err "unsupported architecture: $ARCH" ;;
esac

ASSET="android-kit_${OS}_${ARCH}.tar.gz"
if [ "$VERSION" = "latest" ]; then
  # Resolve the tag through the API. The /releases/latest/download redirect is
  # cached by GitHub's CDN and can briefly point at the previous release.
  RESOLVED="$(curl -fsSL --connect-timeout 10 "https://api.github.com/repos/$REPO/releases/latest" 2>/dev/null \
    | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1)"
  if [ -n "$RESOLVED" ]; then
    VERSION="$RESOLVED"
  else
    VERSION="latest"
  fi
fi
if [ "$VERSION" = "latest" ]; then
  BASE="https://github.com/$REPO/releases/latest/download"
else
  BASE="https://github.com/$REPO/releases/download/$VERSION"
fi

TMP="$(mktemp -d "${TMPDIR:-/tmp}/android-kit.XXXXXX")"
cleanup() { rm -rf "$TMP"; }
trap cleanup EXIT INT TERM

say "android-kit installer"
say "  os/arch: $OS/$ARCH"
say "  version: $VERSION"
say "  prefix:  $PREFIX"
say ""

say "→ downloading $ASSET"
curl -fL --retry 3 --connect-timeout 15 -o "$TMP/$ASSET" "$BASE/$ASSET" \
  || err "download failed. Check https://github.com/$REPO/releases"

if curl -fsSL --retry 2 -o "$TMP/checksums.txt" "$BASE/checksums.txt" 2>/dev/null; then
  EXPECTED="$(grep " $ASSET\$" "$TMP/checksums.txt" | awk '{print $1}' | head -1)"
  if [ -n "$EXPECTED" ]; then
    if command -v shasum >/dev/null 2>&1; then
      ACTUAL="$(shasum -a 256 "$TMP/$ASSET" | awk '{print $1}')"
    elif command -v sha256sum >/dev/null 2>&1; then
      ACTUAL="$(sha256sum "$TMP/$ASSET" | awk '{print $1}')"
    else
      ACTUAL=""
    fi
    if [ -n "$ACTUAL" ] && [ "$ACTUAL" != "$EXPECTED" ]; then
      err "checksum mismatch for $ASSET"
    fi
    [ -n "$ACTUAL" ] && say "✓ checksum verified"
  fi
fi

say "→ extracting"
tar -xzf "$TMP/$ASSET" -C "$TMP"
[ -f "$TMP/android-kit" ] || err "archive does not contain android-kit"

mkdir -p "$BIN_DIR"
install -m 0755 "$TMP/android-kit" "$BIN_DIR/android-kit"
ln -sf android-kit "$BIN_DIR/ak"

say "✓ installed $BIN_DIR/android-kit"
say "✓ linked    $BIN_DIR/ak"

case ":$PATH:" in
  *":$BIN_DIR:"*) ;;
  *)
    say ""
    say "注意: $BIN_DIR 不在 PATH 中。把下面一行加入你的 shell 配置："
    say "  export PATH=\"$BIN_DIR:\$PATH\""
    ;;
esac

if [ "${ANDROID_KIT_NO_SETUP:-0}" != "1" ]; then
  say ""
  say "→ configuring shell integration"
  "$BIN_DIR/android-kit" setup --yes --shell auto --aliases "$ALIASES" || {
    say "setup failed; run manually: $BIN_DIR/android-kit setup"
  }
fi

say ""
say "完成。下一步："
say "  ak project add /path/to/your/android/project --default"
say "  ak doctor"
say "  ak web"
say ""
say "当前终端立即生效："
say "  eval \"\$($BIN_DIR/android-kit shell init --quiet)\""
