#!/bin/sh
# Stand-in for the new image's claude-approve (<org>/sbx-image#5): nothing for AskUserQuestion, allow for the rest.
input=$(cat)
[ -f /tmp/claude-1000/-home-dev-work-repositories-hq/0000001f-0000-4000-8000-00000000001f/scratchpad/qa/ctl-skip-all ] && exit 0
[ "$(printf '%s' "$input" | jq -r '.tool_name // empty')" = AskUserQuestion ] && exit 0
printf '%s\n' '{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"allow"}}}'
