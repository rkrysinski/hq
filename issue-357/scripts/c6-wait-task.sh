#!/bin/sh
# Check 6 (nearby): a declaring wait on w1 (working) given no timeout, w1 let finish 65 s in: a handle at 50 s, then
# completed with w1 done; and, at once beside it, a declaring wait on p2 (done, nothing changes it) given timeout 60:
# a handle at 50 s with ttlMs about 10000, completed at 60 s with no agents (started 4 s later, so the two builds of
# scripts/local-dev.sh do not overlap)
cd "$(dirname "$0")"
./typein.sh hq-dev-t357r w1 finish 65 &
(sleep 4; QAC_POLL_UNTIL=150 ./qac.py discover 'call:list:{"names":["p2"]}' 'call:wait:{"names":["p2"],"since":"$since","timeout":60}' poll -- ./hqmcp.sh claude-code > c6-timeout.txt 2>&1) &
QAC_POLL_UNTIL=150 ./qac.py discover 'call:list:{"names":["w1"]}' 'call:wait:{"names":["w1"],"since":"$since"}' poll -- ./hqmcp.sh claude-code
wait
echo "== the wait on p2 with timeout 60"; cat c6-timeout.txt
