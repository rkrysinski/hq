#!/bin/sh
# Build the files of a GitHub release: hq for each platform with the version
# baked in, install.sh, and SHA256SUMS over them (design §3.9, §7.3). The
# release workflow runs this on every v* tag and uploads OUTDIR.
#
# Usage: scripts/build-release.sh vX.Y.Z OUTDIR [GOOS/GOARCH...]
#        (default: every platform hq ships for)
set -eu

VERSION=${1:-}
OUT=${2:-}
case "$VERSION" in v[0-9]*.[0-9]*.[0-9]*) ;; *)
    echo "usage: scripts/build-release.sh vX.Y.Z OUTDIR [GOOS/GOARCH...]" >&2
    exit 2 ;;
esac
[ -n "$OUT" ] || { echo "usage: scripts/build-release.sh vX.Y.Z OUTDIR [GOOS/GOARCH...]" >&2; exit 2; }
shift 2
PLATFORMS=${*:-darwin/arm64 darwin/amd64 linux/amd64 linux/arm64}

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
mkdir -p "$OUT"
OUT=$(CDPATH= cd -- "$OUT" && pwd)

for p in $PLATFORMS; do
    os=${p%/*}
    arch=${p#*/}
    (cd "$ROOT" && CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath \
        -ldflags "-s -w -X github.com/rkrysinski/hq/internal/version.Version=$VERSION" \
        -o "$OUT/hq-$os-$arch" ./cmd/hq)
    echo "built hq-$os-$arch"
done
cp "$ROOT/scripts/install.sh" "$OUT/install.sh"

cd "$OUT"
if command -v sha256sum >/dev/null 2>&1; then
    sha256sum hq-* install.sh >SHA256SUMS
else
    shasum -a 256 hq-* install.sh >SHA256SUMS
fi
echo "wrote SHA256SUMS"
