#!/bin/sh
# Print the `## QA` section of a spec pull request, built from the files the
# implementers committed to the `qa-artifacts` branch.
#
# Usage: scripts/assemble-qa.sh [--repo <owner/name>] [--limit <chars>]
#                               [--link-only <n>]... <spec> <ticket>...
#
#   --repo           repository the links point to (default: the `origin` remote)
#   --limit          largest size of the section, in characters (default: 65536)
#   --link-only <n>  reduce the subsection of #<n> to its heading and its link
#
# The subsections are the tickets in ascending order, then the review fixes of
# the spec (`issue-<spec>/qa-section.md`) when that file exists. Each one is
# `issue-<n>/qa-section.md` with its `#### Outputs` block replaced by a link to
# the file, pinned to the commit of `qa-artifacts` on origin, which the script
# fetches itself. Every `https://github.com/<repo>/blob/<ref>/<path>` link of
# the result is checked: <ref> is a full commit sha, and <path> exists at that
# commit. Links of other forms are not checked. The full build block is not
# part of the output.
#
# Exits 0 with the section on stdout; 1 with the problems on stderr and nothing
# on stdout; 2 on usage errors, with this text on stderr.
#
# See docs/agents/qa-evidence.md, step 5.
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
. "$SCRIPT_DIR/lib/repo-from-url.sh"

usage() {
    [ $# -eq 0 ] || printf 'assemble-qa: %s\n' "$*" >&2
    sed -n '2,24p' "$0" | sed 's/^# \{0,1\}//' >&2
    exit 2
}
die() { printf 'assemble-qa: %s\n' "$*" >&2; exit 1; }

is_number() {
    case "$1" in
        ''|0*|*[!0-9]*) return 1 ;;
    esac
}
issue_number() {
    is_number "$1" || usage "'$1' is not an issue number"
    printf '%s' "$1"
}
# Whether the space-separated list $1 has the word $2.
contains() {
    case " $1 " in
        *" $2 "*) return 0 ;;
    esac
    return 1
}

REPO=""
LIMIT=65536
LINK_ONLY=""
NUMBERS=""
while [ $# -gt 0 ]; do
    case "$1" in
        --repo) [ $# -ge 2 ] || usage; REPO=$2; shift 2 ;;
        --limit) [ $# -ge 2 ] || usage; LIMIT=$2; shift 2 ;;
        --link-only) [ $# -ge 2 ] || usage; LINK_ONLY="$LINK_ONLY $(issue_number "$2")" || exit 2; shift 2 ;;
        -*) usage "unknown option '$1'" ;;
        *) NUMBERS="$NUMBERS $(issue_number "$1")" || exit 2; shift ;;
    esac
done

