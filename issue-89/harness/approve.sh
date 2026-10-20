#!/bin/sh
# approve.sh: the template's claude-approve, with knobs: ctl/approve-delay (seconds before
# answering), ctl/approve-skip-ask (print nothing for AskUserQuestion).
p=$(cat)
echo "$(date +%s.%N) approve-start pid=$$" >> /tmp/claude-1000/-home-dev-work-repositories-hq/0000001d-0000-4000-8000-00000000001d/scratchpad/spike/events.log
[ -f /tmp/claude-1000/-home-dev-work-repositories-hq/0000001d-0000-4000-8000-00000000001d/scratchpad/spike/ctl/approve-delay ] && sleep $(cat /tmp/claude-1000/-home-dev-work-repositories-hq/0000001d-0000-4000-8000-00000000001d/scratchpad/spike/ctl/approve-delay)
if [ -f /tmp/claude-1000/-home-dev-work-repositories-hq/0000001d-0000-4000-8000-00000000001d/scratchpad/spike/ctl/approve-skip-ask ] && [ "$(printf '%s' "$p" | jq -r .tool_name)" = AskUserQuestion ]; then
	echo "$(date +%s.%N) approve-skip pid=$$" >> /tmp/claude-1000/-home-dev-work-repositories-hq/0000001d-0000-4000-8000-00000000001d/scratchpad/spike/events.log
	exit 0
fi
echo "$(date +%s.%N) approve-allow pid=$$" >> /tmp/claude-1000/-home-dev-work-repositories-hq/0000001d-0000-4000-8000-00000000001d/scratchpad/spike/events.log
printf '%s\n' '{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"allow"}}}'
