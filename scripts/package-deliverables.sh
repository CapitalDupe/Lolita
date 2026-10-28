#!/bin/bash
# Assembles the download deliverables from the fresh builds in /tmp/sxops-build:
#   sxops-linux              - universal Linux program (amd64 + arm64 payload)
#   README-WSL.txt           - WSL setup instructions for Windows users (CRLF)
#   sxops-src.zip            - full source incl. scripts/ and README.md
#   SHA256SUMS.txt
# (Windows users run the same sxops-linux inside WSL2 — see README-WSL.txt / README.md
#  for the short manual setup instructions; no Windows installer is shipped.)
set -eu
SRC=/home/z/my-project/work/sxops-src
OUT_DIR=/home/z/my-project/download/sxops
BUILD=/tmp/sxops-build

[ -f "$BUILD/sxops-linux-amd64" ] || { echo "missing $BUILD/sxops-linux-amd64 (run build-all.sh linux-amd64)"; exit 1; }
[ -f "$BUILD/sxops-linux-arm64" ] || { echo "missing $BUILD/sxops-linux-arm64 (run build-all.sh linux-arm64)"; exit 1; }

mkdir -p "$OUT_DIR"

# 1. universal linux launcher + README + WSL guide
bash "$SRC/scripts/make-universal-linux.sh" "$OUT_DIR"
cp "$SRC/README.md" "$OUT_DIR/README.md"
# README-WSL.txt targets Windows users opening it in Notepad: convert LF -> CRLF
cp "$SRC/README-WSL.txt" "$OUT_DIR/README-WSL.txt"
unix2dos -q "$OUT_DIR/README-WSL.txt" 2>/dev/null || sed -i 's/\r\?$/\r/' "$OUT_DIR/README-WSL.txt"

# 2. source zip (module + scripts/ + README.md)
rm -f "$OUT_DIR/sxops-src.zip"
(cd "$SRC" && zip -9 -r "$OUT_DIR/sxops-src.zip" . -x '.git/*' -x '*.log')
ls -la "$OUT_DIR/sxops-src.zip"

# 3. checksums
cd "$OUT_DIR"
sha256sum sxops-linux README-WSL.txt sxops-src.zip README.md > SHA256SUMS.txt
cat SHA256SUMS.txt

echo "packaging complete: $OUT_DIR"
