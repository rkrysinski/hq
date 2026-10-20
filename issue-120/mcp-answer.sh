#!/bin/bash
# Calls hq mcp's answer tool for agent a's open dialog: first without
# confirmed, then with it.
id=$(/home/dev/work/repositories/hq/.claude/worktrees/issue-120-answer-claude-dialogs/scripts/local-dev.sh ls --json | sed -n 's/.*"dialog": "\(......\)".*/\1/p')
echo "dialog $id"
call() { printf '{"jsonrpc":"2.0","id":%s,"method":"tools/call","params":{"name":"answer","arguments":%s}}\n' "$1" "$2"; }
{
  printf '{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"qa","version":"0"}}}\n'
  printf '{"jsonrpc":"2.0","method":"notifications/initialized"}\n'
  call 1 "{\"name\":\"a\",\"dialog\":\"$id\",\"answers\":[{\"options\":[2]}]}"
  sleep 2
  call 2 "{\"name\":\"a\",\"dialog\":\"$id\",\"answers\":[{\"options\":[2]}],\"confirmed\":true}"
  sleep 6
} | /home/dev/work/repositories/hq/.claude/worktrees/issue-120-answer-claude-dialogs/scripts/local-dev.sh mcp claude-code | python3 -Ic 'import json,sys
for l in sys.stdin:
    m=json.loads(l)
    if m.get("id") in (1,2): print("call %d:" % m["id"], m["result"]["content"][0]["text"].strip())'
