#!/bin/sh
# Check 2 (b): e3 works on a slow job; a declaring send with wait goes on as a task after 50 s; then hq kill
# e3 -y: the task completes not_taken, the message gone with e3, its row ended
cd "$(dirname "$0")"
QAC_TASK_FILE=s2b.task ./qac.py discover 'call:send:{"name":"e3","text":"anything new?","wait":true}' get -- ./hqmcp.sh claude-code
echo "== hq kill e3 -y"; ./hq.sh kill e3 -y
QAC_TASK=$(cat s2b.task) ./qac.py discover poll -- ./hqmcp.sh claude-code
