#!/bin/sh
# Set the project version in every file that records it, so a tag and the code
# it points at agree. scripts/cut-release.sh runs this; see docs/releasing.md.
#
# Usage: scripts/bump-version.sh X.Y.Z
#
# hq has no implementation yet, so no file records the version and the git tag
# is the only record. When the stack is chosen, write the version into its
# manifest here, so there is still one command to run.
set -eu

VERSION=${1:-}
echo "$VERSION" | grep -qE '^[0-9]+\.[0-9]+\.[0-9]+$' || {
    echo "usage: scripts/bump-version.sh X.Y.Z (semver, no leading v)" >&2
    exit 2
}

echo "version set to $VERSION"
echo "  (no version files yet, nothing changed)"
