#!/usr/bin/env python3
"""usage: summarize.py FILE - each request and answer of a qac.py log in one short line: the
answer's handle (taskId, ttlMs, pollIntervalMs), its status, the agents of a result with
their states and last messages, its next_since, structuredContent and isError, or its error;
other lines as they are."""
import json, sys
for l in open(sys.argv[1]):
    l = l.rstrip("\n")
    if "<< " not in l:
        print(l)
        continue
    head, raw = l.split("<< ", 1)
    m = json.loads(raw)
    if "error" in m:
        print("%s<< error %s %s" % (head, m["error"]["code"], m["error"]["message"]))
        continue
    r = m["result"]
    parts = []
    if r.get("resultType") == "task":
        parts.append("TASK HANDLE taskId %s status %s ttlMs %s pollIntervalMs %s" % (r["taskId"], r["status"], json.dumps(r.get("ttlMs", "absent")), r.get("pollIntervalMs")))
    elif "status" in r:
        parts.append("task %s %s" % (r["taskId"], r["status"]))
    elif r == {"resultType": "complete", "_meta": r.get("_meta")} or set(r) <= {"resultType", "_meta"}:
        parts.append("acknowledged %s" % json.dumps({k: v for k, v in r.items() if k != "_meta"}))
    inner = r.get("result") if "status" in r else r
    if inner and inner.get("content"):
        try:
            w = json.loads(inner["content"][0]["text"])
            ags = ", ".join("%s %s %r" % (a["name"], a["state"], a.get("last")) for a in w.get("agents", []))
            parts.append("result: agents [%s] next_since %s structuredContent %s isError %s" % (ags, w.get("next_since"), "structuredContent" in inner, inner.get("isError", False)))
        except (ValueError, KeyError):
            parts.append("result text: %s isError %s" % (inner["content"][0]["text"][:120], inner.get("isError", False)))
    print("%s<< %s" % (head, "; ".join(parts)))
