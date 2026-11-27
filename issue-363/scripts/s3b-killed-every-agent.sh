#!/bin/sh
# Check 3b, run alone: a wait on every agent goes on as a task, and h6 is killed 60 s in: the task completes
# with h6 ended alone
cd "$(dirname "$0")"
(sleep 60; ./hq.sh kill h6 -y) &
./qac.py 'call:list:{}' 'call:wait:{"since":"$since"}' poll -- ./hqmcp.sh claude-code
wait
