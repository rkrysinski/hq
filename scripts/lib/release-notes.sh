#!/bin/sh
# Work out what went into a release: the pull requests merged in a commit range
# and the issues they closed. Sourced by scripts/release.sh (to write the notes)
# and scripts/cut-release.sh (to write the tag message), so a tag and its release
# always describe the same set of changes; scripts/check-pr.sh reads its
# headings and its release-branch rule too.
#
# Needs gh and jq.
#
# Usage after sourcing:
#   release_notes_collect <repo> <from-ref> <to-ref> <workdir>
#       an empty <from-ref> means "from the first commit": the first release
#   release_notes_issue_labels <repo> <workdir>
#   release_notes_prerequisites <workdir>
#   release_notes_section <heading> < body
#   release_notes_drop_ticked < section
#   release_notes_is_spec <labels-csv>
#   release_notes_strip_comments < body
#   release_notes_strip_code < body
#   release_notes_is_release_branch <branch>
#
# Errors are reported on stderr and signalled by a non-zero return, so callers
# can end with their own `|| die "..."`.

RELEASE_NOTES_PROG=${RELEASE_NOTES_PROG:-$(basename "$0" .sh)}

# The headings a pull request uses for its deployment prerequisites. English,
# like the release notes they end up in (docs/releasing.md, docs/hq/releasing.md).
RELEASE_NOTES_BEFORE_HEADING=${RELEASE_NOTES_BEFORE_HEADING:-Before deployment}
RELEASE_NOTES_AFTER_HEADING=${RELEASE_NOTES_AFTER_HEADING:-After deployment}

# A release branch is named release-v<MAJOR>.<MINOR>.<PATCH>
# (docs/agents/issue-tracker.md); any other branch starting with `release-` is
# an ordinary one. An extended regex, read by grep -E and jq alike.
RELEASE_NOTES_BRANCH_REGEX='^release-v[0-9]+\.[0-9]+\.[0-9]+$'

# Succeeds when <branch> is a release branch.
release_notes_is_release_branch() {
    printf '%s\n' "$1" | grep -qE "$RELEASE_NOTES_BRANCH_REGEX"
}

# Reports the reason and always returns 1, so callers can write
# `... || { _release_notes_fail "..."; return 1; }` and mean it.
_release_notes_fail() {
    echo "$RELEASE_NOTES_PROG: $*" >&2
    return 1
}

release_notes_collect() {
    # Fills <workdir> with:
    #   prs.json       every merged pull request but a release's master pull
    #                  request: number, title, body, mergeCommit
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

    # Every closed pull request, page after page (`gh pr list` stops at --limit),
    # then the merged ones in the shape the rest of this file reads. Two steps,
    # because a pipe would hide a failing gh.
    #
    # The master pull request of a supervised release (head `release-vX.Y.Z`, into
    # `dev`) is merged after its tag, so its merge commit falls in the next
    # release's range: it is left out here, with the release issue and specs it
    # closes, which shipped with that tag. The tickets merged into the release
    # branch (base `release-vX.Y.Z`) stay in.
    gh api "repos/$_rn_repo/pulls?state=closed&per_page=100" --paginate \
        < /dev/null > "$_rn_dir/pulls.json" \
        || { _release_notes_fail "could not list merged pull requests for $_rn_repo"; return 1; }
    jq -s --arg release "$RELEASE_NOTES_BRANCH_REGEX" 'add // [] | map(select(.merged_at != null and ((.head.ref // "") | test($release) | not))
        | {number, title, body, mergeCommit: (if .merge_commit_sha then {oid: .merge_commit_sha} else null end)})' \
        "$_rn_dir/pulls.json" > "$_rn_dir/prs.json" \
        || { _release_notes_fail "could not read the merged pull requests of $_rn_repo"; return 1; }

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
        # GitHub's own closing keywords, case-insensitive, whole words ("discloses
        # #4" closes nothing), outside HTML comments and code (which GitHub
        # ignores too). Only a bare `#<n>` is matched, so an `owner/repo#n`
        # reference can never drag another repository's issues into this
        # release.
        _rn_closes=$(printf '%s\n' "$_rn_body" | release_notes_strip_comments | release_notes_strip_code \
            | grep -oiE '(^|[^[:alnum:]_])(clos(e|es|ed)?|fix(e[sd])?|fixes|resolv(e|es|ed)?)[[:space:]]+#[0-9]+' \
            | grep -oE '[0-9]+' | sort -un) || _rn_closes=""
        if [ -n "$_rn_closes" ]; then
            echo "$_rn_closes" >> "$_rn_dir/issue-numbers"
        else
            printf '%s\t%s\n' "$_rn_pr" "$_rn_title" >> "$_rn_dir/internal.tsv"
        fi
    done < "$_rn_dir/prs.tsv"

    sort -un "$_rn_dir/issue-numbers" -o "$_rn_dir/issue-numbers"
}

