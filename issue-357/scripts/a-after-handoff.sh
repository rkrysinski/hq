#!/bin/sh
# usage: [QA_ENV=ID] [QA_BEFORE=1] a-after-handoff.sh AGENT TEXT [LINE2] - check 1: a declaring send with wait of TEXT to AGENT,
# which works on a slow job, is answered with a task handle after 50 s; 56 s in, AGENT's turn is interrupted (esc, so no
# hook delivers the message), and LINE2 typed in the same keystrokes when given; tasks/get every 5 s, as Claude Code asks, until
# the task completes or 60 s after the interrupt
cd "$(dirname "$0")"
env=${QA_ENV:-t357r}
./typein.sh "hq-dev-$env" "$1" esc 56 "$3" &
QAC_POLL_UNTIL=120 QAC_TASK_FILE="$1.task" ./qac.py discover "call:send:{\"name\":\"$1\",\"text\":\"$2\",\"wait\":true}" poll -- ./hqmcp.sh claude-code
wait
echo "== hq ls $1"; ./hq.sh ls "$1"
