#!/bin/sh
# Check 6: without the declaration, a send with wait to e8 (done) whose slow job ends 70 s in: one call that
# returns turn_ended after 70 s, no handle
cd "$(dirname "$0")"
./typein.sh hq-dev-t364 e8 finish 70 &
./qac.py --no-tasks discover 'call:send:{"name":"e8","text":"slow job","wait":true}' -- ./hqmcp.sh claude-code
wait
