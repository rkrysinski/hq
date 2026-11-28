#!/usr/bin/env python3
"""usage: experimental.py initialize|meta SECONDS NAME -- CMD... - check: the experimental tasks of the protocol's revision
2025-11-25 are not the declaration of the MCP Tasks extension. "initialize" opens the session as a 2025-11-25 client that
declares them in its capabilities; "meta" speaks 2026-07-28 with them in each request's _meta capabilities, outside
extensions. Then list NAME, then wait on NAME with timeout SECONDS and params.task (ttl 600000), as that revision asks
for a task; then tasks/get. Every line is printed with the seconds since the start."""
import json, subprocess, sys, time

T0 = time.time()
EXPERIMENTAL = {"list": {}, "cancel": {}, "requests": {"tools": {"call": {}}, "sampling": {"createMessage": {}}}}


def stamp():
    return "%7.1fs %s" % (time.time() - T0, time.strftime("%H:%M:%SZ", time.gmtime()))


mode, seconds, name = sys.argv[1], int(sys.argv[2]), sys.argv[3]
cmd = sys.argv[sys.argv.index("--") + 1:]
p = subprocess.Popen(cmd, stdin=subprocess.PIPE, stdout=subprocess.PIPE, text=True)
n = 0


def send(method, params=None, notify=False):
    global n
    params = dict(params or {})
    if mode == "meta":
        params["_meta"] = {"io.modelcontextprotocol/protocolVersion": "2026-07-28",
                           "io.modelcontextprotocol/clientInfo": {"name": "qa-client", "version": "1"},
                           "io.modelcontextprotocol/clientCapabilities": {"tasks": EXPERIMENTAL}}
    msg = {"jsonrpc": "2.0", "method": method, "params": params}
    if not notify:
        n += 1
        msg["id"] = n
    print("%s >> %s %s" % (stamp(), method, json.dumps({k: v for k, v in params.items() if k != "_meta"})), flush=True)
    p.stdin.write(json.dumps(msg) + "\n")
    p.stdin.flush()
    if notify:
        return None
    while True:
        m = json.loads(p.stdout.readline())
        if m.get("id") == n:
            print("%s << %s" % (stamp(), json.dumps(m)), flush=True)
            return m


if mode == "initialize":
    send("initialize", {"protocolVersion": "2025-11-25", "capabilities": {"tasks": EXPERIMENTAL}, "clientInfo": {"name": "qa-client", "version": "1"}})
    send("notifications/initialized", notify=True)
else:
    send("server/discover")
since = json.loads(send("tools/call", {"name": "list", "arguments": {"names": [name]}})["result"]["content"][0]["text"])["next_since"]
r = send("tools/call", {"name": "wait", "arguments": {"names": [name], "since": since, "timeout": seconds}, "task": {"ttl": 600000}})["result"]
print("%s -- %s" % (stamp(), "TASK HANDLE" if r.get("resultType") == "task" or "taskId" in r else "no handle: the call's own result, isError %s" % r.get("isError", False)), flush=True)
send("tasks/get", {"taskId": r.get("taskId", "w1")})
p.stdin.close()
p.wait(timeout=10)
