#!/bin/sh
# Check 6: one hq mcp claude-code hands e5's wait off as a task and ends; another, started afresh, answers for it:
# working, then completed once e5 finishes
QAC_TASK_FILE=s6.task ./qac.py 'call:list:{"names":["e5"]}' 'call:wait:{"names":["e5"],"since":"$since"}' -- ./hqmcp.sh claude-code
echo "== the first hq mcp has ended; a second one is asked for $(cat s6.task)"
./typein.sh hq-dev-t361 e5 finish 12 &
QAC_TASK=$(cat s6.task) ./qac.py get poll -- ./hqmcp.sh claude-code
wait
