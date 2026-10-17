#!/bin/sh
# approve.sh: the template's claude-approve, with knobs: ctl/approve-delay (seconds before
# answering), ctl/approve-skip-ask (print nothing for AskUserQuestion).
p=$(cat)
echo "$(date +%s.%N) approve-start pid=$$" >> /tmp/claude-1000/-Users-romankrysinski-work-repositories-hq/5be29c0e-761b-4f5f-a874-a65b830ec0ef/scratchpad/spike/events.log
[ -f /tmp/claude-1000/-Users-romankrysinski-work-repositories-hq/5be29c0e-761b-4f5f-a874-a65b830ec0ef/scratchpad/spike/ctl/approve-delay ] && sleep $(cat /tmp/claude-1000/-Users-romankrysinski-work-repositories-hq/5be29c0e-761b-4f5f-a874-a65b830ec0ef/scratchpad/spike/ctl/approve-delay)
if [ -f /tmp/claude-1000/-Users-romankrysinski-work-repositories-hq/5be29c0e-761b-4f5f-a874-a65b830ec0ef/scratchpad/spike/ctl/approve-skip-ask ] && [ "$(printf '%s' "$p" | jq -r .tool_name)" = AskUserQuestion ]; then
	echo "$(date +%s.%N) approve-skip pid=$$" >> /tmp/claude-1000/-Users-romankrysinski-work-repositories-hq/5be29c0e-761b-4f5f-a874-a65b830ec0ef/scratchpad/spike/events.log
	exit 0
fi
echo "$(date +%s.%N) approve-allow pid=$$" >> /tmp/claude-1000/-Users-romankrysinski-work-repositories-hq/5be29c0e-761b-4f5f-a874-a65b830ec0ef/scratchpad/spike/events.log
printf '%s\n' '{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"allow"}}}'
