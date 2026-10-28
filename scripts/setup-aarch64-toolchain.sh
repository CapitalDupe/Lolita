#!/bin/bash
# Sets up the linux/arm64 cross-build toolchain for sxops without root:
#   tools/aarch64-cc    - Debian cross gcc (aarch64-linux-gnu-*) + target libc headers
#   tools/prefix-arm64  - arm64 GTK/GLib/X11 dev+runtime libraries (for Fyne CGO)
#   tools/arm64-debs    - the downloaded .debs (kept for reproducibility)
set -u
T=/home/z/my-project/tools
mkdir -p "$T/aarch64-cc-debs" "$T/aarch64-cc" "$T/arm64-debs" "$T/prefix-arm64"

echo "== 1. host cross toolchain (gcc-aarch64-linux-gnu + deps) =="
cd "$T/aarch64-cc-debs"
LIST=$(apt-cache depends --recurse --no-recommends --no-suggests --no-conflicts \
        --no-breaks --no-replaces --no-enhances gcc-aarch64-linux-gnu 2>/dev/null |
        grep "^[a-zA-Z0-9]" | sort -u)
for p in $LIST; do
  ls "${p}"_*.deb >/dev/null 2>&1 && continue
  apt-get download "$p" >/dev/null 2>&1 || echo "   (skip $p)"
done
for d in *.deb; do dpkg -x "$d" "$T/aarch64-cc"; done
echo "   cross gcc extracted to $T/aarch64-cc"

echo "== 2. arm64 apt state (custom sources.list, no root needed) =="
A=$T/apt-arm64
mkdir -p "$A/etc" "$A/state/lists/partial" "$A/cache/archives/partial"
echo "deb [trusted=yes] http://deb.debian.org/debian trixie main" > "$A/etc/sources.list"
APTOPTS="-o Dir::Etc::SourceList=$A/etc/sources.list \
  -o Dir::Etc::SourceParts=/dev/null \
  -o Dir::State=$A/state \
  -o Dir::State::status=/var/lib/dpkg/status \
  -o Dir::Cache=$A/cache \
  -o APT::Architecture=arm64 \
  -o APT::Architectures::=arm64 \
  -o Acquire::Languages=none"
apt-get $APTOPTS update -qq 2>&1 | tail -2

echo "== 3. arm64 GTK/X11/GLib libraries =="
# Link-relevant set for Fyne (go-gl/glfw X11+Wayland, GTK3 stack) and the
# full runtime chain so the binary can also run under qemu-user for tests.
PKGS="
libgtk-3-0t64 libgtk-3-dev
libglib2.0-0t64 libglib2.0-dev
libgdk-pixbuf-2.0-0 libgdk-pixbuf-2.0-dev
libpango-1.0-0 libpangocairo-1.0-0 libpangoft2-1.0-0 libpangoxft-1.0-0 libpango1.0-dev
libharfbuzz0b libharfbuzz-dev libharfbuzz-subset0 libharfbuzz-gobject0 libgraphite2-3
libcairo2 libcairo2-dev libcairo-gobject2
libfontconfig1 libfontconfig-dev libfreetype6 libfreetype-dev libfribidi0 libfribidi-dev
libthai0 libdatrie1 libpixman-1-0 libpixman-1-dev
libpng16-16t64 libpng-dev libjpeg62-turbo libtiff6 libwebp7 libwebp-dev libjbig0 liblerc4 libdeflate0 libsharpyuv0
libatk1.0-0t64 libatk1.0-dev libatk-bridge2.0-0t64 libatk-bridge2.0-dev libatspi2.0-0t64 libatspi2.0-dev
libdbus-1-3 libdbus-1-dev libsystemd0
libx11-6 libx11-dev libxau6 libxau-dev libxdmcp6 libxcb1 libxcb1-dev
libxcb-render0 libxcb-render0-dev libxcb-shm0 libxcb-shm0-dev libxcb-sync1 libxcb-randr0 libxcb-xfixes0 libxcb-glx0 libxcb-xkb1
libxcomposite1 libxcomposite-dev libxcursor1 libxcursor-dev libxdamage1 libxdamage-dev
libxext6 libxext-dev libxfixes3 libxfixes-dev libxi6 libxi-dev libxinerama1 libxinerama-dev
libxrandr2 libxrandr-dev libxrender1 libxrender-dev libxtst6 libxtst-dev libxxf86vm1 libxxf86vm-dev
libxkbcommon0 libxkbcommon-dev libxkbcommon-x11-0 libxkbcommon-x11-dev libxshmfence1
libgl-dev libgl1 libglx0 libglx-dev libglx-mesa0
libegl-dev libegl1 libegl-mesa0 libopengl-dev libopengl0 libgles-dev libgles1 libgles2
libepoxy0 libepoxy-dev libglvnd0 libglvnd-dev libglvnd-core-dev libdrm2
libwayland-client0 libwayland-cursor0 libwayland-egl1 libwayland-dev
libexpat1 libexpat1-dev libzstd1 libzstd-dev liblzma5 liblzma-dev libbz2-1.0 zlib1g
libicu76 libxml2 libstdc++6 libgcc-s1 libc6
libuuid1 uuid-dev libblkid1 libblkid-dev libmount1 libmount-dev libselinux1 libsepol2 libpcre2-8-0 libffi8
libcups2t64 libavahi-client3 libavahi-common3 libavahi-common-data
libgnutls30t64 libgssapi-krb5-2 libk5crypto3 libkrb5-3 libkrb5support0 libkeyutils1 libcom-err2
libtasn1-6 libp11-kit0 libidn2-0 libunistring5 libhogweed6t64 libnettle8t64 libgmp10
libcolord2 libdconf1 libbrotli1 libbrotlidec1 libbrotlienc1 libbrotli-dev
libcloudproviders0 libcloudproviders-dev
"
cd "$T/arm64-debs"
for p in $PKGS; do
  name="${p%%:*}"
  ls ${name}_*arm64.deb >/dev/null 2>&1 && continue
  apt-get $APTOPTS download "${name}:arm64" >/dev/null 2>&1 || echo "   (miss $name)"
