#!/bin/sh
# Stand-in for the new image's claude-approve (KRISPOL-Sp-z-o-o/sbx-image#5): nothing for AskUserQuestion, allow for the rest.
input=$(cat)
[ -f /tmp/claude-1000/-Users-romankrysinski-work-repositories-hq/5efb3b68-4efc-4f9e-bb3d-e59adfeea699/scratchpad/qa/ctl-skip-all ] && exit 0
[ "$(printf '%s' "$input" | jq -r '.tool_name // empty')" = AskUserQuestion ] && exit 0
printf '%s\n' '{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"allow"}}}'
