#!/bin/sh
# Check 3: a wait on h5 goes on as a task, and h5 is killed 60 s in: the task completes with h5 ended, its
# repository and sandbox from the trace of kills
cd "$(dirname "$0")"
(sleep 60; ./hq.sh kill h5 -y) &
./qac.py 'call:list:{"names":["h5"]}' 'call:wait:{"names":["h5"],"since":"$since"}' poll -- ./hqmcp.sh claude-code
wait
echo "== the trace of kills"; ./hq.sh killed h5
