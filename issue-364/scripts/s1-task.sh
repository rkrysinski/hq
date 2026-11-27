#!/bin/sh
# Checks 1, 5, 8: a declaring send with wait to e1 (done, at its prompt): the slow job it starts ends 70 s
# in (a line typed into e1's pane); the call is answered with a task handle after 50 s, tasks/get works
# until the turn ends, then completes with turn_ended and e1's row; a wait on e1 from the result's
# next_since returns nothing (the change is not returned twice). Another hq mcp answers for the same task
# while it works (check 8).
cd "$(dirname "$0")"
./typein.sh hq-dev-t364 e1 finish 70 &
(sleep 58; echo "== check 8: another hq mcp claude-code asks for the task while it works"; QAC_TASK=$(cat s1.task) ./qac.py discover get -- ./hqmcp.sh claude-code) > s8-other-working.txt 2>&1 &
QAC_TASK_FILE=s1.task ./qac.py discover 'call:send:{"name":"e1","text":"slow job","wait":true}' get poll \
  'call:wait:{"names":["e1"],"since":"$since","timeout":5}' -- ./hqmcp.sh claude-code
wait
echo "== check 8: another hq mcp claude-code asks for the completed task, and for it with one character of its agent's name changed"
id=$(cat s1.task)
QAC_TASK=$id ./qac.py discover get "get:$(echo "$id" | sed 's/:e1=/:e9=/')" -- ./hqmcp.sh claude-code
