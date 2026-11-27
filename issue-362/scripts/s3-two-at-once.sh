#!/bin/sh
# Checks 3 and 4: stub environment t362d, whose state directory holds no key; two hq mcp claude-code, the
# second started 5 s after the first (two runs of scripts/local-dev.sh at once can fail, #368), each take a
# wait on d1 at the same moment and hand it off after 50 s, each making the key if none is there; then each
# is asked for the other's task; d1 is let finish, and a third hq mcp, started afresh, answers for both tasks.
export QA_ENV=t362d
state=/tmp/hq-stub-t362d/home/.local/state/hq
echo "== the state directory before: $(ls $state | tr '\n' ' ')"
at=$(($(date +%s) + 20))
rm -f s3-1.task s3-2.task
QAC_TASK_FILE=s3-1.task ./qac.py 'call:list:{"names":["d1"]}' "at:$at" 'call:wait:{"names":["d1"],"since":"$since"}' sleep:3 getfile:s3-2.task -- ./hqmcp.sh claude-code > s3-first.txt &
sleep 5
QAC_TASK_FILE=s3-2.task ./qac.py 'call:list:{"names":["d1"]}' "at:$at" 'call:wait:{"names":["d1"],"since":"$since"}' sleep:3 getfile:s3-1.task -- ./hqmcp.sh claude-code > s3-second.txt &
wait
echo "== first hq mcp"; ./summarize.py s3-first.txt
echo "== second hq mcp"; ./summarize.py s3-second.txt
echo "== the state directory after"; ls -la $state | sed 1d
./typein.sh hq-dev-t362d d1 finish
echo "== a third hq mcp, started afresh"
QAC_TASK=$(cat s3-1.task) ./qac.py poll getfile:s3-2.task -- ./hqmcp.sh claude-code > s3-third.txt
./summarize.py s3-third.txt
