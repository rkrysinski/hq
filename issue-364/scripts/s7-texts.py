#!/usr/bin/env python3
"""Check 7 of #364: the send tool's description, and its timeout's schema, as hq mcp CLIENT serves
them (tools/list), for each client named on the command line (default: claude-code, claude-desktop,
sbx). usage: s7-texts.py [CLIENT...]"""
import json, subprocess, sys
EXT = "io.modelcontextprotocol/tasks"
meta = {"io.modelcontextprotocol/protocolVersion": "2026-07-28",
        "io.modelcontextprotocol/clientInfo": {"name": "qa-client", "version": "1"},
        "io.modelcontextprotocol/clientCapabilities": {"extensions": {EXT: {}}}}
for client in sys.argv[1:] or ["claude-code", "claude-desktop", "sbx"]:
    p = subprocess.Popen(["./hqmcp.sh", client], stdin=subprocess.PIPE, stdout=subprocess.PIPE, text=True)
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
    for t in out["tools/list"]["tools"]:
        if t["name"] == "send":
            print("== hq mcp %s: send" % client)
            print(t["description"])
            print("timeout: " + t["inputSchema"]["properties"]["timeout"].get("description", ""))
            print()
