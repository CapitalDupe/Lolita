#!/bin/bash
# Builds sxops for Linux: amd64 + arm64 (one universal file per OS is
# assembled by make-universal-linux.sh).
# No display tags -> GLFW compiles BOTH X11 and Wayland backends (runtime
# selection), so the binary works on any Linux desktop - native or WSL2.
# Windows users run this same Linux build inside WSL2 (installed by
# scripts/windows/install.cmd).
set -eu
T=/home/z/my-project/tools
SRC=/home/z/my-project/work/sxops-src
OUT=/tmp/sxops-build
mkdir -p "$OUT"

export PATH=$T/go-sdk/bin:$PATH
export GOPATH=$T/gopath GOMODCACHE=$T/gomodcache
cd "$SRC"

build_linux_amd64() {
  export PKG_CONFIG_PATH=$T/prefix/usr/lib/x86_64-linux-gnu/pkgconfig
  export CGO_CFLAGS="-I$T/prefix/usr/include"
  export CGO_LDFLAGS="-L$T/prefix/usr/lib/x86_64-linux-gnu"
  unset CC
  GOOS=linux GOARCH=amd64 CGO_ENABLED=1 \
    go build -trimpath -ldflags="-s -w" -o "$OUT/sxops-linux-amd64" .
  ls -la "$OUT/sxops-linux-amd64"
}

build_linux_arm64() {
  export PKG_CONFIG_PATH=$T/prefix-arm64/usr/lib/aarch64-linux-gnu/pkgconfig
  export CC=$T/aarch64-cc/bin/aarch64-cc
  export CGO_CFLAGS="-I$T/prefix-arm64/usr/include"
  export CGO_LDFLAGS="-L$T/prefix-arm64/usr/lib/aarch64-linux-gnu -Wl,-rpath-link,$T/prefix-arm64/usr/lib/aarch64-linux-gnu"
  GOOS=linux GOARCH=arm64 CGO_ENABLED=1 \
    go build -trimpath -ldflags="-s -w" -o "$OUT/sxops-linux-arm64" .
  ls -la "$OUT/sxops-linux-arm64"
}

case "${1:-all}" in
  linux-amd64) build_linux_amd64 ;;
  linux-arm64) build_linux_arm64 ;;
  all) build_linux_amd64; build_linux_arm64 ;;
esac
