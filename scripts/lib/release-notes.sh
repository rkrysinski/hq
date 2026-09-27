#!/bin/sh
# Work out what went into a release: the pull requests merged in a commit range
# and the issues they closed. Sourced by scripts/release.sh (to write the notes)
# and scripts/cut-release.sh (to write the tag message), so a tag and its release
# always describe the same set of changes.
#
# Needs gh and jq.
#
# Usage after sourcing:
#   release_notes_collect <repo> <from-ref> <to-ref> <workdir>
#       an empty <from-ref> means "from the first commit": the first release
#   release_notes_issue_labels <repo> <workdir>
#   release_notes_prerequisites <workdir>
#   release_notes_section <heading> < body
#
# Errors are reported on stderr and signalled by a non-zero return, so callers
# can end with their own `|| die "..."`.

RELEASE_NOTES_PROG=${RELEASE_NOTES_PROG:-$(basename "$0" .sh)}

# The headings a pull request uses for its deployment prerequisites. Polish,
# like the release notes they end up in (docs/releasing.md).
RELEASE_NOTES_BEFORE_HEADING=${RELEASE_NOTES_BEFORE_HEADING:-Przed wdrożeniem}
RELEASE_NOTES_AFTER_HEADING=${RELEASE_NOTES_AFTER_HEADING:-Po wdrożeniu}

# Reports the reason and always returns 1, so callers can write
# `... || { _release_notes_fail "..."; return 1; }` and mean it.
_release_notes_fail() {
    echo "$RELEASE_NOTES_PROG: $*" >&2
    return 1
}

release_notes_collect() {
    # Fills <workdir> with:
    #   prs.json       raw gh output for every merged pull request
    #   prs.tsv        "<number>\t<title>" for the ones in range, ascending
    #   issue-numbers  the issues those pull requests close, ascending, unique
    #   internal.tsv   "<number>\t<title>" for pull requests closing no issue
    _rn_repo=$1
    _rn_from=$2
    _rn_to=$3
    _rn_dir=$4

    if [ -n "$_rn_from" ]; then
        _rn_range="$_rn_from..$_rn_to"
    else
        _rn_range=$_rn_to
    fi
    git rev-list "$_rn_range" > "$_rn_dir/range-shas" \
        || { _release_notes_fail "cannot walk $_rn_range"; return 1; }
    [ -s "$_rn_dir/range-shas" ] \
        || { _release_notes_fail "$_rn_range contains no commits"; return 1; }

    gh pr list --repo "$_rn_repo" --state merged --limit 200 \
        --json number,title,body,mergeCommit < /dev/null > "$_rn_dir/prs.json" \
        || { _release_notes_fail "could not list merged pull requests for $_rn_repo"; return 1; }

    # A merged pull request belongs to the range when the commit it produced on
    # the target branch is inside it - true for merge commits, squashes and
    # rebases alike, and false for anything merged into another branch.
    jq -r --rawfile shas "$_rn_dir/range-shas" '
        ($shas | split("\n") | map(select(length > 0))) as $range
        | map(select(.mergeCommit != null and (.mergeCommit.oid as $o | $range | index($o))))
        | sort_by(.number)
        | .[] | "\(.number)\t\(.title)"
    ' "$_rn_dir/prs.json" > "$_rn_dir/prs.tsv" \
        || { _release_notes_fail "could not read the pull request list"; return 1; }

    [ -s "$_rn_dir/prs.tsv" ] \
        || { _release_notes_fail "no merged pull requests found in $_rn_range"; return 1; }

    : > "$_rn_dir/issue-numbers"
    : > "$_rn_dir/internal.tsv"
    while IFS="$(printf '\t')" read -r _rn_pr _rn_title; do
        _rn_body=$(jq -r --argjson n "$_rn_pr" '.[] | select(.number == $n) | .body // ""' "$_rn_dir/prs.json")
        # GitHub's own closing keywords, case-insensitive. Only a bare `#<n>` is
        # matched, so an `owner/repo#n` reference can never drag another
        # repository's issues into this release.
        _rn_closes=$(printf '%s\n' "$_rn_body" \
            | grep -oiE '(clos(e|es|ed)?|fix(e[sd])?|fixes|resolv(e|es|ed)?)[[:space:]]+#[0-9]+' \
            | grep -oE '[0-9]+' | sort -un) || _rn_closes=""
        if [ -n "$_rn_closes" ]; then
            echo "$_rn_closes" >> "$_rn_dir/issue-numbers"
        else
            printf '%s\t%s\n' "$_rn_pr" "$_rn_title" >> "$_rn_dir/internal.tsv"
        fi
    done < "$_rn_dir/prs.tsv"

    sort -un "$_rn_dir/issue-numbers" -o "$_rn_dir/issue-numbers"
}

