#!/usr/bin/env python3
"""A raw MCP client over stdio for QA of hq mcp's wait task (#361, copied for #362 with at:, cancel:ID and getfile:).

It speaks protocol 2026-07-28 as Claude Code 2.1.332 does: server/discover
first, no initialize, and every request carrying the _meta triple, with the
Tasks extension declared (--tasks, the default) or not (--no-tasks).

usage: qac.py [--no-tasks] STEP... -- CMD [ARGS...]

STEPs, run in order:
  discover                 server/discover, prints its capabilities
  call:TOOL:JSON           tools/call; "$since" in JSON is the next_since of
                           the last result that had one
  get                      tasks/get for the last task handle
  get:ID                   tasks/get for ID
  getfile:PATH             tasks/get for the id in file PATH, read at that step
  poll                     tasks/get for the last task until it completes,
                           at the pollIntervalMs the server gave
  cancel                   tasks/cancel for the last task
  cancel:ID                tasks/cancel for ID
  at:EPOCH                 wait until that moment (seconds since the epoch)
  cancel2                  two tasks/cancel for the last task, sent at once
  tasks / notasks          declare the extension (or not) from here on
  sleep:N                  wait N seconds
Every response is printed whole, one line, with the seconds since start.
The last task id is written to the file in $QAC_TASK_FILE when set.
"""
import json
import os
import subprocess
import sys
import time

EXT = "io.modelcontextprotocol/tasks"
T0 = time.time()


def stamp():
    return "%7.1fs %s" % (time.time() - T0, time.strftime("%H:%M:%SZ", time.gmtime()))


class Client:
    def __init__(self, cmd, tasks):
        self.p = subprocess.Popen(cmd, stdin=subprocess.PIPE, stdout=subprocess.PIPE, text=True)
        self.n = 0
        self.tasks = tasks
        self.since = None
        self.task = os.environ.get("QAC_TASK", "")
        self.interval = 5.0

    def meta(self):
        caps = {"roots": {"listChanged": True}, "elicitation": {"form": {}, "url": {}}}
        if self.tasks:
            caps["extensions"] = {EXT: {}}
        return {
            "io.modelcontextprotocol/protocolVersion": "2026-07-28",
            "io.modelcontextprotocol/clientInfo": {"name": "qa-client", "version": "1"},
            "io.modelcontextprotocol/clientCapabilities": caps,
        }

    def write(self, method, params):
        self.n += 1
        params = dict(params or {})
        params["_meta"] = self.meta()
        msg = {"jsonrpc": "2.0", "id": self.n, "method": method, "params": params}
        print("%s >> %s %s" % (stamp(), method, json.dumps({k: v for k, v in params.items() if k != "_meta"})), flush=True)
        self.p.stdin.write(json.dumps(msg) + "\n")
        self.p.stdin.flush()
        return self.n

    def read(self, want):
        while True:
            line = self.p.stdout.readline()
            if not line:
                raise SystemExit("the server closed its output")
            m = json.loads(line)
            if m.get("id") in want:
                print("%s << %s" % (stamp(), json.dumps(m)), flush=True)
                return m

    def request(self, method, params=None):
        return self.read({self.write(method, params)})

    def note(self, m):
        r = m.get("result") or {}
        if r.get("resultType") == "task":
            self.task = r["taskId"]
            self.interval = (r.get("pollIntervalMs") or 5000) / 1000
            if os.environ.get("QAC_TASK_FILE"):
                open(os.environ["QAC_TASK_FILE"], "w").write(self.task)
        inner = r.get("result") if "status" in r else r
        for c in (inner or {}).get("content", []) or []:
            try:
                self.since = json.loads(c.get("text", "")).get("next_since", self.since)
            except ValueError:
                pass


def main():
    args = sys.argv[1:]
    tasks = True
    if args and args[0] == "--no-tasks":
        tasks, args = False, args[1:]
    i = args.index("--")
    steps, cmd = args[:i], args[i + 1:]
    c = Client(cmd, tasks)
    for s in steps:
        kind, _, rest = s.partition(":")
        if kind == "discover":
            m = c.request("server/discover")
        elif kind == "call":
            tool, _, a = rest.partition(":")
            a = a.replace("$since", c.since or "")
            m = c.request("tools/call", {"name": tool, "arguments": json.loads(a or "{}")})
            c.note(m)
        elif kind == "getfile":
            m = c.request("tasks/get", {"taskId": open(rest).read().strip()})
        elif kind == "get":
            m = c.request("tasks/get", {"taskId": rest or c.task})
            c.note(m)
        elif kind == "poll":
            while True:
                m = c.request("tasks/get", {"taskId": c.task})
                c.note(m)
                r = m.get("result") or {}
                if "error" in m or r.get("status") != "working":
                    break
                time.sleep(c.interval)
        elif kind == "cancel":
            c.request("tasks/cancel", {"taskId": rest or c.task})
        elif kind == "at":
            time.sleep(max(0.0, float(rest) - time.time()))
        elif kind == "cancel2":
            a = c.write("tasks/cancel", {"taskId": c.task})
            b = c.write("tasks/cancel", {"taskId": c.task})
            c.read({a, b})
            c.read({a, b})
        elif kind == "tasks":
            c.tasks = True
        elif kind == "notasks":
            c.tasks = False
        elif kind == "sleep":
            time.sleep(float(rest))
        else:
            raise SystemExit("unknown step " + s)
    c.p.stdin.close()
    c.p.wait(timeout=10)


main()
