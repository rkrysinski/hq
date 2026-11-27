#!/bin/sh
# Check 2: a wait on h3 and h4 goes on as a task; both are let finish at the same moment, 60 s in: one result
# of the task with both
cd "$(dirname "$0")"
./typein.sh hq-dev-t363 h3 finish 60 & ./typein.sh hq-dev-t363 h4 finish 60 &
./qac.py 'call:list:{"names":["h3","h4"]}' 'call:wait:{"names":["h3","h4"],"since":"$since"}' poll -- ./hqmcp.sh claude-code
wait