release_notes_strip_comments() {
    # What GitHub shows: the text read from stdin without <!-- ... -->, which
    # may span lines. An unclosed comment hides everything after it.
    awk '{ s = s $0 "\n" }
        END {
            while ((i = index(s, "<!--")) > 0) {
                rest = substr(s, i + 4)
                j = index(rest, "-->")
                if (j == 0) { s = substr(s, 1, i - 1); break }
                s = substr(s, 1, i - 1) substr(rest, j + 3)
            }
            printf "%s", s
        }'
}

release_notes_strip_code() {
    # The text read from stdin without its code, where GitHub links no closing
    # keyword: a fenced block (``` or ~~~, closed by a fence of the same
    # character at least as long, or else running to the end) becomes one blank
    # line, and an inline code span `_`, so no keyword before it joins a
    # reference after it. A span opens at a run of backticks and closes at the
    # next run of the same length within its block: a paragraph, list item,
    # heading or table row. A backslash-escaped or unmatched backtick is text.
    # Indented code blocks are left as they are: in a pull request they are
    # rare, and four spaces as often continue a list item. Read after
    # release_notes_strip_comments, as check-pr.sh does.
    awk '
        function run(s, i,   k) { k = 0; while (substr(s, i + k, 1) == "`") k++; return k }
        function flush(   s, out, i, n, c, m, j, k, found) {
            if (para == "") return
            s = para; out = ""; i = 1; n = length(s)
            while (i <= n) {
                c = substr(s, i, 1)
                # A backslash escapes the next character, a backslash included.
                if (c == "\\") { out = out substr(s, i, 2); i += 2; continue }
                if (c != "`") { out = out c; i++; continue }
                m = run(s, i); j = i + m; found = 0
                while (j <= n) {
                    if (substr(s, j, 1) != "`") { j++; continue }
                    k = run(s, j)
                    if (k == m) { found = 1; break }
                    j += k
                }
                if (found) { out = out "_"; i = j + m } else { out = out substr(s, i, m); i += m }
            }
            printf "%s", out
            para = ""
        }
        fence != "" { if ($0 ~ closing) fence = ""; next }
        match($0, /^[[:space:]]*(```+|~~~+)/) {
            marker = substr($0, RSTART, RLENGTH); sub(/^[[:space:]]*/, "", marker)
            # A backtick in the info string makes it a code span, not a fence.
            if (!(substr(marker, 1, 1) == "`" && index(substr($0, RSTART + RLENGTH), "`"))) {
                flush(); print ""
                fence = substr(marker, 1, 1)
                closing = "^[[:space:]]*" marker fence "*[[:space:]]*$"
                next
            }
        }
        /^[[:space:]]*$/ { flush(); print; next }
        # A heading or a table row is a block of its own line; a list item
        # starts a block its continuation lines join.
        /^[[:space:]]*(#+([[:space:]]|$)|\|)/ { flush(); para = $0 "\n"; flush(); next }
        /^[[:space:]]*([-*+]|[0-9]+[.)])([[:space:]]|$)/ { flush() }
        { para = para $0 "\n" }
        END { flush() }
    '
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
    #
    # A horizontal rule (`---`) or the attribution line an agent appends to its
    # pull request (`🤖 Generated with ...`) ends the section too: when the
    # section is the last one in the body, neither is a deployment step.
    tr -d '\r' | awk -v heading="## $1" -v footer="🤖 Generated with" '
        $0 == heading { inside = 1; next }
        inside && (/^## / || /^---[[:space:]]*$/ || index($0, footer) == 1) { inside = 0 }
        inside { lines[++n] = $0 }
        END {
            first = 1; while (first <= n && lines[first] ~ /^[[:space:]]*$/) first++
            last = n; while (last >= first && lines[last] ~ /^[[:space:]]*$/) last--
            for (i = first; i <= last; i++) print lines[i]
        }
    '
}

release_notes_drop_ticked() {
    # Prints the section read from stdin without its ticked list items
    # (`- [x]`, `* [X]`, `1. [x]`): a step done before the tag, under
    # supervision, is not a step of the deploy (docs/releasing.md). An item goes
    # with the lines indented under it, sub-items included. When no list item is
    # left, nothing is printed, prose and all. A section without a ticked item
    # is printed unchanged; otherwise runs of blank lines are squeezed to one
    # and blank lines at either end trimmed.
    awk -v bullet='^[[:space:]]*([-*+]|[0-9]+[.)])' '
        function indent(line) { match(line, /^[[:space:]]*/); return RLENGTH }
        {
            fence_line = $0 ~ /^[[:space:]]*(```|~~~)/
            # A line of a fenced code block is never an item of the list.
            item = !fence && !fence_line && $0 ~ (bullet "[[:space:]]")
            if (skip && $0 ~ /[^[:space:]]/ && indent($0) <= skip_indent) skip = 0
            if (item && $0 ~ (bullet "[[:space:]]+\\[[xX]\\]([[:space:]]|$)")) {
                skip = 1; skip_indent = indent($0); ticked++
            }
            if (fence_line) fence = !fence
            all[++total] = $0
            if (skip) next
            if (item) kept++
            lines[++n] = $0
        }
        END {
            if (!ticked) { for (i = 1; i <= total; i++) print all[i]; exit }
            if (!kept) exit
            first = 1; while (first <= n && lines[first] ~ /^[[:space:]]*$/) first++
            last = n; while (last >= first && lines[last] ~ /^[[:space:]]*$/) last--
            for (i = first; i <= last; i++) {
                blank = lines[i] ~ /^[[:space:]]*$/
                if (!(blank && previous_blank)) print lines[i]
                previous_blank = blank
            }
        }
    '
}

