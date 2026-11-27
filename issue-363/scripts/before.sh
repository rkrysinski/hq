#!/bin/sh
# The before of checks 1, 3 and 5, on the integration branch (QA_BEFORE=1) in stub environment t363b:
# a wait given a timeout of 120 s; a wait task whose agent is killed; a wait task after an update to another generation
cd "$(dirname "$0")"
export QA_ENV=t363b QA_BEFORE=1
for n in g1 g2 g3; do ./hq.sh new $n /tmp/hq-stub-t363b/repo "slow job of $n"; done
sleep 3
./hq.sh ls
echo "== check 1 before: wait on g1 with a timeout of 120 s, the request declaring tasks"
./qac.py 'call:list:{"names":["g1"]}' 'call:wait:{"names":["g1"],"since":"$since","timeout":120}' -- ./hqmcp.sh claude-code
echo "== check 3 before: wait on g2 goes on as a task, then g2 is killed, and the task is asked for"
QAC_TASK_FILE=b3.task ./qac.py 'call:list:{"names":["g2"]}' 'call:wait:{"names":["g2"],"since":"$since"}' get -- ./hqmcp.sh claude-code
./hq.sh kill g2 -y
QAC_TASK=$(cat b3.task) ./qac.py get sleep:5 get -- ./hqmcp.sh claude-code
echo "== check 5 before: wait on g3 goes on as a task; 55 s in, the hq on disk becomes another generation; the task is asked for, then a tool"
bin=$(LOCAL_DEV_ID=$QA_ENV <worktree>/scripts/local-dev.sh --stub-env)/bin/hq
(sleep 56; printf '#!/bin/sh\n# stands for an hq of another generation\necho 999\n' > "$bin.new" && chmod +x "$bin.new" && mv -f "$bin.new" "$bin" && echo "$(date -u +%H:%M:%SZ) the hq on disk now answers __generation with 999") &
./qac.py 'call:list:{"names":["g3"]}' 'call:wait:{"names":["g3"],"since":"$since"}' sleep:8 get 'call:list:{"names":["g3"]}' -- ./hqmcp.sh claude-code
wait
