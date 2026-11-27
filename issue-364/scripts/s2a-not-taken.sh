#!/bin/sh
# Check 2 (a): not_taken from a send's wait task, the message waiting behind a dialog. e2 (at its prompt) is
# given a slow job; a plain send leaves "input please" queued; a declaring send with wait leaves
# "/model test-model", a slash command, held behind it: the call goes on as a task after 50 s; 55 s in,
# e2's turn is interrupted (esc, so no hook delivers), a poll types "input please" in, which opens a
# question dialog before e2 took the slash command: the task completes not_taken, the message waiting
cd "$(dirname "$0")"
./hq.sh send e2 "slow job"
./hq.sh send e2 "input please"
./typein.sh hq-dev-t364 e2 esc 58 &
QAC_TASK_FILE=s2a.task ./qac.py discover 'call:send:{"name":"e2","text":"/model test-model","wait":true}' poll -- ./hqmcp.sh claude-code
wait
echo "== hq ls e2"; ./hq.sh ls e2
