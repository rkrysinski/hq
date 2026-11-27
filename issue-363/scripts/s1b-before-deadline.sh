#!/bin/sh
# Check 1b: a wait on h2 given a timeout of 300 s goes on as a task; h2 is let finish 72 s in: the task
# completes with h2, as one with no timeout would
cd "$(dirname "$0")"
./typein.sh hq-dev-t363 h2 finish 72 &
./qac.py 'call:list:{"names":["h2"]}' 'call:wait:{"names":["h2"],"since":"$since","timeout":300}' poll -- ./hqmcp.sh claude-code
wait
