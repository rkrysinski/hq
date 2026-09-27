#!/bin/sh
# Publish the GitHub release for a tag: notes derived from the pull requests
# merged since the previous tag, a milestone stamped on every issue and PR in
# the release, and a comment on each issue pointing back at the release.
#
# Usage: scripts/release.sh <vX.Y.Z> [--dry-run] [--no-issue-writes]
#                           [--repo <owner/name>]
#
#   --dry-run          print every write instead of performing it
#   --no-issue-writes  publish the release but touch no issue or milestone
#   --repo             target repository (default: the `origin` remote)
#
# Run from a checkout that has the tag and the history behind it. Everything the
# script writes is scoped to --repo; issue references are only ever read as bare
# `#<n>`, so a cross-repository reference can never pull another repo in.
#
# Issue labels decide the section: `enhancement` -> Nowe funkcje, `bug` -> Poprawki,
# `internal` -> Wewnętrzne (alongside pull requests that closed no issue), the rest
# -> Pozostałe.
#
# Exits 0 on success, 1 on a failure that needs a human, 2 on usage errors.
#
# See docs/releasing.md.
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
REPO_ROOT=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)
. "$SCRIPT_DIR/lib/release-notes.sh"

# The notes link to this document when the tagged version has it: the
# project's own deployment procedure, written for whoever installs a version.
DEPLOY_DOC=docs/deployment.md
GENERATED_BEGIN='<!-- generated -->'
GENERATED_END='<!-- /generated -->'

# Wording of the notes: Polish, for the people who read them. Change it here
# and in docs/agents/triage-labels.md together. The prerequisite headings are
# RELEASE_NOTES_BEFORE_HEADING and RELEASE_NOTES_AFTER_HEADING in
# scripts/lib/release-notes.sh, because pull requests use them too.
HEADING_FEATURES='Nowe funkcje'
HEADING_FIXES='Poprawki'
HEADING_OTHER='Pozostałe'
HEADING_INTERNAL='Wewnętrzne'
HEADING_DEPLOY='Wdrożenie'
TEXT_BEFORE='Do zrobienia, zanim wdrożysz tę wersję:'
TEXT_AFTER='Do zrobienia na produkcji po wdrożeniu:'
TEXT_DEPLOY_DOC='Instrukcja'
TEXT_MILESTONE='Wydanie'
TEXT_RELEASED_IN='Wydane w'

usage() {
    sed -n '2,23p' "$0" | sed 's/^# \{0,1\}//'
    exit 2
}

die() {
    echo "release: $*" >&2
    exit 1
}

VERSION=${1:-}
[ -n "$VERSION" ] || usage
case "$VERSION" in -*) usage ;; esac
shift

DRY_RUN=0
ISSUE_WRITES=1
REPO=""
while [ $# -gt 0 ]; do
    case "$1" in
        --dry-run) DRY_RUN=1; shift ;;
        --no-issue-writes) ISSUE_WRITES=0; shift ;;
        --repo) REPO=${2:-}; [ -n "$REPO" ] || usage; shift 2 ;;
        *) usage ;;
    esac
done

case "$VERSION" in
    v[0-9]*.[0-9]*.[0-9]*) ;;
    *) die "'$VERSION' is not a vX.Y.Z tag name" ;;
esac

cd "$REPO_ROOT"

command -v gh >/dev/null 2>&1 || die "the gh CLI is not installed"
command -v jq >/dev/null 2>&1 || die "jq is not installed"

if [ -z "$REPO" ]; then
    origin=$(git remote get-url origin 2>/dev/null) || die "no 'origin' remote; pass --repo <owner/name>"
    REPO=$(echo "$origin" | sed -e 's#^git@[^:]*:##' -e 's#^https\{0,1\}://[^/]*/##' -e 's#\.git$##')
