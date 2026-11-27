#!/bin/sh
# Check 4 of #360: no text says the gateway gives a tool call 60 s.
# Run from the issue's worktree; $1 is the base to compare with.
base=$1
places="*.go README.md docs/design/hq.md docs/requirements/spec.md GLOSSARY.md"
for phrase in "which gives a tool call 60 s, as Claude Desktop does" \
    "the gateway gives a tool call 60 s" \
    "which give up on a tool call after 60 s" \
    "50 s for Claude Desktop and \`sbx\`'s gateway" \
    "so \`wait\` is capped at 50 s" \
    "tool call 60 s: wait is capped as for Claude Desktop"; do
    b=$(git grep -h -F "$phrase" "$base" -- $places | wc -l)
    a=$(git grep -h -F "$phrase" HEAD -- $places | wc -l)
    echo "$b -> $a  \"$phrase\""
done
echo
printf '%s\n' '$ git grep -n -E "\b60 ?s\b" -- *.go README.md docs/design/hq.md docs/requirements/spec.md GLOSSARY.md | grep -i -E "gateway|sbx|sandbox"'
git grep -n -E "\b60 ?s\b" HEAD -- $places | sed 's/^HEAD://' | grep -i -E "gateway|sbx|sandbox" |
    sed -E 's/^([^:]*:[0-9]+:).*(.{90}60 s.{60}).*$/\1 ...\2.../'
