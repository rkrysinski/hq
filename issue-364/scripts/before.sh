#!/bin/sh
# The before of checks 1 and 7, on the integration branch (QA_BEFORE=1) in stub environment t364b:
# a declaring send with wait to an agent at its prompt whose slow turn ends 70 s in; the send tool's
# description for Claude Code
cd "$(dirname "$0")"
export QA_ENV=t364b QA_BEFORE=1
./hq.sh new b1 /tmp/hq-stub-t364b/repo "hello"
sleep 4
./hq.sh ls
echo "== check 1 before: send with wait to b1 (done, at its prompt), the request declaring tasks; b1's turn ends 70 s in"
./typein.sh hq-dev-t364b b1 finish 70 &
./qac.py 'call:send:{"name":"b1","text":"slow job","wait":true}' -- ./hqmcp.sh claude-code
wait
