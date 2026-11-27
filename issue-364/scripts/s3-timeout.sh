#!/bin/sh
# Check 3: e4 works on a slow job; a declaring send with wait and timeout 80: a handle after 50 s with
# ttlMs 30000; the task completes at 80 s with timeout and no agents; the message still waits for e4
cd "$(dirname "$0")"
./qac.py discover 'call:send:{"name":"e4","text":"status?","wait":true,"timeout":80}' poll -- ./hqmcp.sh claude-code
echo "== hq ls e4 (PENDING-like: the message still waits)"; ./hq.sh ls e4 --json | python3 -c 'import json,sys; a=json.load(sys.stdin)["agents"][0]; print(a["name"], a["state"], "pending", a["pending"])'
