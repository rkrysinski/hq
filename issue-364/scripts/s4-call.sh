#!/bin/sh
# Check 4: a stall and no_turn stay in the call, the request declaring tasks.
# (a) /model test-model to e5 (done, at its prompt): no_turn in the call after 5 s.
# (b) e6's box holds a draft (the line "draft" typed into its pane): "hi" stalls in the call after 5 s.
# (c) e7 works on a slow job; "unsent please" is queued; 44 s in e7's turn is interrupted (esc), the call types
# the message in, and Claude leaves it in the box: stalled in the call, past 50 s, no task handle.
cd "$(dirname "$0")"
echo "== (a)"; ./qac.py discover 'call:send:{"name":"e5","text":"/model test-model","wait":true}' -- ./hqmcp.sh claude-code
echo "== (b)"; ./typein.sh hq-dev-t364 e6 draft; sleep 2
./qac.py discover 'call:send:{"name":"e6","text":"hi","wait":true}' -- ./hqmcp.sh claude-code
echo "== (c)"; ./typein.sh hq-dev-t364 e7 esc 44 &
./qac.py discover 'call:send:{"name":"e7","text":"unsent please","wait":true}' -- ./hqmcp.sh claude-code
wait
