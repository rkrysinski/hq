#!/usr/bin/env python3
"""A raw MCP client over stdio for QA of hq mcp.

usage: mcpc.py texts -- CMD [ARGS...]
         prints the server's instructions and the descriptions of wait and send,
         with the timeout field of each one's input schema
       mcpc.py call TOOL JSON [TOOL JSON ...] -- CMD [ARGS...]
         calls each tool in turn, printing when each call started and ended,
         how long it took and its result; a JSON value {"$since": "prev"} is
         replaced by the next_since of the previous result
"""
import json
import subprocess
import sys
import time


def now():
    return time.strftime("%H:%M:%S", time.gmtime()) + "Z"


class Client:
    def __init__(self, cmd):
        self.p = subprocess.Popen(cmd, stdin=subprocess.PIPE, stdout=subprocess.PIPE, text=True)
        self.n = 0

    def request(self, method, params):
        self.n += 1
        msg = {"jsonrpc": "2.0", "id": self.n, "method": method, "params": params}
        self.p.stdin.write(json.dumps(msg) + "\n")
        self.p.stdin.flush()
        while True:
            line = self.p.stdout.readline()
            if not line:
                raise SystemExit("the server closed its output")
            m = json.loads(line)
            if m.get("id") == self.n:
                if "error" in m:
                    raise SystemExit("error: %s" % m["error"])
                return m["result"]

    def notify(self, method):
        self.p.stdin.write(json.dumps({"jsonrpc": "2.0", "method": method}) + "\n")
        self.p.stdin.flush()

    def start(self):
        r = self.request("initialize", {"protocolVersion": "2025-06-18", "capabilities": {}, "clientInfo": {"name": "qa-client", "version": "1"}})
        self.notify("notifications/initialized")
        return r

    def close(self):
        self.p.stdin.close()
        self.p.wait(timeout=10)


def main():
    args = sys.argv[1:]
    i = args.index("--")
    mode, rest, cmd = args[0], args[1:i], args[i + 1:]
    c = Client(cmd)
    init = c.start()
    if mode == "texts":
        print("== instructions")
        print(init.get("instructions", ""))
        tools = {t["name"]: t for t in c.request("tools/list", {})["tools"]}
        for name in ("wait", "send"):
            print("== %s description" % name)
            print(tools[name]["description"])
            print("== %s input schema, timeout" % name)
            print(tools[name]["inputSchema"]["properties"]["timeout"]["description"])
    else:
        since = None
        for k in range(0, len(rest), 2):
            tool, a = rest[k], json.loads(rest[k + 1])
            for key, v in list(a.items()):
                if v == {"$since": "prev"}:
                    a[key] = since
            t0 = time.time()
            print("%s call %s %s" % (now(), tool, json.dumps(a)), flush=True)
            r = c.request("tools/call", {"name": tool, "arguments": a})
            text = "".join(x.get("text", "") for x in r.get("content", []))
            print("%s returned after %.1f s, isError %s:" % (now(), time.time() - t0, r.get("isError", False)), flush=True)
            print(text, flush=True)
            try:
                since = json.loads(text).get("next_since", since)
            except ValueError:
                pass
    c.close()


main()
