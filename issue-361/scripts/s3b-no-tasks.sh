#!/bin/sh
# Check 3: a wait on e4 (working) whose requests declare no tasks goes on past 50 s and returns e4 when it finishes, 80 s in
./typein.sh hq-dev-t361 e4 finish 82 &
./qac.py --no-tasks 'call:list:{"names":["e4"]}' 'call:wait:{"names":["e4"],"since":"$since"}' -- ./hqmcp.sh claude-code
wait
