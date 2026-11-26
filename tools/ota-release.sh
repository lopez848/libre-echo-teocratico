#!/bin/sh
# Build, sign and (with --publish) publish an OTA release of the bridge.
#   tools/ota-release.sh v1.0.0            build + sign into dist/v1.0.0, publish nothing
#   tools/ota-release.sh v1.0.0 --publish  also create the GitHub release (owner's yes first)
#   tools/ota-release.sh --build-agent     build dist/ota (the device agent) with the public key
# The signing key stays at $OTA_KEY (default ~/.config/libre-echo-ota/signing.key, mode 600).
set -eu
REPO=lopez848/libre-echo-teocratico
KEY=${OTA_KEY:-$HOME/.config/libre-echo-ota/signing.key}
GO=$HOME/.local/share/go-echo/go1.27.4/bin/go
ROOT=$(cd "$(dirname "$0")/.." && pwd)
sign() { (cd "$ROOT/ota" && GOTOOLCHAIN=local "$GO" run ./cmd/ota-sign "$@"); }

[ -f "$KEY" ] || { echo "no signing key at $KEY (create it with: ota-sign keygen $KEY)" >&2; exit 1; }
PUB=$(sign pubkey "$KEY")

if [ "${1:-}" = "--build-agent" ]; then
	mkdir -p "$ROOT/dist"
	(cd "$ROOT/ota" && GOTOOLCHAIN=local CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 "$GO" build -buildvcs=false -trimpath \
		-ldflags "-X main.publicKey=$PUB" -o "$ROOT/dist/ota" .)
	echo "built $ROOT/dist/ota"
	exit 0
fi

VER=${1:?usage: ota-release.sh vX.Y.Z [--publish] | --build-agent}
echo "$VER" | grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$' || { echo "version must look like v1.2.3" >&2; exit 1; }
[ -z "$(git -C "$ROOT" status --porcelain)" ] || { echo "working tree not clean" >&2; exit 1; }
OUT=$ROOT/dist/$VER
mkdir -p "$OUT"
(cd "$ROOT/puente" && GOTOOLCHAIN=local CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 "$GO" build -buildvcs=false -trimpath -o "$OUT/puente" .)
# ota-sign writes manifest.json in its working directory (ota/)
sign manifest "$VER" "https://github.com/$REPO/releases/download/$VER/" "puente=$OUT/puente"
mv "$ROOT/ota/manifest.json" "$OUT/manifest.json"
sign sign "$KEY" "$OUT/manifest.json"
echo "signed release in $OUT:"; ls -l "$OUT"
if [ "${2:-}" = "--publish" ]; then
	gh release create "$VER" -R "$REPO" --target "$(git -C "$ROOT" rev-parse HEAD)" --title "$VER" \
		--notes "Signed OTA release for Libre Echo speakers." "$OUT/puente" "$OUT/manifest.json" "$OUT/manifest.json.sig"
fi
