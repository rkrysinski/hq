#!/bin/sh
# Check 10, nearby: a declaring wait on e9 (given a slow job) still goes on as a wait task after 50 s and completes
# once e9 finishes (65 s in); hq send --wait at the command line to e10 (working) still says nothing yet after its 50 s
cd "$(dirname "$0")"
./hq.sh send e9 "slow job again"
./typein.sh hq-dev-t364 e9 finish 65 &
./qac.py discover 'call:list:{"names":["e9"]}' 'call:wait:{"names":["e9"],"since":"$since"}' poll -- ./hqmcp.sh claude-code
wait
