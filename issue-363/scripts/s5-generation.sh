#!/bin/sh
# Check 5: a wait on h8 goes on as a task; 56 s in, the hq on disk becomes another generation (a program that
# answers __generation with 999 is renamed over the binary hq mcp runs): the task completes with hq's line as
# a tool error, a tool call fails with it, a cancel is acknowledged
cd "$(dirname "$0")"
bin=$(LOCAL_DEV_ID=t363 <worktree>/scripts/local-dev.sh --stub-env)/bin/hq
(sleep 56; printf '#!/bin/sh\n# stands for an hq of another generation\necho 999\n' > "$bin.new" && chmod +x "$bin.new" && mv -f "$bin.new" "$bin" && echo "$(date -u +%H:%M:%SZ) the hq on disk now answers __generation with 999") &
./qac.py 'call:list:{"names":["h8"]}' 'call:wait:{"names":["h8"],"since":"$since"}' sleep:8 get 'call:list:{"names":["h8"]}' cancel -- ./hqmcp.sh claude-code
wait
