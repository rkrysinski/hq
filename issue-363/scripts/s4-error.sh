#!/bin/sh
# Check 4: a wait on h7 goes on as a task in an hq mcp whose tmux, 56 s in, fails (a wrapper first in its PATH
# that fails while a flag file exists): the task completes with the error as a tool error, the same text a
# wait called then ends with
cd "$(dirname "$0")"
W=$PWD/tmuxwrap; mkdir -p "$W"; rm -f "$W/fail"
printf '#!/bin/sh\n# tmux, failing while the file fail stands beside this script (check 4)\nif [ -e "$(dirname "$0")/fail" ]; then echo "simulated failure of tmux (check 4)" >&2; exit 1; fi\nexec /usr/bin/tmux "$@"\n' > "$W/tmux"; chmod +x "$W/tmux"
(sleep 56; touch "$W/fail"; echo "$(date -u +%H:%M:%SZ) tmux now fails for this hq mcp") &
PATH=$W:$PATH ./qac.py 'call:list:{"names":["h7"]}' 'call:wait:{"names":["h7"],"since":"$since"}' sleep:8 get 'call:wait:{"names":["h7"],"since":"$since"}' -- ./hqmcp.sh claude-code
wait; rm -f "$W/fail"
