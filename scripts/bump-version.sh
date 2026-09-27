#!/bin/sh
# Set the project version in every file that records it, so a tag and the code
# it points at agree. scripts/cut-release.sh runs this; see docs/releasing.md.
#
# Usage: scripts/bump-version.sh X.Y.Z
#
# No file records hq's version: the git tag is the only record, and a release
# build bakes it into the binary (internal/version, set with -ldflags). If a
# file ever needs the version, write it here, so there is still one command.
set -eu

VERSION=${1:-}
echo "$VERSION" | grep -qE '^[0-9]+\.[0-9]+\.[0-9]+$' || {
    echo "usage: scripts/bump-version.sh X.Y.Z (semver, no leading v)" >&2
    exit 2
}

echo "version set to $VERSION"
echo "  (no version files yet, nothing changed)"
