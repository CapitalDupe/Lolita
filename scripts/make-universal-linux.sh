#!/bin/bash
# Generates the universal "sxops-linux" file: a small POSIX-shell launcher
# with the amd64 and arm64 binaries appended as a payload. On first run it
# extracts the matching architecture into ~/.cache/sxops and execs it.
set -eu
OUT_DIR=${1:-/home/z/my-project/download/sxops}
AMD64=/tmp/sxops-build/sxops-linux-amd64
ARM64=/tmp/sxops-build/sxops-linux-arm64

[ -f "$AMD64" ] || { echo "missing $AMD64 (run build-all.sh linux-amd64)"; exit 1; }
[ -f "$ARM64" ] || { echo "missing $ARM64 (run build-all.sh linux-arm64)"; exit 1; }

SZ_AMD64=$(stat -c %s "$AMD64")
SZ_ARM64=$(stat -c %s "$ARM64")
SHA_AMD64=$(sha256sum "$AMD64" | cut -d' ' -f1)
SHA_ARM64=$(sha256sum "$ARM64" | cut -d' ' -f1)

LAUNCHER=$(mktemp)
cat > "$LAUNCHER" <<EOF
#!/bin/sh
# sxops for Linux - one file for all common computers (x86-64 and ARM64).
# This is a small launcher; the real program for your computer's CPU is
# embedded below and unpacked automatically on first use.
set -eu

ARCH=\$(uname -m)
case "\$ARCH" in
  x86_64|amd64)  ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) echo "sxops: this computer's CPU (\$ARCH) is not supported." >&2; exit 1 ;;
esac

case "\$ARCH" in
  amd64) SZ=$SZ_AMD64; SHA=$SHA_AMD64 ;;
  arm64) SZ=$SZ_ARM64; SHA=$SHA_ARM64 ;;
esac

# where to unpack (kept between runs so later starts are instant)
CACHE=\${XDG_CACHE_HOME:-\$HOME/.cache}/sxops
BIN=\$CACHE/sxops-linux-\$ARCH

if ! { [ -x "\$BIN" ] && [ "\$(sha256sum "\$BIN" | cut -d' ' -f1)" = "\$SHA" ]; }; then
  mkdir -p "\$CACHE" 2>/dev/null || CACHE=\$(mktemp -d)
  BIN=\$CACHE/sxops-linux-\$ARCH
  MARK1='__SXOPS'
  MARK2='_PAYLOAD__'
  # byte offset of the payload (after the marker line)
  OFF=\$(grep -abo "\$MARK1\$MARK2" "\$0" | head -n 1 | cut -d: -f1)
  START=\$(( OFF + \${#MARK1} + \${#MARK2} + 1 ))
  if [ "\$ARCH" = amd64 ]; then SKIP=\$START; else SKIP=\$(( START + $SZ_AMD64 )); fi
  TMP=\$BIN.tmp.\$\$
  tail -c +\$(( SKIP + 1 )) "\$0" | head -c "\$SZ" > "\$TMP"
  if [ "\$(sha256sum "\$TMP" | cut -d' ' -f1)" != "\$SHA" ]; then
    rm -f "\$TMP"
    echo "sxops: the downloaded file is damaged - please download it again." >&2
    exit 1
  fi
  chmod 700 "\$TMP"
  mv -f "\$TMP" "\$BIN"
fi

exec "\$BIN" "\$@"

__SXOPS_PAYLOAD__
EOF

cat "$AMD64" >> "$LAUNCHER"
cat "$ARM64" >> "$LAUNCHER"

mkdir -p "$OUT_DIR"
DEST="$OUT_DIR/sxops-linux"
mv "$LAUNCHER" "$DEST"
chmod 755 "$DEST"
ls -la "$DEST"
echo "universal linux launcher written: $DEST"