release_notes_is_spec() {
    # Succeeds when <labels-csv> (as in issues.tsv) carries `prd`. A spec is a
    # plan, not a change: the tickets built from it are what a release lists, in
    # its notes and in its tag message alike.
    case ",$1," in
        *,prd,*) return 0 ;;
        *) return 1 ;;
    esac
}

release_notes_prerequisites() {
    # Reads <workdir>/prs.tsv and prs.json and writes <workdir>/before.md and
    # <workdir>/after.md: every pull request's `## Before deployment` and
    # `## After deployment` section (RELEASE_NOTES_BEFORE_HEADING and
    # RELEASE_NOTES_AFTER_HEADING), in pull request order, each block headed by
    # the pull request it comes from, without the steps ticked as done
    # (release_notes_drop_ticked). Empty files when no pull request has one.
    _rn_dir=$1

    : > "$_rn_dir/before.md"
    : > "$_rn_dir/after.md"
    while IFS="$(printf '\t')" read -r _rn_pr _rn_title; do
        [ -n "$_rn_pr" ] || continue
        jq -r --argjson n "$_rn_pr" '.[] | select(.number == $n) | .body // ""' "$_rn_dir/prs.json" > "$_rn_dir/pr-body"
        for _rn_pair in "$RELEASE_NOTES_BEFORE_HEADING:before.md" "$RELEASE_NOTES_AFTER_HEADING:after.md"; do
            _rn_text=$(release_notes_section "${_rn_pair%%:*}" < "$_rn_dir/pr-body" | release_notes_drop_ticked)
            [ -n "$_rn_text" ] || continue
            printf '**%s (#%s)**\n\n%s\n\n' "$_rn_title" "$_rn_pr" "$_rn_text" >> "$_rn_dir/${_rn_pair#*:}"
        done
    done < "$_rn_dir/prs.tsv"
}
