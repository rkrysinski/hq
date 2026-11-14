#!/bin/sh
# Cut a release: bump the version, commit, tag and push. Pushing the tag starts
# .github/workflows/release.yml, which writes the release notes. Run on a
# developer machine from a clean `dev`; see docs/releasing.md.
#
# Usage: scripts/cut-release.sh <X.Y.Z> [--dry-run] [--no-push]
#
#   --dry-run  print every step and change nothing, locally or on the remote
#   --no-push  release branch only: stop after the tag and print the push
#
# On a `release-vX.Y.Z` branch (release mode) the version is already bumped, as
# the branch's last commit, and the candidate HEAD is tagged as it is; a local
# tag already at HEAD is kept. The version must be higher than every version
# already tagged. Anything that fails leaves the repository exactly as it was: no
# release commit, no local tag it created, nothing pushed.
#
# Exits 0 on success, 1 on a refusal or failure, 2 on usage errors.
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
REPO_ROOT=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)
. "$SCRIPT_DIR/lib/release-notes.sh"
. "$SCRIPT_DIR/lib/repo-from-url.sh"

RELEASE_NOTES_PROG=cut-release
BRANCH=dev

usage() {
    sed -n '2,18p' "$0" | sed 's/^# \{0,1\}//'
    exit 2
}

die() {
    echo "cut-release: $*" >&2
    exit 1
}

VERSION=${1:-}
[ -n "$VERSION" ] || usage
case "$VERSION" in -*) usage ;; esac
shift

DRY_RUN=0
NO_PUSH=0
while [ $# -gt 0 ]; do
    case "$1" in
        --dry-run) DRY_RUN=1; shift ;;
        --no-push) NO_PUSH=1; shift ;;
        *) usage ;;
    esac
done

echo "$VERSION" | grep -qE '^[0-9]+\.[0-9]+\.[0-9]+$' \
    || die "'$VERSION' is not a semver version (X.Y.Z, no leading v)"
TAG="v$VERSION"

cd "$REPO_ROOT"

command -v gh >/dev/null 2>&1 || die "the gh CLI is not installed"
command -v jq >/dev/null 2>&1 || die "jq is not installed"

# ------------------------------------------------------------- preflight ----

current=$(git rev-parse --abbrev-ref HEAD)
RELEASE_MODE=0
if release_notes_is_release_branch "$current"; then
    RELEASE_MODE=1
    BRANCH=$current
fi
[ "$current" = "$BRANCH" ] || die "on branch '$current' - cut a release from '$BRANCH' or a release-vX.Y.Z branch"
[ "$NO_PUSH" -eq 0 ] || [ "$RELEASE_MODE" -eq 1 ] \
    || die "--no-push is for a release-vX.Y.Z branch - on $BRANCH the release commit and tag are pushed together"

[ -z "$(git status --porcelain)" ] \
    || die "the working tree has uncommitted changes - commit or stash them first"

git fetch -q origin "$BRANCH" || die "could not fetch origin/$BRANCH"
git fetch -q --tags origin || die "could not fetch tags from origin"

ahead=$(git rev-list --count "origin/$BRANCH..$BRANCH")
behind=$(git rev-list --count "$BRANCH..origin/$BRANCH")
[ "$ahead" -eq 0 ] || die "$BRANCH is $ahead commit(s) ahead of origin/$BRANCH - push them first"
[ "$behind" -eq 0 ] || die "$BRANCH is $behind commit(s) behind origin/$BRANCH - pull first"

# Checked before the order rule below: an existing tag would otherwise be
# reported as "not higher than the newest", which is true but says nothing useful.
# In release mode a local tag already on HEAD is a previous run on this candidate.
TAG_EXISTS=0
if local_tag=$(git rev-parse -q --verify "refs/tags/$TAG^{commit}"); then
    head=$(git rev-parse HEAD)
    if [ "$RELEASE_MODE" -eq 1 ] && [ "$local_tag" = "$head" ]; then
        TAG_EXISTS=1
    elif [ "$RELEASE_MODE" -eq 1 ]; then
        die "tag $TAG already exists locally at $local_tag, not at HEAD $head - delete it (git tag -d $TAG) to tag this candidate"
    else
        die "tag $TAG already exists locally - that version has been cut"
    fi