fi
case "$REPO" in
    */*) ;;
    *) die "--repo must be owner/name, got '$REPO'" ;;
esac

# `gh auth status` is unreliable in sandboxed environments where credentials are
# injected at the network layer, so prove access with a real call instead.
gh api "repos/$REPO" -q .full_name >/dev/null 2>&1 \
    || die "cannot read $REPO through gh - check authentication and the repository name"

git rev-parse -q --verify "refs/tags/$VERSION" >/dev/null \
    || die "tag $VERSION does not exist in this checkout (fetch it first: git fetch --tags)"

# The previous release is the highest version tag reachable from this one, not
# `git describe "$VERSION^"`: when two tags sit on the same commit the parent is
# behind both, describe would reach back past the other tag, and the notes would
# re-announce work that already shipped.
PREV=$(git tag --list 'v*' --merged "$VERSION" \
    | sort -t. -k1.2,1n -k2,2n -k3,3n \
    | awk -v v="$VERSION" '$0 == v { print prev; exit } { prev = $0 }') || PREV=""
# No previous tag: this is the first release, and everything behind the tag is in it.

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT INT TERM

echo "repository : $REPO"
echo "range      : ${PREV:-(first commit)}..$VERSION"
[ "$DRY_RUN" -eq 1 ] && echo "mode       : dry run (no writes)"
[ "$ISSUE_WRITES" -eq 0 ] && echo "mode       : release only (no issue or milestone writes)"
echo

# ---------------------------------------------------------------- collect ----

RELEASE_NOTES_PROG=release
release_notes_collect "$REPO" "$PREV" "$VERSION" "$TMP" || exit 1
release_notes_issue_labels "$REPO" "$TMP"
release_notes_prerequisites "$TMP"

: > "$TMP/features.tsv"
: > "$TMP/fixes.tsv"
: > "$TMP/other.tsv"
: > "$TMP/issues"
while IFS="$(printf '\t')" read -r issue title labels; do
    [ -n "$issue" ] || continue
    echo "$issue" >> "$TMP/issues"
    case ",$labels," in
        *,enhancement,*) printf '%s\t%s\n' "$issue" "$title" >> "$TMP/features.tsv" ;;
        *,bug,*) printf '%s\t%s\n' "$issue" "$title" >> "$TMP/fixes.tsv" ;;
        # Tooling, CI and deployment work: real issues, but nothing a user of the
        # platform would recognise, so they join the internal section.
        *,internal,*) printf '%s\t%s\n' "$issue" "$title" >> "$TMP/internal.tsv" ;;
        *) printf '%s\t%s\n' "$issue" "$title" >> "$TMP/other.tsv" ;;
    esac
done < "$TMP/issues.tsv"

echo "prerequisites : $(grep -c '^\*\*.*(#[0-9]*)\*\*$' "$TMP/before.md") before deployment, $(grep -c '^\*\*.*(#[0-9]*)\*\*$' "$TMP/after.md") after (from pull request sections)"
echo

# ----------------------------------------------------------------- notes ----

section() {
    # section <heading> <anchor> <tsv-file>
    # GitHub gives release-note headings no id of their own, so each section
    # carries an explicit anchor: .../releases/tag/vX.Y.Z#<anchor> deep-links to it.
    [ -s "$3" ] || return 0
    printf '<a name="%s"></a>\n## %s\n\n' "$2" "$1"
    while IFS="$(printf '\t')" read -r number title; do
        printf -- '- %s (#%s)\n' "$title" "$number"
    done < "$3"
    printf '\n'
}

HAS_DEPLOY_DOC=0
git cat-file -e "$VERSION:$DEPLOY_DOC" 2>/dev/null && HAS_DEPLOY_DOC=1

{
    echo "$GENERATED_BEGIN"
    echo
    section "$HEADING_FEATURES" nowe-funkcje "$TMP/features.tsv"
    section "$HEADING_FIXES" poprawki "$TMP/fixes.tsv"
    section "$HEADING_OTHER" pozostale "$TMP/other.tsv"
    sort -n "$TMP/internal.tsv" -o "$TMP/internal.tsv"
    section "$HEADING_INTERNAL" wewnetrzne "$TMP/internal.tsv"
    # The deployment part appears only when there is something to say in it.
    if [ -s "$TMP/before.md" ] || [ -s "$TMP/after.md" ] || [ "$HAS_DEPLOY_DOC" -eq 1 ]; then
        echo '<a name="wdrozenie"></a>'
        echo "## $HEADING_DEPLOY"
        echo
        if [ -s "$TMP/before.md" ]; then
            echo '<a name="przed-wdrozeniem"></a>'
            echo "### $RELEASE_NOTES_BEFORE_HEADING"
            echo
            echo "$TEXT_BEFORE"
            echo
            cat "$TMP/before.md"
        fi
        # Invisible anchor that ends the block above, so a deploy script can read
        # the steps to confirm back from the notes.
        echo '<a name="polecenie"></a>'
        echo
        if [ -s "$TMP/after.md" ]; then
            echo '<a name="po-wdrozeniu"></a>'
            echo "### $RELEASE_NOTES_AFTER_HEADING"
            echo
            echo "$TEXT_AFTER"
            echo
            cat "$TMP/after.md"
        fi
        if [ "$HAS_DEPLOY_DOC" -eq 1 ]; then
            echo "$TEXT_DEPLOY_DOC: [$DEPLOY_DOC](https://github.com/$REPO/blob/$VERSION/$DEPLOY_DOC)"
            echo
        fi
    fi
    echo "$GENERATED_END"
} > "$TMP/generated"

# ---------------------------------------------------------------- publish ----

# Bodies edited in the browser come back with CRLF line endings; drop the
# carriage returns first, or the marker lines never match and the generated
# block would silently never be refreshed.
if gh release view "$VERSION" --repo "$REPO" --json body -q '.body' > "$TMP/existing-raw" 2>/dev/null; then
    RELEASE_EXISTS=1
    tr -d '\r' < "$TMP/existing-raw" > "$TMP/existing"
else
    RELEASE_EXISTS=0
    : > "$TMP/existing"
fi

if [ "$RELEASE_EXISTS" -eq 1 ] && grep -qF "$GENERATED_BEGIN" "$TMP/existing"; then
    # Replace only the generated block; hand-written text around it survives.
    awk -v begin="$GENERATED_BEGIN" -v end="$GENERATED_END" -v gen="$TMP/generated" '
        $0 == begin { inside = 1; while ((getline line < gen) > 0) print line; close(gen); next }
        $0 == end { inside = 0; next }
        !inside { print }
    ' "$TMP/existing" > "$TMP/body"
elif [ "$RELEASE_EXISTS" -eq 1 ]; then
    cat "$TMP/existing" > "$TMP/body"
    printf '\n' >> "$TMP/body"
    cat "$TMP/generated" >> "$TMP/body"
else
    cat "$TMP/generated" > "$TMP/body"
fi

# GitHub stores a release body with a trailing newline of its own and hands back
# CRLF for bodies edited in the browser. Without normalising both, every re-run
# would splice in one more blank line and the notes would never settle.
NORMALISED=$(tr -d '\r' < "$TMP/body")
printf '%s\n' "$NORMALISED" > "$TMP/body"

# `gh release create` and `gh release edit` store trailing whitespace slightly
# differently, so compare the normalised forms and skip the call when the notes
# have not actually changed. That also keeps a re-run from touching the release
# at all.
UNCHANGED=0
if [ "$RELEASE_EXISTS" -eq 1 ]; then
    EXISTING_NORMALISED=$(tr -d '\r' < "$TMP/existing")
    printf '%s\n' "$EXISTING_NORMALISED" > "$TMP/existing-normalised"
    cmp -s "$TMP/existing-normalised" "$TMP/body" && UNCHANGED=1
fi

if [ "$DRY_RUN" -eq 1 ]; then
    if [ "$UNCHANGED" -eq 1 ]; then
        echo "release $VERSION is already up to date; notes would be:"
    elif [ "$RELEASE_EXISTS" -eq 1 ]; then
        echo "would update release $VERSION with:"
    else
        echo "would create release $VERSION with:"
    fi
    sed 's/^/  /' "$TMP/body"
    echo
else
    if [ "$UNCHANGED" -eq 1 ]; then
        echo "release $VERSION already up to date"
    elif [ "$RELEASE_EXISTS" -eq 1 ]; then
        gh release edit "$VERSION" --repo "$REPO" --notes-file "$TMP/body" >/dev/null \
            || die "could not update release $VERSION"
        echo "updated release $VERSION"
    else
        gh release create "$VERSION" --repo "$REPO" --title "$VERSION" --notes-file "$TMP/body" >/dev/null \
            || die "could not create release $VERSION"
        echo "created release $VERSION"
    fi
fi

RELEASE_URL="https://github.com/$REPO/releases/tag/$VERSION"

# ------------------------------------------------------- milestone, notes ----

if [ "$ISSUE_WRITES" -eq 0 ]; then
    echo "skipped milestone and issue comments (--no-issue-writes)"
    exit 0
fi

gh api "repos/$REPO/milestones?state=all&per_page=100" --paginate \
    -q ".[] | select(.title == \"$VERSION\") | .number" > "$TMP/milestone" 2>/dev/null \
    || die "could not list milestones for $REPO"
MILESTONE=$(head -1 "$TMP/milestone")

if [ -z "$MILESTONE" ]; then
    if [ "$DRY_RUN" -eq 1 ]; then
        echo "would create milestone $VERSION"
        MILESTONE="(new)"
    else
        MILESTONE=$(gh api "repos/$REPO/milestones" -f "title=$VERSION" \
            -f "description=$TEXT_MILESTONE $VERSION" -q '.number') \
            || die "could not create milestone $VERSION"
        echo "created milestone $VERSION (#$MILESTONE)"
    fi
else
    echo "milestone $VERSION already exists (#$MILESTONE)"
fi

# Issues and PRs share one numbering space, and the issues API sets the
# milestone on both.
cat "$TMP/issues" > "$TMP/stamp" 2>/dev/null || : > "$TMP/stamp"
cut -f1 "$TMP/prs.tsv" >> "$TMP/stamp"
sort -un "$TMP/stamp" -o "$TMP/stamp"

while read -r number; do
    [ -n "$number" ] || continue
    current=$(gh api "repos/$REPO/issues/$number" -q '.milestone.title // ""' < /dev/null 2>/dev/null) || current=""
    if [ "$current" = "$VERSION" ]; then
        echo "  #$number already in milestone $VERSION"
    elif [ "$DRY_RUN" -eq 1 ]; then
        echo "  would add #$number to milestone $VERSION"
    else
        gh api "repos/$REPO/issues/$number" -X PATCH -F "milestone=$MILESTONE" < /dev/null >/dev/null \
            || die "could not add #$number to milestone $VERSION"
        echo "  added #$number to milestone $VERSION"
    fi
done < "$TMP/stamp"

COMMENT_MARKER="$TEXT_RELEASED_IN $VERSION"
while read -r issue; do
    [ -n "$issue" ] || continue
    # Not a pipeline: `gh ... | grep -c` reports the status of grep, so a failed
    # lookup would look like "no comment yet" and post a duplicate.
    gh api "repos/$REPO/issues/$issue/comments" --paginate -q '.[].body' \
        < /dev/null > "$TMP/comments" 2>/dev/null \
        || die "could not read comments on #$issue"
    if grep -qF "$COMMENT_MARKER" "$TMP/comments"; then
        echo "  #$issue already has the $VERSION comment"
    elif [ "$DRY_RUN" -eq 1 ]; then
        echo "  would comment on #$issue: $COMMENT_MARKER - $RELEASE_URL"
    else
        gh issue comment "$issue" --repo "$REPO" --body "$COMMENT_MARKER - $RELEASE_URL" \
            < /dev/null >/dev/null \
            || die "could not comment on #$issue"
        echo "  commented on #$issue"
    fi
done < "$TMP/issues"

if [ "$DRY_RUN" -eq 1 ]; then
    echo "would close milestone $VERSION"
else
    state=$(gh api "repos/$REPO/milestones/$MILESTONE" -q '.state' 2>/dev/null) || state=""
    if [ "$state" = closed ]; then
        echo "milestone $VERSION already closed"
    else
        gh api "repos/$REPO/milestones/$MILESTONE" -X PATCH -f state=closed >/dev/null \
            || die "could not close milestone $VERSION"
        echo "closed milestone $VERSION"
    fi
fi

echo
echo "$RELEASE_URL"
