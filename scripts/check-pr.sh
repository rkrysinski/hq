#!/bin/sh
# Check a pull request's title and description before review: the parts a
# reviewer needs (.github/PULL_REQUEST_TEMPLATE.md) and the deployment
# prerequisite headings scripts/release.sh copies into the release notes.
# Given the range of the pull request's commits, their `Closes` lines too.
#
# Usage: scripts/check-pr.sh [--release-pr] [--head-branch <name>] "<title>" [<base>..<head>] < description.md
#
# Fails when
#   - the title says less than what the change intends: under three words,
#     or only "update", "fix", "WIP" and the like
#   - `## Summary`, `## QA` or `## Merge Danger` is missing or holds nothing
#     but template comments
#   - a line that begins with `Closes #`, indented or not, has no issue number
#     (the rule quoted inside a line, as `Closes #<n>` in a sentence, passes)
#   - the description closes an issue and a commit of <base>..<head> has no
#     line starting with `Closes #<n>` in its message (AGENTS.md, *Issue
#     tracker*); merge commits are not checked, and without the range no
#     commit is read
#   - git cannot resolve <base>..<head>: the full history is needed
#   - a prerequisite section (`## Before deployment`, `## After deployment`) is
#     empty, sits inside an HTML comment, or its heading is misspelt (another
#     level, another case, a trailing colon, the template's Polish heading):
#     release.sh reads only the exact heading, so it would drop or garble the steps
#     (ticked steps, `- [x]`, count as content: release.sh leaves them out of
#     the notes, but the section and its rollback rule stay)
#   - a deployment prerequisite section is present and `## Rollback` is
#     missing or empty: one line per rollback step, or `None: one-way door`
#   - in release-PR mode, a closing keyword followed by `#<n>` (close, closes,
#     closed, fix, fixes, fixed, resolve, resolves, resolved) sits anywhere but
#     inside `## Closes`. The mode is on with `--release-pr`, or when
#     `--head-branch` names a `release-vX.Y.Z` branch (the workflow passes
#     the pull request's head branch); the commit rule is unchanged
# Notes, without failing, a description that closes no issue.
# Closing keywords are read as GitHub and the release notes read them: whole
# words only, and a keyword inside an HTML comment, a code span or a fenced
# code block closes nothing and is never checked.
#
# Cases (scripts/checkpr_integration_test.go):
#   `## After deployment` and no `## Rollback`       -> fails, names the section
#   the same with `## Rollback` and one line         -> passes
#   --release-pr: `Closes #12` under `## Closes`     -> passes
#   --release-pr: `Fixes #12` under `## Summary`     -> fails
#   the same description without --release-pr        -> passes, as before
#
# Exits 0 when the pull request passes, 1 with the problems listed, 2 on usage
# errors. .github/workflows/pull-request.yml runs it on every pull request that
# is ready for review, with origin/<base branch>..<head sha>; locally the range
# is origin/dev..HEAD. See docs/agents/issue-tracker.md.
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
RELEASE_NOTES_PROG=check-pr
. "$SCRIPT_DIR/lib/release-notes.sh"

RELEASE_PR=
while [ $# -gt 0 ]; do
    case $1 in
        --release-pr) RELEASE_PR=1; shift ;;
        --head-branch)
            [ $# -ge 2 ] || { sed -n '2,40p' "$0" | sed 's/^# \{0,1\}//'; exit 2; }
            if release_notes_is_release_branch "$2"; then RELEASE_PR=1; fi
            shift 2 ;;
        *) break ;;
    esac
done

[ $# -eq 1 ] || [ $# -eq 2 ] || { sed -n '2,40p' "$0" | sed 's/^# \{0,1\}//'; exit 2; }
# A single ref would make `git log` walk the whole history.
case "${2:-..}" in
    *..*) ;;
    *) echo "check-pr: '$2' is not <base>..<head>" >&2; exit 2 ;;
esac

