#!/bin/sh
# Check 8: two waits on e6 from the same next_since, 3 s apart, are two tasks; each completes once e6 finishes
since=$(./qac.py 'call:list:{"names":["e6"]}' -- ./hqmcp.sh claude-code | sed -n 's/.*next_since[^c]*\(c[0-9]*\).*/\1/p' | tail -1)
echo "== list's next_since: $since"
./typein.sh hq-dev-t361 e6 finish 62 &
./qac.py "call:wait:{\"names\":[\"e6\"],\"since\":\"$since\"}" poll -- ./hqmcp.sh claude-code > s8-first.txt &
sleep 3 # the second starts once the first has built hq
./qac.py "call:wait:{\"names\":[\"e6\"],\"since\":\"$since\"}" poll -- ./hqmcp.sh claude-code > s8-second.txt
wait
echo "== first wait"; cat s8-first.txt; echo "== second wait"; cat s8-second.txt
