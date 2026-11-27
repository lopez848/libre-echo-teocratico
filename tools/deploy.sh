#!/bin/sh
# Build one service for the Echo and install it over ADB, announcing the
# restart to vigia so the planned restart is recorded but not alerted.
#   tools/deploy.sh puente|vigia|ota
set -eu
SVC=${1:?usage: deploy.sh puente|vigia|ota}
SERIAL=${ECHO_SERIAL:-G090LF1173650V3V}
GO=$HOME/.local/share/go-echo/go1.27.4/bin/go
ROOT=$(cd "$(dirname "$0")/.." && pwd)
D=/data/libreecho/local/services/$SVC
OUT=$(mktemp -d)
trap 'rm -rf "$OUT"' EXIT
case "$SVC" in
	puente|vigia) (cd "$ROOT/$SVC" && GOTOOLCHAIN=local CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 "$GO" build -buildvcs=false -trimpath -o "$OUT/$SVC" .) ;;
	ota) "$ROOT/tools/ota-release.sh" --build-agent >/dev/null && cp "$ROOT/dist/ota" "$OUT/ota" ;;
	*) echo "unknown service $SVC" >&2; exit 1 ;;
esac
adb -s "$SERIAL" push "$OUT/$SVC" "$D/$SVC.new" >/dev/null
adb -s "$SERIAL" shell "chmod 755 $D/$SVC.new && echo $SVC \$((\$(date +%s)+180)) >> /data/libreecho/local/vigia/planned && mv $D/$SVC.new $D/$SVC && kill \$(pidof $SVC) 2>/dev/null; sleep 8; grep service.$SVC= /run/libreecho/local.status"
