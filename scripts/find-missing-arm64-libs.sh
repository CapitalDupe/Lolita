#!/bin/bash
# Finds every DT_NEEDED shared library of the arm64 prefix closure that is
# missing from the prefix (so we can download exactly those packages).
set -u
T=/home/z/my-project/tools
L=$T/prefix-arm64/usr/lib/aarch64-linux-gnu
RE=$T/aarch64-cc/usr/bin/aarch64-linux-gnu-readelf
export LD_LIBRARY_PATH=$T/aarch64-cc/usr/lib/x86_64-linux-gnu

# collect DT_NEEDED of every shared object in the prefix
needed=$(for f in $L/*.so*; do
  [ -f "$f" ] || continue
  $RE -d "$f" 2>/dev/null | grep NEEDED | sed 's/.*\[\(.*\)\].*/\1/'
done | sort -u)

missing=""
for n in $needed; do
  # provided inside the prefix itself?
  if ls $L/$n >/dev/null 2>&1; then continue; fi
  # provided by the cross sysroot (libc/libgcc)?
  if ls $T/aarch64-cc/usr/aarch64-linux-gnu/lib/$n >/dev/null 2>&1; then continue; fi
  # linker/crt support libs
  case "$n" in
    ld-linux-aarch64.so.1|libgcc_s.so.1|libanl.so.1|libdl.so.2|libm.so.6|libpthread.so.0|librt.so.1|libresolv.so.2|libutil.so.1) continue;;
  esac
  missing="$missing $n"
done
echo "MISSING:$missing" | tr ' ' '\n'
