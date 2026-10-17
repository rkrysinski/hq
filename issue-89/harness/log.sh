#!/bin/sh
# log.sh EVENT: keeps the payload with the moment it came.
t=$(date +%s.%3N)
cat > /tmp/claude-1000/-Users-romankrysinski-work-repositories-hq/5be29c0e-761b-4f5f-a874-a65b830ec0ef/scratchpad/spike/log/$t-$$-$1.json
echo "$t hook $1 pid=$$ $(jq -c '{tool_name,agent_id,notification_type,mcp_server_name,mode,action,reason,source,elicitation_id}|with_entries(select(.value!=null))' /tmp/claude-1000/-Users-romankrysinski-work-repositories-hq/5be29c0e-761b-4f5f-a874-a65b830ec0ef/scratchpad/spike/log/$t-$$-$1.json 2>/dev/null)" >> /tmp/claude-1000/-Users-romankrysinski-work-repositories-hq/5be29c0e-761b-4f5f-a874-a65b830ec0ef/scratchpad/spike/events.log
