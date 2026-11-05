#!/bin/sh
set -eu

VERSION="${1:-0.1.0}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DIST="$ROOT/dist"
rm -rf "$DIST"
mkdir -p "$DIST"

build() {
  os="$1"; arch="$2"; ext="$3"
  out="$DIST/android-kit${ext}"
  echo "building $os/$arch"
  GOOS="$os" GOARCH="$arch" CGO_ENABLED=0 go build \
    -trimpath -ldflags "-s -w -X main.appVersion=$VERSION" \
    -o "$out" "$ROOT"
  if [ "$os" = "windows" ]; then
    (cd "$DIST" && zip -q "android-kit_${os}_${arch}.zip" "android-kit.exe")
    rm -f "$out"
  else
    (cd "$DIST" && tar -czf "android-kit_${os}_${arch}.tar.gz" "android-kit")
    rm -f "$out"
  fi
}

build darwin arm64 ""
build darwin amd64 ""
build linux arm64 ""
build linux amd64 ""
build windows amd64 ".exe"

cp "$ROOT/install.sh" "$DIST/install.sh"
cp "$ROOT/install.ps1" "$DIST/install.ps1"

(cd "$DIST" && shasum -a 256 android-kit_* install.sh install.ps1 > checksums.txt)
echo "artifacts in $DIST"
ls -lh "$DIST"
