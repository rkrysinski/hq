"""Drive hq mcp CLIENT over stdio, as an MCP client would, and time wait and send.

Usage: python3 drive.py WORKTREE CLIENT STEP...
STEP: texts | wait | wait:SECONDS | send:AGENT
"""
import json
import subprocess
import sys
import time

worktree, client, steps = sys.argv[1], sys.argv[2], sys.argv[3:]
p = subprocess.Popen([worktree + "/scripts/local-dev.sh", "--stub", "native", "mcp", client],
                     stdin=subprocess.PIPE, stdout=subprocess.PIPE, text=True)
seq = 0


def rpc(method, params=None, notify=False):
    global seq
    msg = {"jsonrpc": "2.0", "method": method}
    if params is not None:
        msg["params"] = params
    if not notify:
        seq += 1
        msg["id"] = seq
    p.stdin.write(json.dumps(msg) + "\n")
    p.stdin.flush()
    if notify:
        return None
    while True:
        r = json.loads(p.stdout.readline())
        if r.get("id") == seq:
            return r


def tool(name, args):
    r = rpc("tools/call", {"name": name, "arguments": args})["result"]
    return r.get("isError", False), r["content"][0]["text"]


init = rpc("initialize", {"protocolVersion": "2025-06-18", "capabilities": {},
                          "clientInfo": {"name": "qa-driver", "version": "1"}})["result"]
rpc("notifications/initialized", notify=True)
print(f"$ hq mcp {client}   (driven over stdio)")
_, out = tool("list", {})
since = json.loads(out)["next_since"]
for step in steps:
    if step == "texts":
        ins = init["instructions"]
        line = next(l for l in ins.splitlines() if l.startswith("- To watch agents"))
        print("instructions, line on watching:\n  " + line)
        tools = rpc("tools/list", {})["result"]["tools"]
        for t in tools:
            if t["name"] in ("wait", "send"):
                d = t["description"]
                i = d.find("(default")
                print(f"{t['name']} description: ...{d[max(0, i - 40):i + 60]}...")
                if t["name"] == "wait":
                    j = d.find("To watch agents")
                    print(f"wait description on watching: {d[j:j + 120]}...")
    elif step.startswith("wait"):
        args = {"since": since}
        if ":" in step:
            args["timeout"] = int(step.split(":")[1])
        start = time.monotonic()
        err, out = tool("wait", args)
        took = time.monotonic() - start
        res = json.loads(out)
        print(f"wait {json.dumps(args)} -> agents {len(res['agents'])}, error {err}, after {took:.1f} s")
        since = res["next_since"]
    elif step.startswith("send:"):
        args = {"name": step.split(":")[1], "text": "any news?", "wait": True}
        start = time.monotonic()
        err, out = tool("send", args)
        took = time.monotonic() - start
        res = json.loads(out)
        print(f"send {json.dumps(args)} -> outcome {res.get('outcome')}, delivery {res.get('delivery')!r}, error {err}, after {took:.1f} s")
p.stdin.close()
p.wait(timeout=30)