BEFORE=$RELEASE_NOTES_BEFORE_HEADING
AFTER=$RELEASE_NOTES_AFTER_HEADING
TITLE=$(printf '%s' "$1" | tr -d '\r' | sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//')
RANGE=${2:-}
BODY=$(tr -d '\r')
PROBLEMS=0

problem() {
    echo "check-pr: $*" >&2
    PROBLEMS=$((PROBLEMS + 1))
}

has_text() {
    printf '%s\n' "$1" | grep -q '[^[:space:]]'
}

count_heading() {
    # How many lines of $2 are exactly `## $1`.
    printf '%s\n' "$2" | grep -cxF "## $1" || true
}

normalize_heading() {
    # A heading reduced to lower-case ASCII words: "### before deployment:" and
    # "## BEFORE  DEPLOYMENT" both become "before deployment".
    sed -e 's/^[[:space:]]*#*[[:space:]]*//' -e 's/[[:space:]:.]*$//' \
        -e 's/[[:space:]][[:space:]]*/ /g' \
        -e 's/ą/a/g; s/ć/c/g; s/ę/e/g; s/ł/l/g; s/ń/n/g; s/ó/o/g; s/ś/s/g; s/ź/z/g; s/ż/z/g' \
        -e 's/Ą/a/g; s/Ć/c/g; s/Ę/e/g; s/Ł/l/g; s/Ń/n/g; s/Ó/o/g; s/Ś/s/g; s/Ź/z/g; s/Ż/z/g' |
        tr 'A-Z' 'a-z'
}

VISIBLE=$(printf '%s\n' "$BODY" | release_notes_strip_comments)
# What closing keywords are read from, as release_notes_collect reads them:
# GitHub links none inside code either.
CODELESS=$(printf '%s\n' "$VISIBLE" | release_notes_strip_code)

# Title: the outcome the change intends.
words=$(printf '%s\n' "$TITLE" | awk '{ print NF }')
if [ "$words" -lt 3 ]; then
    problem "title \"$TITLE\": state the intended outcome in at least three words, e.g. \"Show the delivery date on the order list\""
elif printf '%s\n' "$TITLE" | grep -Eiq '^(wip|draft|changes?|updates?|fix(es)?|poprawki|zmiany|pull request|pr)([[:space:]]*[:#-]?[[:space:]]*[0-9]+)?$'; then
    problem "title \"$TITLE\" is too generic: state the intended outcome"
fi
if printf '%s\n' "$TITLE" | grep -Eiq '^\[?(wip|draft)\]?([[:space:]:-]|$)'; then
    problem "title \"$TITLE\" marks work in progress: finish it or convert the pull request to a draft"
fi

# The sections a reviewer needs.
for heading in Summary QA "Merge Danger"; do
    if [ "$(count_heading "$heading" "$VISIBLE")" -eq 0 ]; then
        problem "\`## $heading\` is missing (see .github/PULL_REQUEST_TEMPLATE.md)"
    elif ! has_text "$(printf '%s\n' "$VISIBLE" | release_notes_section "$heading")"; then
        problem "\`## $heading\` is empty"
    fi
done

# The issue the pull request closes.
keywords='(close[sd]?|fix(e[sd])?|resolve[sd]?)'
# Only a line that starts with the keyword, the form of the template's
# `Closes #` line, is held to a number: prose may quote `Closes #<n>`.
if printf '%s\n' "$CODELESS" | grep -Eiq "^[[:space:]]*$keywords[[:space:]]+#([^0-9]|\$)"; then
    problem "\`Closes #\` has no issue number: add it or delete the line"
elif ! printf '%s\n' "$CODELESS" | grep -Eiq "(^|[^[:alnum:]_])$keywords[[:space:]]+#[0-9]+"; then
    echo "check-pr: note: the description closes no issue; the release notes will list the pull request under Internal" >&2
elif [ -n "$RANGE" ]; then
    # The commits of a pull request that closes an issue (AGENTS.md, *Issue
    # tracker*). Merge commits are left out by their parent count.
    if commits=$(git log --no-merges --format=%H "$RANGE" -- 2>/dev/null); then
        for sha in $commits; do
            if ! git log -1 --format=%B "$sha" | grep -Eq '^Closes #[0-9]+'; then
                problem "commit $(git log -1 --format='%h "%s"' "$sha") has no \`Closes #<n>\` line: add it to the message, by rewording or squashing the commit (AGENTS.md, *Issue tracker*)"
            fi
        done
    else
        problem "cannot read the commits of $RANGE: the full history is needed (in CI \`fetch-depth: 0\`; locally \`git fetch origin\`)"
    fi
fi

# Release-PR mode: the issues a release pull request closes are listed under
# `## Closes`, so a closing keyword anywhere else is a problem.
if [ -n "$RELEASE_PR" ]; then
    outside=$(printf '%s\n' "$CODELESS" | awk '
        $0 == "## Closes" { inside = 1; next }
        inside && (/^## / || /^---[[:space:]]*$/) { inside = 0 }
        !inside { print }')
    if printf '%s\n' "$outside" | grep -Eiq "(^|[^[:alnum:]_])$keywords[[:space:]]+#[0-9]+"; then
        problem "a release pull request lists the issues it closes only under \`## Closes\`: move every \`Closes #<n>\` there, or reword a mention to \"see #<n>\""
    fi
fi

# Deployment prerequisites, as release.sh reads them.
for heading in "$BEFORE" "$AFTER"; do
    if [ "$(count_heading "$heading" "$BODY")" -gt "$(count_heading "$heading" "$VISIBLE")" ]; then
        problem "\`## $heading\` sits inside an HTML comment, but release.sh still reads it: uncomment the section or delete it"
    elif [ "$(count_heading "$heading" "$VISIBLE")" -gt 0 ] &&
        ! has_text "$(printf '%s\n' "$VISIBLE" | release_notes_section "$heading")"; then
        problem "\`## $heading\` is empty: list the steps or delete the heading"
    fi
done

# Rollback: a deployment step needs a way back.
if [ "$(count_heading "$BEFORE" "$VISIBLE")" -gt 0 ] || [ "$(count_heading "$AFTER" "$VISIBLE")" -gt 0 ]; then
    if [ "$(count_heading Rollback "$VISIBLE")" -eq 0 ]; then
        problem "\`## Rollback\` is missing: the description has deployment steps, so add one line per rollback step, or \`None: one-way door\`"
    elif ! has_text "$(printf '%s\n' "$VISIBLE" | release_notes_section Rollback)"; then
        problem "\`## Rollback\` is empty: add one line per rollback step, or \`None: one-way door\`"
    fi
fi

misspelt=$(printf '%s\n' "$VISIBLE" | grep -E '^[[:space:]]*#' | grep -vxF -e "## $BEFORE" -e "## $AFTER" || true)
if [ -n "$misspelt" ]; then
    before_key=$(printf '%s\n' "$BEFORE" | normalize_heading)
    after_key=$(printf '%s\n' "$AFTER" | normalize_heading)
    while IFS= read -r line; do
        case $(printf '%s\n' "$line" | normalize_heading) in
            # hq's headings are English; the Polish ones of the template are
            # named too, so their steps are not lost (docs/hq/releasing.md).
            "$before_key" | "przed wdrozeniem") problem "heading \"$line\" is not read by release.sh: write exactly \`## $BEFORE\`" ;;
            "$after_key" | "po wdrozeniu") problem "heading \"$line\" is not read by release.sh: write exactly \`## $AFTER\`" ;;
        esac
    done <<LINES
$misspelt
LINES
fi

if [ "$PROBLEMS" -gt 0 ]; then
    echo "check-pr: $PROBLEMS problem(s); edit the pull request and the check runs again" >&2
    exit 1
fi
echo "check-pr: ok"