release_notes_issue_labels() {
    # Reads <workdir>/issue-numbers and writes <workdir>/issues.tsv as
    # "<number>\t<title>\t<labels-csv>", skipping numbers that turn out to be
    # pull requests rather than issues.
    _rn_repo=$1
    _rn_dir=$2

    : > "$_rn_dir/issues.tsv"
    while read -r _rn_issue; do
        [ -n "$_rn_issue" ] || continue
        # </dev/null: gh would otherwise read this loop's stdin and eat the list.
        if ! gh issue view "$_rn_issue" --repo "$_rn_repo" --json number,title,labels \
            < /dev/null > "$_rn_dir/issue.json" 2>/dev/null; then
            echo "$RELEASE_NOTES_PROG: warning: #$_rn_issue is referenced as closed but is not an issue in $_rn_repo - skipping" >&2
            continue
        fi
        printf '%s\t%s\t%s\n' \
            "$_rn_issue" \
            "$(jq -r '.title' "$_rn_dir/issue.json")" \
            "$(jq -r '[.labels[].name] | join(",")' "$_rn_dir/issue.json")" \
            >> "$_rn_dir/issues.tsv"
    done < "$_rn_dir/issue-numbers"
}

release_notes_section() {
    # Prints the body of the `## <heading>` section read from stdin: the lines
    # after that exact heading up to the next `## ` heading, with surrounding
    # blank lines trimmed. Prints nothing when the section is missing or empty.
    # Carriage returns (bodies edited in the browser) are ignored.
    tr -d '\r' | awk -v heading="## $1" '
        $0 == heading { inside = 1; next }
        inside && /^## / { inside = 0 }
        inside { lines[++n] = $0 }
        END {
            first = 1; while (first <= n && lines[first] ~ /^[[:space:]]*$/) first++
            last = n; while (last >= first && lines[last] ~ /^[[:space:]]*$/) last--
            for (i = first; i <= last; i++) print lines[i]
        }
    '
}

release_notes_prerequisites() {
    # Reads <workdir>/prs.tsv and prs.json and writes <workdir>/before.md and
    # <workdir>/after.md: every pull request's `## Przed wdrożeniem` and
    # `## Po wdrożeniu` section (RELEASE_NOTES_BEFORE_HEADING and
    # RELEASE_NOTES_AFTER_HEADING), in pull request order, each block headed by
    # the pull request it comes from. Empty files when no pull request has one.
    _rn_dir=$1

    : > "$_rn_dir/before.md"
    : > "$_rn_dir/after.md"
    while IFS="$(printf '\t')" read -r _rn_pr _rn_title; do
        [ -n "$_rn_pr" ] || continue
        jq -r --argjson n "$_rn_pr" '.[] | select(.number == $n) | .body // ""' "$_rn_dir/prs.json" > "$_rn_dir/pr-body"
        for _rn_pair in "$RELEASE_NOTES_BEFORE_HEADING:before.md" "$RELEASE_NOTES_AFTER_HEADING:after.md"; do
            _rn_text=$(release_notes_section "${_rn_pair%%:*}" < "$_rn_dir/pr-body")
            [ -n "$_rn_text" ] || continue
            printf '**%s (#%s)**\n\n%s\n\n' "$_rn_title" "$_rn_pr" "$_rn_text" >> "$_rn_dir/${_rn_pair#*:}"
        done
    done < "$_rn_dir/prs.tsv"
}
