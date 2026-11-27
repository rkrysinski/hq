#!/bin/sh
# Check 1: a wait on h1 given a timeout of 120 s, the request declaring tasks, nothing changing: a handle after
# 50 s with ttlMs 70000, working until 120 s, then completed with no agents and next_since
cd "$(dirname "$0")"
./qac.py 'call:list:{"names":["h1"]}' 'call:wait:{"names":["h1"],"since":"$since","timeout":120}' poll -- ./hqmcp.sh claude-code
