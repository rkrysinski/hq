#!/usr/bin/env python3
"""usage: show.py RUN - the run's transcripts in time order: prompts, model requests, tool calls and results, queued notifications.
The run folder's path is shown as <run>."""
import glob, json, os, sys
TB = os.path.dirname(os.path.abspath(__file__))
run = sys.argv[1]
rows = []
for f in glob.glob(os.path.join(TB, "cfg", "projects", "*runs-%s-work" % run, "*.jsonl")):
    for l in open(f):
        e = json.loads(l)
        t, ts = e.get("type"), e.get("timestamp", "")
        if t == "api-request":
            rows.append((ts, "MODEL REQUEST"))
        elif t in ("user", "assistant"):
            c = e.get("message", {}).get("content")
            if isinstance(c, str):
                rows.append((ts, "%s: %s" % (t, c[:400])))
                continue
            for x in c or []:
                k = x.get("type")
                if k == "tool_use":
                    rows.append((ts, "%s tool_use %s %s" % (t, x.get("name"), json.dumps(x.get("input")))))
                elif k == "tool_result":
                    cc = x.get("content")
                    txt = "".join(y.get("text", "") for y in cc) if isinstance(cc, list) else str(cc)
                    rows.append((ts, "%s tool_result %s" % (t, txt.replace("\n", " ")[:500])))
                elif k == "text":
                    rows.append((ts, "%s text %s" % (t, x.get("text", "").replace("\n", " ")[:400])))
        elif t == "queue-operation" and e.get("operation") == "enqueue":
            rows.append((ts, "queued: %s" % str(e.get("content")).replace("\n", " ")[:500]))
        elif t == "system":
            rows.append((ts, "system %s: %s" % (e.get("subtype"), str(e.get("content"))[:200].replace("\n", " "))))
for ts, r in sorted(rows):
    print(ts, r.replace(TB, "<trial>"))
