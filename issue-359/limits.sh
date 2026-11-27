#!/bin/sh
# QA of #359, check 4 and 5: how long a wait with no agent changing lasts, per client and timeout, and the command line's hq wait
for c in claude-desktop sbx; do
  echo "== hq mcp $c, wait with no timeout"
  qa-mcp call wait '{"names":["a"],"since":"0s"}' -- hq-mcp $c | grep -E "call |returned|agents"
done
for t in 50 3; do
  echo "== hq mcp claude-code, wait with timeout $t"
  qa-mcp call wait '{"names":["a"],"since":"0s","timeout":'$t'}' -- hq-mcp claude-code | grep -E "call |returned|agents"
done
echo "== hq wait a (the command line, no --timeout)"
s=$(date +%s); hq wait a; echo "returned after $(( $(date +%s) - s )) s"
