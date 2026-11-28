#!/bin/sh
# Checks 4 and 5: a declaring send with wait of "slow job" to p1 (done, at its prompt): the slow turn ends 70 s in, so
# the call goes on as a task after 50 s and completes with turn_ended; the id's length. Then (check 5) p1 is given
# another slow job with a plain send and the completed task is asked again by another hq mcp, while that turn works
# and after it ended
cd "$(dirname "$0")"
./typein.sh hq-dev-t357r p1 finish 70 &
QAC_POLL_UNTIL=150 QAC_TASK_FILE=p1.task ./qac.py discover 'call:send:{"name":"p1","text":"slow job","wait":true}' poll -- ./hqmcp.sh claude-code
wait
echo "== the task id: $(cat p1.task)"
echo "== its length: $(tr -d '\n' < p1.task | wc -c) characters"
echo "== check 5: p1 given another slow job (plain send), then the completed task asked again"
./hq.sh send p1 "slow job again"
sleep 4
QAC_TASK=$(cat p1.task) ./qac.py discover get -- ./hqmcp.sh claude-code
./typein.sh hq-dev-t357r p1 finish
sleep 4
QAC_TASK=$(cat p1.task) ./qac.py discover get -- ./hqmcp.sh claude-code
echo "== hq ls p1"; ./hq.sh ls p1