is_number "$LIMIT" || usage "--limit must be a positive integer, got '$LIMIT'"
# Every word of NUMBERS is a number by now, so the expansion splits nothing else.
set -- $NUMBERS
[ $# -ge 2 ] || usage
SPEC=$1
shift
for number in $LINK_ONLY; do
    contains "$SPEC $*" "$number" || usage "--link-only $number: #$number is not among the arguments"
done

if [ -z "$REPO" ]; then
    origin=$(git remote get-url origin 2>/dev/null) || die "no 'origin' remote; pass --repo <owner/name>"
    REPO=$(repo_from_url "$origin")
fi
case "$REPO" in
    */*/*|/*|*/|*[!A-Za-z0-9._/-]*) usage "the repository must be owner/name, got '$REPO'; pass --repo <owner/name>" ;;
    */*) ;;
    *) usage "the repository must be owner/name, got '$REPO'; pass --repo <owner/name>" ;;
esac

# FETCH_HEAD, not origin/qa-artifacts: a single-branch clone has no such ref.
git fetch -q origin qa-artifacts 2>/dev/null || die "could not fetch 'qa-artifacts' from origin"
qa_sha=$(git rev-parse --verify -q 'FETCH_HEAD^{commit}') || die "'qa-artifacts' does not resolve after the fetch"

TMP=$(mktemp -d) || die "could not create a temporary directory"
trap 'rm -rf "$TMP"' EXIT
: > "$TMP/problems"
: > "$TMP/sizes"
problem() { printf 'assemble-qa: %s\n' "$*" >> "$TMP/problems"; }

# The tickets in ascending order; the review fixes of the spec come last, and
# only when they were published, whether or not the spec is among the tickets.
SECTIONS=""
for n in $(printf '%s\n' "$@" | sort -n -u); do
    [ "$n" = "$SPEC" ] || SECTIONS="$SECTIONS $n"
done
if git cat-file -e "$qa_sha:issue-$SPEC/qa-section.md" 2>/dev/null; then
    SECTIONS="$SECTIONS $SPEC"
fi

# The cut: the `#### Outputs` heading up to the next `#### ` heading becomes
# the link. Blank lines at the end are dropped, so one blank line separates
# subsections.
cut_outputs() {
    awk -v link="$1" '
        function emit(line) { while (blank > 0) { print ""; blank-- } print line }
        /^#### / { cut = 0 }
        /^#### Outputs[ \t]*$/ { cut = 1; seen = 1; emit(link); blank = 1 }
        cut { next }
        /^$/ { blank++; next }
        { emit($0) }
        END { if (!seen) { blank = 1; emit(link) } }
    '
}

printf '## QA\n' > "$TMP/out"
for n in $SECTIONS; do
    file="issue-$n/qa-section.md"
    link="[Commands and outputs of #$n](https://github.com/$REPO/blob/$qa_sha/$file)"
    if ! git show "$qa_sha:$file" > "$TMP/raw" 2>/dev/null; then
        problem "#$n: $file is not on qa-artifacts ($qa_sha)"
        continue
    fi
    if ! awk -v start="### #$n " 'NR == 1 && index($0, start) == 1 && length($0) > length(start) { ok = 1 } END { exit !ok }' "$TMP/raw"; then
        problem "#$n: the first line of $file is not '### #$n <title>'"
        continue
    fi

    if contains "$LINK_ONLY" "$n"; then
        { sed -n 1p "$TMP/raw"; printf '\n%s\n' "$link"; } > "$TMP/part"
    else
        cut_outputs "$link" < "$TMP/raw" > "$TMP/part"
    fi

    # Every blob link of the printed text into the repository: `<url>`, a tab, then
    # `<ref>/<path>` without its query and fragment, the path percent-decoded.
    # LC_ALL=C: a decoded byte is printed as that byte, not as a character.
    LC_ALL=C awk -v prefix="https://github.com/$REPO/blob/" '
        function hex(c) { return index("0123456789abcdef", tolower(c)) - 1 }
        function decode(text,    out, i, c) {
            out = ""
            for (i = 1; i <= length(text); i++) {
                c = substr(text, i, 1)
                if (c == "%" && substr(text, i + 1, 2) ~ /^[0-9A-Fa-f][0-9A-Fa-f]$/) {
                    c = sprintf("%c", hex(substr(text, i + 1, 1)) * 16 + hex(substr(text, i + 2, 1)))
                    i += 2
                }
                out = out c
            }
            return out
        }
        BEGIN { prefix = tolower(prefix) }
        {
            line = $0
            while ((at = index(tolower(line), prefix)) > 0) {
                line = substr(line, at)
                match(line, /^[^ \t)>"<`]+/)
                url = substr(line, 1, RLENGTH)
                line = substr(line, RLENGTH + 1)
                target = substr(url, length(prefix) + 1)
                sub(/[?#].*$/, "", target)
                print url "\t" decode(target)
            }
        }
    ' "$TMP/part" > "$TMP/links"
    while IFS='	' read -r url target; do
        ref=${target%%/*}
        path=${target#*/}
        case "$ref" in
            *[!0-9a-fA-F]*) problem "#$n: $url: not pinned to a commit"; continue ;;
        esac
        # A full sha only (40 or 64 digits): a shorter hex ref can be a branch name.
        hex=$(printf '%s' "$ref" | wc -c)
        if [ "$hex" -ne 40 ] && [ "$hex" -ne 64 ]; then
            problem "#$n: $url: not pinned to a commit"
        elif ! git cat-file -e "$ref:$path" 2>/dev/null &&
             ! git cat-file -e "$ref:$(printf '%s' "$path" | sed 's/[.,;:!?]*$//')" 2>/dev/null; then
            # The second try: GitHub leaves the punctuation after a bare link out of it.
            problem "#$n: $url: no such file at that commit"
        fi
    done < "$TMP/links"

    printf '\n' >> "$TMP/out"
    cat "$TMP/part" >> "$TMP/out"
    echo "$(($(wc -m < "$TMP/part"))) #$n" >> "$TMP/sizes"
done

if [ -s "$TMP/problems" ]; then
    cat "$TMP/problems" >&2
    exit 1
fi

# `wc -m` counts bytes in a locale that is not UTF-8, which only fails early.
total=$(($(wc -m < "$TMP/out")))
if [ "$total" -gt "$LIMIT" ]; then
    {
        echo "assemble-qa: the section is $total characters, the limit is $LIMIT; the subsections, largest first:"
        sort -rn "$TMP/sizes" | while read -r size name; do echo "assemble-qa:   $name: $size"; done
        echo "assemble-qa: re-run with --link-only <n> for the largest ones, until it fits"
    } >&2
    exit 1
fi

cat "$TMP/out"
