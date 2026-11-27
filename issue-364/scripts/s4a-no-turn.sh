#!/bin/sh
# Check 4 (a): /model test-model to e5 (done, at its prompt), the request declaring tasks: no_turn in the call after 5 s
cd "$(dirname "$0")"
./qac.py discover 'call:send:{"name":"e5","text":"/model test-model","wait":true}' -- ./hqmcp.sh claude-code
