#!/bin/bash
# tools-setup-host.sh - recreates /home/z/my-project/tools (host amd64 part)
# for sxops builds after a session reset: Go SDK + prefix/ with the dev+runtime
# libraries Fyne's CGO build needs (GTK3/systray + GLFW X11/Wayland/GL stack).
# No root required: packages are downloaded and extracted, never installed.
# Package list mirrors scripts/setup-aarch64-toolchain.sh (proven set).
set -u
T=/home/z/my-project/tools
mkdir -p "$T/host-debs" "$T/prefix"

echo "== 1. Go SDK 1.27.3 =="
if [ ! -x "$T/go-sdk/bin/go" ]; then
  cd /tmp
  if [ ! -f go1.27.3.linux-amd64.tar.gz ]; then
    curl -fsSL -o go1.27.3.linux-amd64.tar.gz \
      https://dl.google.com/go/go1.27.3.linux-amd64.tar.gz
  fi
  rm -rf "$T/go-sdk"
  tar -C "$T" -xzf go1.27.3.linux-amd64.tar.gz
  mv "$T/go" "$T/go-sdk"
fi
"$T/go-sdk/bin/go" version || exit 1

echo "== 2. host dev+runtime debs (amd64) =="
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
libdbus-1-3 libdbus-1-dev libsystemd0 libsystemd-dev
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
libexpat1 libexpat1-dev libzstd1 libzstd-dev liblzma5 liblzma-dev libbz2-1.0 zlib1g zlib1g-dev
libicu76 libxml2 libstdc++6 libgcc-s1 libc6
libuuid1 uuid-dev libblkid1 libblkid-dev libmount1 libmount-dev libselinux1 libsepol2 libpcre2-8-0 libffi8
libcups2t64 libavahi-client3 libavahi-common3 libavahi-common-data
libgnutls30t64 libgssapi-krb5-2 libk5crypto3 libkrb5-3 libkrb5support0 libkeyutils1 libcom-err2
libtasn1-6 libp11-kit0 libidn2-0 libunistring5 libhogweed6t64 libnettle8t64 libgmp10
libcolord2 libdconf1 libbrotli1 libbrotlidec1 libbrotlienc1 libbrotli-dev
libcloudproviders0 libcloudproviders-dev
"
cd "$T/host-debs"
for p in $PKGS; do
  ls "${p}"_*.deb >/dev/null 2>&1 && continue
  apt-get download "$p" >/dev/null 2>&1 || echo "   (miss $p)"
done
echo "   extracting..."
for d in *.deb; do dpkg -x "$d" "$T/prefix"; done
echo "   host libs extracted to $T/prefix"

echo "== 3. sanity: pkg-config through the prefix =="
export PKG_CONFIG_PATH=$T/prefix/usr/lib/x86_64-linux-gnu/pkgconfig
for m in x11 gl egl wayland-client xkbcommon gtk+-3.0; do
  pkg-config --exists $m && echo "   $m: OK" || echo "   $m: MISSING"
done
echo "== done (host) =="
