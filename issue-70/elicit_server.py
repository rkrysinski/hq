#!/usr/bin/env python3
"""Tiny stdio MCP server with one tool, ask_color, that asks the user through an
MCP elicitation (elicitation/create), then works on for WORK_SECONDS before it
returns, so the screen after the answer shows Claude working. No dependencies."""
import json, sys, threading, time

WORK_SECONDS = 45
lock = threading.Lock()
pending = {}  # our request id -> (event, holder)
next_id = [1000]

def send(msg):
    with lock:
        sys.stdout.write(json.dumps(msg) + "\n")
        sys.stdout.flush()

def request(method, params):
    next_id[0] += 1
    rid = next_id[0]
    ev, holder = threading.Event(), {}
    pending[rid] = (ev, holder)
    send({"jsonrpc": "2.0", "id": rid, "method": method, "params": params})
    ev.wait()
    return holder.get("msg")

def call_tool(mid, params):
    resp = request("elicitation/create", {
        "message": "Which color should I use?",
        "requestedSchema": {
            "type": "object",
            "properties": {"color": {"type": "string", "title": "Color",
                                      "enum": ["red", "blue"], "description": "Pick one"}},
            "required": ["color"],
        },
    })
    time.sleep(WORK_SECONDS)
    text = json.dumps(resp.get("result") if resp else None)
    send({"jsonrpc": "2.0", "id": mid, "result": {"content": [{"type": "text", "text": "elicitation answer: " + text}]}})

for line in sys.stdin:
    try:
        msg = json.loads(line)
    except ValueError:
        continue
    if "method" not in msg:  # a response to our request
        p = pending.pop(msg.get("id"), None)
        if p:
            p[1]["msg"] = msg
            p[0].set()
        continue
    m, mid = msg["method"], msg.get("id")
    if m == "initialize":
        send({"jsonrpc": "2.0", "id": mid, "result": {
            "protocolVersion": msg["params"].get("protocolVersion", "2025-06-18"),
            "capabilities": {"tools": {}},
            "serverInfo": {"name": "elicit", "version": "0.1"}}})
    elif m == "tools/list":
        send({"jsonrpc": "2.0", "id": mid, "result": {"tools": [{
            "name": "ask_color",
            "description": "Asks the user to pick a color (via an elicitation form) and returns it.",
            "inputSchema": {"type": "object", "properties": {}}}]}})
    elif m == "tools/call":
        threading.Thread(target=call_tool, args=(mid, msg["params"]), daemon=True).start()
    elif mid is not None:
        send({"jsonrpc": "2.0", "id": mid, "result": {}})
