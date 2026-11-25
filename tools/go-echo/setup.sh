#!/usr/bin/env sh
# Build a Go toolchain copy that runs on the Echo Dot 2nd gen kernel, which
# has no eventfd. Go >= 1.22 needs eventfd only for netpollBreak; the patch
# falls back to the non-blocking pipe Go 1.21 used. Normal kernels are unchanged.
# Usage: tools/go-echo/setup.sh [go-version]   (default go1.27.4)
# Then build with: GOTOOLCHAIN=local ~/.local/share/go-echo/<ver>/bin/go build ...
set -eu
ver="${1:-go1.27.4}"
here="$(cd "$(dirname "$0")" && pwd)"
dst="$HOME/.local/share/go-echo/$ver"
if [ ! -d "$dst" ]; then
  GOTOOLCHAIN="$ver" go version >/dev/null
  src="$(go env GOMODCACHE)/golang.org/toolchain@v0.0.1-$ver.$(go env GOHOSTOS)-$(go env GOHOSTARCH)"
  mkdir -p "$(dirname "$dst")"
  cp -r "$src" "$dst"
  chmod -R u+w "$dst"
  patch -d "$dst" -p1 < "$here/netpoll-pipe-fallback.patch"
fi
GOTOOLCHAIN=local "$dst/bin/go" version
echo "toolchain: $dst/bin/go"
