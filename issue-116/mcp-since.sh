#!/bin/sh
# QA for #116: what hq mcp tells the supervisor about wait's since.
# Usage: mcp-since.sh HQ_BIN
set -eu
HQ=$1
out=$(printf '%s\n' \
    '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"qa","version":"0"}}}' \
    '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
    '{"jsonrpc":"2.0","id":2,"method":"tools/list"}' | (cat; sleep 1) | HOME=$(mktemp -d) "$HQ" mcp)
sentences() { tr '\n' ' ' | sed 's/\. /.\n/g' | grep -i 'since' | sed 's/^ *//' | fold -s -w 104 | sed 's/^/  /'; }
echo "# instructions"
printf "%s\n" "$out" | jq -r 'select(.id==1) | .result.instructions' | sentences
for t in wait read send answer list; do
    echo "# $t"
    printf "%s\n" "$out" | jq -r --arg t "$t" 'select(.id==2) | .result.tools[] | select(.name==$t) | .description, (.inputSchema.properties.since.description // empty)' | sentences
done
