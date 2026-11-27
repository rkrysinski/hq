#!/bin/sh
# Check 7, nearby, run side by side: a wait on h9 with a timeout of 30 s declaring tasks ends in its call at
# 30 s; with a timeout of 120 s and no declaration a call of 120 s; hq wait h9 at the command line "nothing
# yet" after 50 s; each started 5 s after the one before, since two builds of scripts/local-dev.sh at once can race (#368)
cd "$(dirname "$0")"
(./qac.py 'call:list:{"names":["h9"]}' 'call:wait:{"names":["h9"],"since":"$since","timeout":30}' -- ./hqmcp.sh claude-code > s7-30.txt) &
sleep 5
(./qac.py --no-tasks 'call:list:{"names":["h9"]}' 'call:wait:{"names":["h9"],"since":"$since","timeout":120}' -- ./hqmcp.sh claude-code > s7-120-no-tasks.txt) &
sleep 5
(s=$(date +%s); ./hq.sh wait h9 > s7-cli.txt; echo "exit $? after $(( $(date +%s) - s )) s" >> s7-cli.txt) &
wait
