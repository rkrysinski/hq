#!/bin/sh
# Check 16: unchanged nearby: Claude Desktop's and the gateway's waits, declaring tasks, end with no agents
# after 50 s and 110 s, no task; Claude Code's wait given a timeout of 70 stays a call of 70 s; hq wait at
# the command line still says nothing yet after 50 s (e7 working throughout)
./qac.py 'call:list:{"names":["e7"]}' 'call:wait:{"names":["e7"],"since":"$since"}' -- ./hqmcp.sh claude-desktop &
sleep 3
./qac.py 'call:list:{"names":["e7"]}' 'call:wait:{"names":["e7"],"since":"$since"}' -- ./hqmcp.sh sbx > s16-sbx.txt &
sleep 3
./qac.py 'call:list:{"names":["e7"]}' 'call:wait:{"names":["e7"],"since":"$since","timeout":70}' -- ./hqmcp.sh claude-code > s16-timeout.txt &
sleep 3
start=$(date +%s); ./hq.sh wait e7; echo "== hq wait e7 returned after $(( $(date +%s) - start )) s"
wait
echo "== sbx"; cat s16-sbx.txt; echo "== claude-code, timeout 70"; cat s16-timeout.txt
