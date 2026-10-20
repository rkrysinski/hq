#!/bin/sh
# log.sh EVENT: keeps the payload with the moment it came.
t=$(date +%s.%3N)
cat > /tmp/claude-1000/-home-dev-work-repositories-hq/0000001d-0000-4000-8000-00000000001d/scratchpad/spike/log/$t-$$-$1.json
echo "$t hook $1 pid=$$ $(jq -c '{tool_name,agent_id,notification_type,mcp_server_name,mode,action,reason,source,elicitation_id}|with_entries(select(.value!=null))' /tmp/claude-1000/-home-dev-work-repositories-hq/0000001d-0000-4000-8000-00000000001d/scratchpad/spike/log/$t-$$-$1.json 2>/dev/null)" >> /tmp/claude-1000/-home-dev-work-repositories-hq/0000001d-0000-4000-8000-00000000001d/scratchpad/spike/events.log