done
# Architecture-independent X11 protocol headers (X11/X.h and friends
# live in x11proto-dev on trixie, NOT in libx11-dev) - fetched via the
# default apt config because they are arch:all packages.
for p in x11proto-dev xtrans-dev; do
  ls ${p}_*_all.deb >/dev/null 2>&1 && continue
  (cd "$T/aarch64-cc-debs" && apt-get download "$p" >/dev/null 2>&1) \
    || apt-get download "$p" >/dev/null 2>&1 \
    || echo "   (miss $p)"
  mv -t . "$T/aarch64-cc-debs"/${p}_*_all.deb 2>/dev/null || true
done
for d in *.deb; do
  [ -e "$d" ] || continue
  dpkg -x "$d" "$T/prefix-arm64"
done
echo "   arm64 libs extracted to $T/prefix-arm64"

echo "== 4. convenience symlinks + linker-script fixups =="
# /lib -> usr/lib so the arm64 ELF interpreter resolves for qemu -L runs
if [ ! -e "$T/prefix-arm64/lib" ]; then
  ln -s usr/lib "$T/prefix-arm64/lib"
fi
# The native arm64 libc6-dev linker scripts (libc.so, libm.so) reference
# absolute /lib/aarch64-linux-gnu/... paths of the deb build host - they
# do not exist here, and ld does NOT re-root paths coming from scripts
# located outside --sysroot. Rewrite them to point into the extracted
# tree (via the /lib symlink above), or every arm64 link fails with
# "cannot find /lib/aarch64-linux-gnu/libm.so.6". Environment restores
# have been observed to revert both the symlink and these scripts.
P=$T/prefix-arm64/usr/lib/aarch64-linux-gnu
cat > "$P/libm.so" <<EOM
/* GNU ld script
*/
OUTPUT_FORMAT(elf64-littleaarch64)
GROUP ( $T/prefix-arm64/lib/aarch64-linux-gnu/libm.so.6  AS_NEEDED ( $T/prefix-arm64/lib/aarch64-linux-gnu/libmvec.so.1 ) )
EOM
cat > "$P/libc.so" <<EOM
/* GNU ld script
   Use the shared library, but some functions are only in
   the static library, so try that secondarily.  */
OUTPUT_FORMAT(elf64-littleaarch64)
GROUP ( $T/prefix-arm64/lib/aarch64-linux-gnu/libc.so.6 $T/prefix-arm64/lib/aarch64-linux-gnu/libc_nonshared.a  AS_NEEDED ( $T/prefix-arm64/lib/aarch64-linux-gnu/ld-linux-aarch64.so.1 ) )
EOM
echo "   linker scripts re-rooted to $T/prefix-arm64"

echo "== done =="
