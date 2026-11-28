#!/usr/bin/env python3
"""Checks 3 and 7: the instructions and the tools' descriptions as hq mcp CLIENT serves them (server/discover,
tools/list), for each client named on the command line (default: claude-code, claude-desktop, sbx). For each: the
instructions' length and what of them lies past the 4096th character, which Claude Code 2.1.332 cuts (#365); the
watching line; the wait and send descriptions and their timeouts. For claude-code also: each phrase that would say a
timeout keeps the call a call, or forbid one, and whether it is anywhere; and each fact of the bullet on own tasks
whether the wait, read and list descriptions hold it. usage: [QA_ENV=ID] [QA_BEFORE=1] texts.py [CLIENT...]"""
import json, subprocess, sys
EXT = "io.modelcontextprotocol/tasks"
meta = {"io.modelcontextprotocol/protocolVersion": "2026-07-28",
        "io.modelcontextprotocol/clientInfo": {"name": "qa-client", "version": "1"},
        "io.modelcontextprotocol/clientCapabilities": {"extensions": {EXT: {}}}}
NEVER = ["with no timeout and", "once with no timeout", "with no timeout (", "unless it has started a task", "stays a call", "keeps the call a call", "so to watch give none"]
OWN = {"wait": "with own_tasks still runs a shell command or watch of its own: Claude takes another turn when one ends, so wait for it again if you need that result (a dev server may never end); the list is that turn end's and may be stale.",
       "read": "with own_tasks still runs a shell command or watch of its own: Claude takes another turn when one ends, so wait for it again if you need that result (a dev server may never end); the list is that turn end's and may be stale.",
       "list": "own_tasks (while it is done or in question: the shell commands and watches of its own still running at that turn end"}
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
    ins = out["server/discover"].get("instructions", "")
    tools = {t["name"]: t for t in out["tools/list"]["tools"]}
    print("== hq mcp %s: instructions, %d characters" % (client, len(ins)))
    for l in ins.splitlines():
        if l.startswith("- To watch agents"):
            end = ins.index(l) + len(l)
            print("watching line (ends at character %d): %s" % (end, l))
    if len(ins) > 4096:
        print("past the 4096th character, which Claude Code 2.1.332 cuts: %r" % ins[4096:])
    else:
        print("nothing past the 4096th character")
    for n in ["wait", "send"]:
        print("-- %s: %s" % (n, tools[n]["description"]))
        print("   timeout: %s" % tools[n]["inputSchema"]["properties"]["timeout"].get("description", ""))
    if client == "claude-code":
        every = ins + " ".join(t["description"] + json.dumps(t["inputSchema"]) for t in tools.values())
        for ph in NEVER:
            print("phrase %r: %s" % (ph, "PRESENT" if ph in every else "absent"))
        for n, fact in OWN.items():
            print("own tasks in %s: %s" % (n, "held" if fact in tools[n]["description"] else "MISSING"))
    print()