fi
git ls-remote --exit-code --tags origin "refs/tags/$TAG" >/dev/null 2>&1 \
    && die "tag $TAG already exists on origin - that version has been cut"

# Newest version released so far, in numeric order (v1.10.0 is above v1.9.3),
# leaving out this run's own tag when an earlier run left it on HEAD.
# Empty when nothing has been tagged yet: the first release.
PREV=$(git tag --list | grep -vxF "$TAG" | "$SCRIPT_DIR/version.sh" sort | tail -1) || PREV=""
if [ -n "$PREV" ]; then
    highest=$(printf '%s\n%s\n' "$PREV" "$TAG" | "$SCRIPT_DIR/version.sh" sort | tail -1)
    [ "$highest" = "$TAG" ] || die "$VERSION is not higher than the newest release $PREV"
fi

origin_url=$(git remote get-url origin) || die "no 'origin' remote"
REPO=$(repo_from_url "$origin_url")
# `gh auth status` is unreliable in sandboxed environments where credentials are
# injected at the network layer, so prove access with a real call instead.
gh api "repos/$REPO" -q .full_name >/dev/null 2>&1 \
    || die "cannot read $REPO through gh - check authentication"

# A release branch is bumped by its own last commit, before Release QA tests
# it, so bump-version.sh must find nothing to change. The tree was clean, so
# putting it back cannot lose anything.
if [ "$RELEASE_MODE" -eq 1 ]; then
    "$SCRIPT_DIR/bump-version.sh" "$VERSION" >/dev/null \
        || { git reset -q --hard HEAD; git clean -qfd; die "bump-version.sh $VERSION failed"; }
    if [ -n "$(git status --porcelain)" ]; then
        changed=$(git status --porcelain | cut -c4- | paste -s -d ' ' -)
        git reset -q --hard HEAD
        git clean -qfd
        die "$BRANCH is not bumped to $VERSION (bump-version.sh changes $changed) - run scripts/bump-version.sh $VERSION and commit it as the branch's last commit"
    fi
fi

ORIGINAL_HEAD=$(git rev-parse HEAD)

echo "repository : $REPO"
echo "branch     : $BRANCH at $(git rev-parse --short HEAD)"
echo "previous   : ${PREV:-none (first release)}"
echo "new tag    : $TAG"
if [ "$RELEASE_MODE" -eq 1 ]; then
    echo "mode       : release branch (version files already read $VERSION)"
    [ "$TAG_EXISTS" -eq 1 ] && echo "local tag  : $TAG already at HEAD - kept"
fi
[ "$DRY_RUN" -eq 1 ] && echo "mode       : dry run (nothing is changed)"
echo

# --------------------------------------------------- what goes in the tag ----

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT INT TERM

# Collected before anything is modified, so a lookup failure cannot leave a
# release commit behind.
release_notes_collect "$REPO" "$PREV" HEAD "$TMP" || exit 1
release_notes_issue_labels "$REPO" "$TMP"

{
    echo "Release $TAG"
    echo
    if [ -s "$TMP/issues.tsv" ]; then
        while IFS="$(printf '\t')" read -r number title labels; do
            [ -n "$number" ] || continue
            # Left out exactly as in the release notes (scripts/release.sh).
            release_notes_is_spec "$labels" && continue
            printf -- '- %s (#%s)\n' "$title" "$number"
        done < "$TMP/issues.tsv"
    fi
    if [ -s "$TMP/internal.tsv" ]; then
        while IFS="$(printf '\t')" read -r number title; do
            [ -n "$number" ] || continue
            printf -- '- %s (#%s)\n' "$title" "$number"
        done < "$TMP/internal.tsv"
    fi
} > "$TMP/tag-message"

