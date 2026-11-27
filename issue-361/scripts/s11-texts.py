#!/usr/bin/env python3
"""Check 11: Claude Code's texts as hq mcp claude-code serves them: the line on watching of its
instructions (server/discover), where it starts and ends in them (Claude Code shows the first
4096 characters), and the wait and send tools' descriptions (tools/list)."""
import json, subprocess
EXT = "io.modelcontextprotocol/tasks"
meta = {"io.modelcontextprotocol/protocolVersion": "2026-07-28",
        "io.modelcontextprotocol/clientInfo": {"name": "qa-client", "version": "1"},
        "io.modelcontextprotocol/clientCapabilities": {"extensions": {EXT: {}}}}
p = subprocess.Popen(["./hqmcp.sh", "claude-code"], stdin=subprocess.PIPE, stdout=subprocess.PIPE, text=True)
out = {}
for i, m in enumerate(["server/discover", "tools/list"], 1):
    p.stdin.write(json.dumps({"jsonrpc": "2.0", "id": i, "method": m, "params": {"_meta": meta}}) + "\n")
    p.stdin.flush()
    while True:
        r = json.loads(p.stdout.readline())
        if r.get("id") == i:
            out[m] = r["result"]
            break
p.stdin.close()
p.wait(timeout=10)
ins = out["server/discover"]["instructions"]
line = next(l for l in ins.split("\n") if l.startswith("- To watch agents"))
at = ins.index(line)
print("instructions: %d characters; the line on watching runs from %d to %d (within the 4096 Claude Code shows: %s):" % (len(ins), at, at + len(line), at + len(line) <= 4096))
print(line)
for t in out["tools/list"]["tools"]:
    if t["name"] in ("wait", "send"):
        print()
        print("%s (readOnlyHint %s):" % (t["name"], (t.get("annotations") or {}).get("readOnlyHint")))
        print(t["description"])
