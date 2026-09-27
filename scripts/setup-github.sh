#!/bin/sh
# Prepare a GitHub repository for the way this project works: the labels the
# agent docs and the release notes rely on, the `qa-artifacts` branch that holds
# QA evidence, and the repository settings. A repository created from a template
# inherits none of these, only the files.
#
# Usage: scripts/setup-github.sh [--dry-run] [--repo <owner/name>]
#
#   --dry-run  print every write instead of performing it
#   --repo     target repository (default: the `origin` remote)
#
# Safe to run again: labels are updated in place, an existing `qa-artifacts`
# branch is left alone. See docs/agents/triage-labels.md and
# docs/agents/qa-evidence.md.
#
# Exits 0 on success, 1 on a failure that needs a human, 2 on usage errors.
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
REPO_ROOT=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)
QA_BRANCH=qa-artifacts

usage() {
    sed -n '2,16p' "$0" | sed 's/^# \{0,1\}//'
    exit 2
}

die() {
    echo "setup-github: $*" >&2
    exit 1
}

DRY_RUN=0
REPO=""
while [ $# -gt 0 ]; do
    case "$1" in
        --dry-run) DRY_RUN=1; shift ;;
        --repo) REPO=${2:-}; [ -n "$REPO" ] || usage; shift 2 ;;
        *) usage ;;
    esac
done

cd "$REPO_ROOT"

command -v gh >/dev/null 2>&1 || die "the gh CLI is not installed"

if [ -z "$REPO" ]; then
    origin=$(git remote get-url origin 2>/dev/null) || die "no 'origin' remote; pass --repo <owner/name>"
    REPO=$(echo "$origin" | sed -e 's#^git@[^:]*:##' -e 's#^https\{0,1\}://[^/]*/##' -e 's#\.git$##')
    # Push the way this checkout already pushes (HTTPS or SSH).
    PUSH_TARGET=origin
else
    PUSH_TARGET="https://github.com/$REPO.git"
fi
case "$REPO" in
    */*) ;;
    *) die "--repo must be owner/name, got '$REPO'" ;;
esac

# `gh auth status` is unreliable in sandboxed environments where credentials are
# injected at the network layer, so prove access with a real call instead.
gh api "repos/$REPO" -q .full_name >/dev/null 2>&1 \
    || die "cannot read $REPO through gh - check authentication and the repository name"

echo "repository : $REPO"
[ "$DRY_RUN" -eq 1 ] && echo "mode       : dry run (no writes)"
echo

# ----------------------------------------------------------------- labels ----

# <name>|<colour>|<description>. The first three exist in every new GitHub
# repository; they are listed so that a repository which lost them gets them back.
LABELS='bug|d73a4a|Something that was broken for a user
enhancement|a2eeef|Something a user of the product can see or do
wontfix|ffffff|Will not be actioned
internal|ededed|Tooling, CI, deployment or docs; invisible to the product'"'"'s users
needs-triage|fbca04|Maintainer needs to evaluate this issue
needs-info|d876e3|Waiting on reporter for more information
ready-for-agent|0e8a16|Fully specified, ready for an AFK agent
ready-for-human|1d76db|Requires human implementation
prd|5319e7|Product requirements document; implementation issues are sub-issues
claimed|c5def5|Claimed by an agent session
wayfinder:map|006b75|Wayfinder map
wayfinder:research|bfdadc|Wayfinder research ticket
wayfinder:prototype|bfdadc|Wayfinder prototype ticket
wayfinder:grilling|bfdadc|Wayfinder grilling ticket
wayfinder:task|bfdadc|Wayfinder task ticket'

echo "labels:"
echo "$LABELS" | while IFS='|' read -r name colour description; do
    [ -n "$name" ] || continue
    if [ "$DRY_RUN" -eq 1 ]; then
        echo "  would create or update '$name'"
    else
        # </dev/null: gh would otherwise read this loop's stdin and eat the list.
        gh label create "$name" --repo "$REPO" --color "$colour" --description "$description" --force \
            < /dev/null >/dev/null \
            || die "could not create label '$name'"
        echo "  $name"
    fi
done
echo

# ------------------------------------------------------------ qa-artifacts ----

# Built with plumbing, as a commit with no parent and one file, and pushed by
# its hash: the working tree, the index and the current branch are not touched.
echo "branch $QA_BRANCH:"
if gh api "repos/$REPO/branches/$QA_BRANCH" -q .name >/dev/null 2>&1; then
    echo "  already exists"
elif [ "$DRY_RUN" -eq 1 ]; then
    echo "  would create it with a README.md and push it to $REPO"
else
    blob=$(git hash-object -w --stdin <<'README'
# QA artifacts

QA evidence (screenshots, exported files, command output) recorded by agents for pull requests.
This branch never merges into `main`. Evidence for an issue lives under `issue-<n>/`; PR bodies link to it
by commit SHA. See `docs/agents/qa-evidence.md` on `main`.
README
    )
    tree=$(printf '100644 blob %s\tREADME.md\n' "$blob" | git mktree)
    commit=$(git commit-tree "$tree" -m "Start the QA artifacts branch")
    git push -q "$PUSH_TARGET" "$commit:refs/heads/$QA_BRANCH" \
        || die "could not push $QA_BRANCH to $REPO"
    echo "  created"
fi
echo

# --------------------------------------------------------------- settings ----

# Issues are the tracker; the wiki is off because documentation lives in docs/.
echo "settings:"
if [ "$DRY_RUN" -eq 1 ]; then
    echo "  would enable issues and disable the wiki"
else
    gh repo edit "$REPO" --enable-issues --enable-wiki=false >/dev/null \
        || die "could not update the settings of $REPO"
    echo "  issues on, wiki off"
fi

echo
echo "https://github.com/$REPO"
