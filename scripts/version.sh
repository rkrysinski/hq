#!/bin/sh
# The order of version tags and the next version number, defined once. Every
# script and skill that compares or increments versions calls this one, so the
# order is the same on macOS (BSD sort) and Linux and is never re-implemented
# inline. A version is vMAJOR.MINOR.PATCH, three numbers compared as numbers
# (v1.101.3 is above v1.100.4, v1.10.0 above v1.9.9); anything else, such as a
# pre-release (v1.0.0-rc1), is not a version and is dropped.
#
# Usage: scripts/version.sh sort
#        scripts/version.sh latest [--remote <name>]
#        scripts/version.sh next-patch|next-minor|next-major <tag>
#
#   sort         read tags (or versions without the v) on stdin, print the
#                versions among them, lowest first
#   latest       the highest version tag of this repository, or of the remote
#                <name> with --remote; prints nothing and exits 0 when none
#   next-patch   the version after <tag>: v1.10.4 gives v1.10.5
#   next-minor   v1.10.4 gives v1.11.0
#   next-major   v1.10.4 gives v2.0.0
#
# Exits 0 on success, 1 on a failure, 2 on usage errors.
set -eu

usage() {
    sed -n '2,20p' "$0" | sed 's/^# \{0,1\}//'
    exit 2
}

die() {
    echo "version: $*" >&2
    exit 1
}

# Version order with POSIX tools only: awk keeps the lines of the form
# [v]N.N.N and puts the three numbers in front, sort compares them as numbers
# (the line itself breaks a tie, so the output is the same on every platform),
# cut takes the numbers off again.
sort_versions() {
    awk -F. '/^v?[0-9]+\.[0-9]+\.[0-9]+$/ { v = $1; sub(/^v/, "", v); print v "\t" $2 "\t" $3 "\t" $0 }' \
        | sort -t "$(printf '\t')" -k1,1n -k2,2n -k3,3n -k4,4 \
        | cut -f4
}

# The tags of a remote, one per line; the peeled entries (refs/tags/x^{}) of
# annotated tags name the same tag a second time and are ignored.
remote_tags() {
    out=$(git ls-remote --tags "$1") || return 1
    printf '%s\n' "$out" | awk -F'\t' '$2 ~ /^refs\/tags\// && $2 !~ /\^\{\}$/ { sub(/^refs\/tags\//, "", $2); print $2 }'
}

# Prints the three numbers of a tag, each on its own line.
parts() {
    printf '%s\n' "$1" | grep -qE '^v[0-9]+\.[0-9]+\.[0-9]+$' \
        || die "'$1' is not a version tag (vX.Y.Z)"
    printf '%s\n' "${1#v}" | tr . '\n'
}

[ $# -gt 0 ] || usage
CMD=$1
shift

case "$CMD" in
    sort)
        [ $# -eq 0 ] || usage
        sort_versions
        ;;
    latest)
        REMOTE=""
        while [ $# -gt 0 ]; do
            case "$1" in
                --remote) [ $# -ge 2 ] || usage; REMOTE=$2; shift 2 ;;
                *) usage ;;
            esac
        done
        if [ -n "$REMOTE" ]; then
            tags=$(remote_tags "$REMOTE") || die "could not list the tags of '$REMOTE'"
        else
            tags=$(git tag --list) || die "could not list the tags"
        fi
        printf '%s\n' "$tags" | sort_versions | tail -1
        ;;
    next-patch | next-minor | next-major)
        [ $# -eq 1 ] || usage
        nums=$(parts "$1") || exit 1
        set -- $nums
        case "$CMD" in
            next-patch) echo "v$1.$2.$(($3 + 1))" ;;
            next-minor) echo "v$1.$(($2 + 1)).0" ;;
            next-major) echo "v$(($1 + 1)).0.0" ;;
        esac
        ;;
    *) usage ;;
esac
