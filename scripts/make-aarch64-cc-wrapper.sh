#!/bin/bash
# Recreates tools/aarch64-cc/bin/aarch64-cc - the CC wrapper build-all.sh
# uses for the linux/arm64 cross-build. The wrapper itself lives in
# tools/ (wiped between sessions), which is why its creation is persisted
# here in the source tree.
#
# Why it is needed: the extracted Debian cross-gcc drives binutils
# (as/ld) that link against libopcodes/libbfd shipped *next to* them in
# usr/lib/x86_64-linux-gnu - outside the standard loader path. The
# wrapper puts that directory on LD_LIBRARY_PATH. It also passes
# --sysroot: the libc6-dev-arm64-cross linker scripts (libc.so) use
# absolute /usr/aarch64-linux-gnu/... paths, which only resolve under
# the extracted tree's sysroot.
#
# NOTE: libc6-dev-arm64-cross + linux-libc-dev-arm64-cross must be
# extracted into tools/aarch64-cc first (they carry Scrt1.o/crti.o and
# the kernel headers) - this script downloads them if missing.
set -u
T=/home/z/my-project/tools
GCC=$T/aarch64-cc/usr/bin/aarch64-linux-gnu-gcc
[ -x "$GCC" ] || { echo "missing $GCC - run setup-aarch64-toolchain.sh first"; exit 1; }
if ! ls $T/aarch64-cc/usr/aarch64-linux-gnu/lib/Scrt1.o >/dev/null 2>&1; then
  echo "crt objects missing - fetching libc6-dev-arm64-cross"
  mkdir -p "$T/aarch64-cc-debs" && cd "$T/aarch64-cc-debs"
  apt-get download libc6-dev-arm64-cross linux-libc-dev-arm64-cross >/dev/null 2>&1 \
    || { echo "download failed - no network?"; exit 1; }
  for d in libc6-dev-arm64-cross_*_all.deb linux-libc-dev-arm64-cross_*_all.deb; do
    [ -f "$d" ] && dpkg -x "$d" "$T/aarch64-cc"
  done
fi
mkdir -p "$T/aarch64-cc/bin"
cat > "$T/aarch64-cc/bin/aarch64-cc" <<'EOF'
#!/bin/bash
# aarch64-cc - wrapper for the extracted Debian cross toolchain (see
# scripts/make-aarch64-cc-wrapper.sh in the sxops source).
T=/home/z/my-project/tools
export LD_LIBRARY_PATH="$T/aarch64-cc/usr/lib/x86_64-linux-gnu${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}"
exec "$T/aarch64-cc/usr/bin/aarch64-linux-gnu-gcc" --sysroot="$T/aarch64-cc" "$@"
EOF
chmod +x "$T/aarch64-cc/bin/aarch64-cc"
echo "wrapper installed: $T/aarch64-cc/bin/aarch64-cc"
echo 'int main(void){return 0;}' > /tmp/aarch64-cc-test.c
"$T/aarch64-cc/bin/aarch64-cc" -o /tmp/aarch64-cc-test /tmp/aarch64-cc-test.c \
  && file /tmp/aarch64-cc-test | grep -q aarch64 \
  && echo "cross-compile smoke test: OK" \
  || { echo "cross-compile smoke test FAILED"; exit 1; }
