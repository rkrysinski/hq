#!/bin/sh
# Usage: corpus.sh <old-lib> <new-lib> <prs.json> - old vs new release_notes_section on every PR body
d=$(mktemp -d); same=0 diff=0 footer=0
for n in $(jq -r '.[].number' "$3"); do
    jq -r --argjson n "$n" '.[] | select(.number == $n) | .body // ""' "$3" > "$d/body"
    for h in "Przed wdrożeniem" "Po wdrożeniu"; do
        old=$(sh -c '. "$1"; release_notes_section "$2"' _ "$1" "$h" < "$d/body")
        new=$(sh -c '. "$1"; release_notes_section "$2"' _ "$2" "$h" < "$d/body")
        if [ "$old" = "$new" ]; then same=$((same+1))
        elif printf '%s' "$old" | grep -q 'Generated with \[Claude Code\]'; then footer=$((footer+1)); echo "#$n $h: footer dropped"
        else diff=$((diff+1)); echo "#$n $h: UNEXPECTED DIFFERENCE"; fi
    done
done
rm -rf "$d"; echo "identical $same, footer-only differences $footer, unexpected $diff"
