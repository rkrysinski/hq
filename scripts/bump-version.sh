#!/bin/sh
# Set the project version, so a tag and the code it points at agree.
# scripts/cut-release.sh runs this; see docs/releasing.md.
#
# Usage: scripts/bump-version.sh X.Y.Z
#
# VERSION at the repository root is the single source of truth, and the running
# app reads it. When the stack records the version in its own files too
# (pyproject.toml, package.json, pom.xml), scripts/sync-version.sh copies it
# there; this script runs it when it exists, so there is still one command.
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
REPO_ROOT=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)

VERSION=${1:-}
echo "$VERSION" | grep -qE '^[0-9]+\.[0-9]+\.[0-9]+$' || {
    echo "usage: scripts/bump-version.sh X.Y.Z (semver, no leading v)" >&2
    exit 2
}

cd "$REPO_ROOT"

echo "version set to $VERSION"
if [ "$(cat VERSION 2>/dev/null)" = "$VERSION" ]; then
    echo "  VERSION already reads $VERSION"
else
    printf '%s\n' "$VERSION" > VERSION
    echo "  updated VERSION"
fi

if [ -f "$SCRIPT_DIR/sync-version.sh" ]; then
    sh "$SCRIPT_DIR/sync-version.sh" "$VERSION"
fi