echo "tag message:"
sed 's/^/  /' "$TMP/tag-message"
echo

# ------------------------------------------------------------------ steps ----

# Everything below this line changes the repository. Each failure unwinds back
# to ORIGINAL_HEAD before exiting, so a refused cut is indistinguishable from
# never having run.
unwind() {
    # A signal that arrives while `git push` is running is handled only once push
    # returns, so the tag may already be on origin by the time we get here.
    # Rolling the checkout back then would hide a release that has really been
    # cut, so ask origin before undoing anything.
    if git ls-remote --exit-code --tags origin "refs/tags/$TAG" >/dev/null 2>&1; then
        echo "cut-release: $TAG is already on origin - the cut stands, leaving this checkout alone" >&2
        return 0
    fi
    [ "$TAG_EXISTS" -eq 1 ] || git tag -d "$TAG" >/dev/null 2>&1 || true
    git reset -q --hard "$ORIGINAL_HEAD"
}

interrupted() {
    echo >&2
    echo "cut-release: interrupted - rolling back to $(git rev-parse --short "$ORIGINAL_HEAD")" >&2
    unwind
    exit 1
}

if [ "$DRY_RUN" -eq 1 ]; then
    if [ "$RELEASE_MODE" -eq 0 ]; then
        echo "would run : scripts/bump-version.sh $VERSION"
        echo "would run : git commit -am 'Release $VERSION'   (skipped if the files already read $VERSION)"
    fi
    if [ "$TAG_EXISTS" -eq 1 ]; then
        echo "would keep: $TAG, already at HEAD"
    else
        echo "would run : git tag -a $TAG -F <the message above>"
    fi
    if [ "$NO_PUSH" -eq 1 ]; then
        echo "would stop: the push is yours - git push origin $TAG"
    else
        echo "would run : git push --atomic origin $BRANCH $TAG"
    fi
    echo
    echo "dry run: nothing was changed"
    exit 0
fi

# From here until the push succeeds, a Ctrl-C would otherwise leave a release
# commit behind. SIGKILL cannot be caught; docs/releasing.md says how to
# recover by hand if that happens.
trap interrupted INT TERM

# On a release branch the bump was checked above and the candidate is tagged
# as it is.
if [ "$RELEASE_MODE" -eq 0 ]; then
    echo "bumping the version to $VERSION"
    "$SCRIPT_DIR/bump-version.sh" "$VERSION" || die "bump-version.sh failed - nothing has changed"

    # The files may already carry this version; then there is no release commit to
    # make and the tag goes on the commit as it is.
    if [ -n "$(git status --porcelain)" ]; then
        git commit -q -am "Release $VERSION" || { unwind; die "could not create the release commit"; }
        echo "committed $(git rev-parse --short HEAD) Release $VERSION"
    else
        echo "version files already read $VERSION - tagging $(git rev-parse --short HEAD) as it is"
    fi
fi

if [ "$TAG_EXISTS" -eq 1 ]; then
    echo "$TAG is already at $(git rev-parse --short HEAD) - kept"
else
    git tag -a "$TAG" -F "$TMP/tag-message" || { unwind; die "could not create tag $TAG"; }
    echo "tagged $TAG"
fi

if [ "$NO_PUSH" -eq 1 ]; then
    trap - INT TERM
    echo
    echo "tagged $TAG at $(git rev-parse --short HEAD), nothing pushed"
    echo "next: git push origin $TAG   (runs .github/workflows/release.yml)"
    exit 0
fi

echo "pushing $BRANCH and $TAG"
if ! git push --atomic origin "$BRANCH" "$TAG"; then
    unwind
    die "push failed - the release commit and tag have been rolled back, origin is untouched"
fi

trap - INT TERM

echo
echo "cut $TAG"
echo "next: .github/workflows/release.yml publishes the release notes (or run scripts/release.sh $TAG)"
