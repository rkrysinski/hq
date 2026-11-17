#!/bin/sh
# Check 4 of #231: the hook with a PATH that has no go.
hook() { printf '$ %s\n' "$1"; printf '{"cwd":"/w","tool_input":{"command":"%s"}}' "$1" | PATH=/tmp/qa231/nogo /tmp/qa231/after/hq/scripts/claude-hooks/prepost; echo "=> hook exit $?"; echo; }
echo "PATH without go:"; echo
hook 'gh pr comment 7 --body fine'
hook 'gh issue view 231 --json title'
hook 'git log --oneline -3'
