#!/bin/sh
# Check 2: another stub environment, t362b, has its own state directory; its hq mcp hands off a wait of its own
# after 50 s (its first, which makes its key); each environment's hq mcp is asked for the other's id. Then
# t362's state directory is moved aside: its hq mcp is asked for its own id, and makes nothing; moved back,
# the id works again.
state=/tmp/hq-stub-t362/home/.local/state
echo "== t362b's state directory before: $(ls /tmp/hq-stub-t362b/home/.local/state/hq 2>&1 | tr '\n' ' ')"
QA_ENV=t362b QAC_TASK_FILE=s2b.task ./qac.py 'call:list:{}' 'call:wait:{"since":"$since"}' -- ./hqmcp.sh claude-code
echo "== t362b's state directory after: $(ls /tmp/hq-stub-t362b/home/.local/state/hq | tr '\n' ' ')"
echo "== t362b asked for t362's id, then for its own"
QA_ENV=t362b ./qac.py "get:$(cat s1.task)" "get:$(cat s2b.task)" -- ./hqmcp.sh claude-code
echo "== t362 asked for t362b's id"
./qac.py "get:$(cat s2b.task)" -- ./hqmcp.sh claude-code
echo "== t362's state directory moved aside: $(mv $state/hq $state/hq.aside && ls -A $state | tr '\n' ' ')"
./qac.py "get:$(cat s1.task)" "cancel:$(cat s1.task)" -- ./hqmcp.sh claude-code
echo "== after the refused asks: $(ls -A $state | tr '\n' ' ')"
mv $state/hq.aside $state/hq
echo "== moved back: $(ls $state/hq | tr '\n' ' ')"
./qac.py "get:$(cat s1.task)" -- ./hqmcp.sh claude-code
