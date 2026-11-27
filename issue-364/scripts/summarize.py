#!/usr/bin/env python3
"""usage: summarize.py FILE - (#361's, copied for #363 with each agent's repo_path and sandbox, and for #364 with
send's delivery, outcome and message, the keys of a result and a tool error's whole text)
each request and answer of a qac.py log in one short line: the
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
            ags = ", ".join("%s %s %r (repo_path %r, sandbox %r)" % (a["name"], a["state"], a.get("last"), a.get("repo_path"), a.get("sandbox")) for a in w.get("agents", []))
            sent = ""
            if "outcome" in w:
                sent = "keys %s delivery %r outcome %s message %r " % (sorted(w), w.get("delivery"), w.get("outcome"), w.get("message"))
            parts.append("result: %sagents [%s] next_since %s structuredContent %s isError %s" % (sent, ags, w.get("next_since"), "structuredContent" in inner, inner.get("isError", False)))
        except (ValueError, KeyError, AttributeError):
            parts.append("result text: %s isError %s" % (inner["content"][0]["text"].strip(), inner.get("isError", False)))
    print("%s<< %s" % (head, "; ".join(parts)))
