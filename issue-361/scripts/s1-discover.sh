#!/bin/sh
# Check 1: what server/discover answers, for each client's hq mcp (capabilities only)
for c in claude-code claude-desktop sbx; do
  echo "== hq mcp $c: server/discover, its capabilities"
  ./qac.py discover -- ./hqmcp.sh $c | python3 -c 'import json,sys
for l in sys.stdin:
    if "<<" in l: print("  ", json.dumps(json.loads(l.split("<< ",1)[1])["result"]["capabilities"]))'
done
