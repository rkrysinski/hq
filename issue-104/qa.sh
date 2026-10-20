#!/bin/sh
# Usage: qa.sh <release-notes.sh>  - runs the #104 checks against the sourced library
. "$1"
F='🤖 Generated with [Claude Code](https://claude.com/claude-code)'
pass=0 fail=0
check() { # name expected actual
    if [ "$2" = "$3" ]; then echo "PASS $1"; pass=$((pass+1)); else echo "FAIL $1"; echo "  expected: $(printf '%s' "$2" | od -c | head -3)"; echo "  actual:   $(printf '%s' "$3")"; fail=$((fail+1)); fi
}
two=$(printf -- '- one\n- two')

out=$(printf '## Summary\nx\n\n## Po wdrożeniu\n- one\n- two\n\n%s\n' "$F" | release_notes_section "Po wdrożeniu")
check "1 Po wdrożeniu + footer -> two bullets" "$two" "$out"

out=$(printf '## Summary\nx\n\n## Przed wdrożeniem\n- one\n- two\n\n%s\n' "$F" | release_notes_section "Przed wdrożeniem")
check "2 Przed wdrożeniem + footer -> two bullets" "$two" "$out"

out=$(printf '## Po wdrożeniu\n- one\n- two\n\n%s\n\n## Other\n- three\n' "$F" | release_notes_section "Po wdrożeniu")
check "3 footer then ## Other -> two bullets, no Other" "$two" "$out"

out=$(printf '## Po wdrożeniu\n\n%s\n' "$F" | release_notes_section "Po wdrożeniu")
check "4a section holding only the footer -> empty" "" "$out"

out=$(printf '## Po wdrożeniu\n- one\n- two\n\n---\n\n%s\n' "$F" | release_notes_section "Po wdrożeniu")
check "5 rule + footer -> rule trimmed too" "$two" "$out"

out=$(printf '## Po wdrożeniu\r\n- one\r\n- two\r\n\r\n%s\r\n' "$F" | release_notes_section "Po wdrożeniu")
check "6 CRLF body + footer -> two bullets" "$two" "$out"

out=$(printf '## Po wdrożeniu\n- one\n- two\n\nGenerated with [Claude Code](https://example.invalid/x)\n' | release_notes_section "Po wdrożeniu")
check "7 footer without emoji, other URL -> dropped" "$two" "$out"

out=$(printf '## Po wdrożeniu\n- one\n\n---\n- two\n' | release_notes_section "Po wdrożeniu")
check "8 no footer: inner rule kept" "$(printf -- '- one\n\n---\n- two')" "$out"

out=$(printf '## Po wdrożeniu\n- one\n- two\n\n---\n' | release_notes_section "Po wdrożeniu")
check "9 no footer: trailing rule kept as before" "$(printf -- '- one\n- two\n\n---')" "$out"

out=$(printf '## Po wdrożeniu extra\n- one\n' | release_notes_section "Po wdrożeniu")
check "10 heading must match exactly" "" "$out"

out=$(printf '## Po wdrożeniu\n- one\n%s\n- two\n---\n' "$F" | release_notes_section "Po wdrożeniu")
check "12 footer mid-section: author's own trailing rule kept" "$(printf -- '- one\n- two\n---')" "$out"

out=$(printf '## Po wdrożeniu\n- one\n- two\n\n---\n---\n\n%s\n' "$F" | release_notes_section "Po wdrożeniu")
check "13 two rules + footer -> both trimmed" "$two" "$out"

out=$(printf '## Po wdrożeniu\n---\n\n%s\n' "$F" | release_notes_section "Po wdrożeniu")
check "14 rule + footer only -> empty" "" "$out"

# 4b / 11: through release_notes_prerequisites, byte for byte
w=$(mktemp -d)
printf '1\tOnly footer\n2\tWith footer\n' > "$w/prs.tsv"
jq -n --arg f "$F" '[
  {number: 1, body: ("## Summary\nx\n\n## Po wdrożeniu\n\n" + $f + "\n")},
  {number: 2, body: ("## Przed wdrożeniem\n- a\n\n## Po wdrożeniu\n- one\n- two\n\n" + $f + "\n")}
]' > "$w/prs.json"
release_notes_prerequisites "$w"; cp "$w/before.md" "$w/before.with"; cp "$w/after.md" "$w/after.with"
jq -n '[
  {number: 1, body: "## Summary\nx\n"},
  {number: 2, body: "## Przed wdrożeniem\n- a\n\n## Po wdrożeniu\n- one\n- two\n"}
]' > "$w/prs.json"
release_notes_prerequisites "$w"
check "4b only-footer PR gets no block" "" "$(grep -c '#1)' "$w/after.with" | grep -v '^0$')"
check "11a before.md identical with/without footer" "$(od -c "$w/before.md")" "$(od -c "$w/before.with")"
check "11b after.md identical with/without footer" "$(od -c "$w/after.md")" "$(od -c "$w/after.with")"
echo "--- after.md with footer:"; cat "$w/after.with"
rm -rf "$w"
echo "passed $pass, failed $fail"
[ "$fail" -eq 0 ]
