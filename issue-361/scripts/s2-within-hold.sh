#!/bin/sh
# Check 2: agent e2 (working on a slow job) finishes 20 s into a declaring wait: the call returns it, no task
./typein.sh hq-dev-t361 e2 finish 21 &
./qac.py 'call:list:{"names":["e2"]}' 'call:wait:{"names":["e2"],"since":"$since"}' -- ./hqmcp.sh claude-code
wait
