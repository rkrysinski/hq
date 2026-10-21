#!/bin/sh
# Stand-in for the sbx image's claude-approve after sbx-image #4: nothing for AskUserQuestion, allow for the rest.
# With old-approve present, the one before it: a bare allow for every request.
input=$(cat)
printf '%s\n' "$input" >>$QA/log/approve.log
[ ! -f $QA/old-approve ] && [ "$(printf '%s' "$input" | jq -r '.tool_name // empty')" = AskUserQuestion ] && exit 0
printf '%s\n' '{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"allow"}}}'
