#!/usr/bin/env python3
"""QA driver for issue #46: plays the supervisor (an MCP client of `hq mcp`
over stdio) and the user (keys typed at the agent, hq from a shell), and
prints what happened and which desktop notifications the agents' hooks sent
(the fake Claude records them in $FAKE_CLAUDE_NOTIFIED)."""
import json, os, subprocess, sys, time

HQ = os.environ["HQ"]
SOCK = os.environ["HQ_TMUX_SOCKET"]
APP = os.environ["QA_APP"]
LOG = os.environ["FAKE_CLAUDE_NOTIFIED"]
B, D, G, Y, R, Z = "\033[1m", "\033[2m", "\033[32m", "\033[33m", "\033[31m", "\033[0m"


class Supervisor:
    def __init__(self):
        self.p = subprocess.Popen([HQ, "mcp"], stdin=subprocess.PIPE, stdout=subprocess.PIPE, text=True)
        self.n = 0
        self.init = self.rpc("initialize", {"protocolVersion": "2025-06-18", "capabilities": {}, "clientInfo": {"name": "qa-supervisor", "version": "0"}})
        self.p.stdin.write(json.dumps({"jsonrpc": "2.0", "method": "notifications/initialized"}) + "\n")
        self.p.stdin.flush()

    def rpc(self, method, params):
        self.n += 1
        self.p.stdin.write(json.dumps({"jsonrpc": "2.0", "id": self.n, "method": method, "params": params}) + "\n")
        self.p.stdin.flush()
        while True:
            m = json.loads(self.p.stdout.readline())
            if m.get("id") == self.n:
                return m["result"]

    def call(self, tool, quiet=False, **args):
        r = self.rpc("tools/call", {"name": tool, "arguments": args})
        text = r["content"][0]["text"]
        if not quiet:
            shown = ", ".join(f"{k}={json.dumps(v)}" for k, v in args.items() if k != "since").replace(APP, "~/app")
            print(f"{B}supervisor{Z} {tool}({shown})")
            first = text.strip().splitlines()
            if tool == "send":
                first = [json.loads(text)["delivery"]]
            for l in first[:2]:
                print(f"  {D}{l}{Z}")
        return text


def user(name, line):
    out = subprocess.run(["tmux", "-L", SOCK, "list-windows", "-a", "-F", "#{window_id} #{@hq_name}"], capture_output=True, text=True).stdout
    for w in out.splitlines():
        wid, _, n = w.partition(" ")
        if n == name:
            subprocess.run(["tmux", "-L", SOCK, "send-keys", "-t", wid, line, "Enter"], check=True)
            print(f"{B}user{Z} types at {name}: {line!r}")
            return
    sys.exit(f"no window for {name}")


def shell(*args):
    print(f"{B}user{Z} $ hq {' '.join(json.dumps(a) if ' ' in a else a for a in args)}".replace(APP, "~/app"))
    out = subprocess.run([HQ, *args], capture_output=True, text=True)
    for l in (out.stdout + out.stderr).strip().splitlines():
        print(f"  {D}{l}{Z}")


def state(name):
    rows = json.loads(subprocess.run([HQ, "ls", "--json"], capture_output=True, text=True).stdout)["agents"]
    for r in rows:
        if r["name"] == name:
            return r
    return {"state": "?", "last": ""}


def until(name, want, last=None):
    for _ in range(150):
        r = state(name)
        if r["state"] == want and (last is None or r["last"] == last):
            colour = {"done": G, "question": Y, "needs input": Y}.get(want, "")
            print(f"  -> {name} is {colour}{r['state']}{Z}: {r['last']!r}")
            return
        time.sleep(0.1)
    sys.exit(f"{name} never became {want} ({last!r}): {state(name)}")


def notified(mark=[0]):
    try:
        lines = open(LOG).read().splitlines()
    except FileNotFoundError:
        lines = []
    new = lines[mark[0]:]
    mark[0] = len(lines)
    if not new:
        print(f"  {G}desktop notifications sent: none{Z}")
    for l in new:
        name, _, seq = l.partition(" ")
        text = json.loads(seq.replace("\\x1b", "\\u001b").replace("\\a", "\\u0007")).removeprefix("\x1b]9;").removesuffix("\x07")
        print(f"  {R}desktop notification sent: {text}  (agent {name}){Z}")


def ls():
    print(f"{B}user{Z} $ hq ls")
    for l in subprocess.run([HQ, "ls"], capture_output=True, text=True).stdout.rstrip().splitlines():
        print("  " + l)


def already():
    try:
        notified.__defaults__[0][0] = len(open(LOG).read().splitlines())
    except FileNotFoundError:
        pass


def main(group):
    already()
    if group == "told":
        s = Supervisor()
        print(f"{B}instructions of hq mcp (the line on notifications):{Z}")
        for l in s.init["instructions"].splitlines():
            if "not notified" in l:
                print("  " + l)
        tools = {t["name"]: t["description"] for t in s.rpc("tools/list", {})["tools"]}
        for t in ("new", "send"):
            print(f"{B}description of {t} (the sentence on notifications):{Z}")
            for sentence in tools[t].replace(": the user", ". The user").split(". "):
                if "not notified" in sentence:
                    print("  " + sentence.strip() + ".")
    elif group == "done":
        s = Supervisor()
        s.call("new", name="a", dir=APP, prompt="hello")
        until("a", "done", "Done: hello")
        notified()
        s.call("send", name="a", text="one more thing")
        until("a", "done", "Done: one more thing")
        notified()
        s.call("send", name="a", text="a question for you")
        until("a", "question")
        notified()
        ls()
    elif group == "dialog":
        s = Supervisor()
        s.call("send", name="a", text="this needs input")
        until("a", "needs input")
        time.sleep(0.6)
        notified()
        user("a", "1")
        until("a", "done", "Done: this needs input")
        notified()
    elif group == "mixed":
        s = Supervisor()
        user("a", "slow job")
        until("a", "working")
        s.call("send", name="a", text="check the logs")
        user("a", "go on")
        until("a", "done", "Answered: Message from the user (hq send): check the logs")
        notified()
        print()
        s.call("send", name="a", text="slow work")
        until("a", "working")
        user("a", "go on")
        until("a", "done", "Done: slow work")
        notified()
        user("a", "and thanks")
        until("a", "done", "Done: and thanks")
        notified()
    elif group == "background":
        s = Supervisor()
        s.call("send", name="a", text="background check")
        time.sleep(1)
        until("a", "working")
        notified()
        user("a", "wake")
        until("a", "done", "The background work is done, 0 still running.")
        notified()
    elif group == "ask":
        s = Supervisor()
        s.call("send", name="a", text="a question for you")
        until("a", "question")
        notified()
        ls()
    elif group == "shell":
        shell("new", "b", APP, "hello")
        until("b", "done", "Done: hello")
        notified()
        shell("send", "b", "again")
        until("b", "done", "Done: again")
        notified()
        ls()
    print(f"{D}-- end of {group} --{Z}")


main(sys.argv[1])
