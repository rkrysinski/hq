#!/usr/bin/env python3
"""usage: metrics.py RUN - for each hq wait call of the run: when it was called, when Claude Code moved it to the
background, when its result came, how long it lasted, and the model requests between the move and the result."""
import glob, json, os, sys
from datetime import datetime
TB = os.path.dirname(os.path.abspath(__file__))
def sec(x): return datetime.strptime(x, "%Y-%m-%dT%H:%M:%S.%fZ").timestamp()
run = sys.argv[1]
reqs, calls, moved, results = [], [], [], []
for f in glob.glob(os.path.join(TB, "cfg", "projects", "*runs-%s-work" % run, "*.jsonl")):
    for l in open(f):
        e = json.loads(l); t, ts = e.get("type"), e.get("timestamp")
        if t == "api-request": reqs.append(ts)
        c = e.get("message", {}).get("content") if t in ("user", "assistant") else None
        if isinstance(c, list):
            for x in c:
                if x.get("type") == "tool_use" and x.get("name") == "mcp__hq__wait": calls.append((ts, json.dumps(x.get("input"))))
                if x.get("type") == "tool_result" and "moved to the background" in json.dumps(x.get("content")): moved.append(ts)
        if t == "queue-operation" and e.get("operation") == "enqueue" and "hq/wait" in str(e.get("content")): results.append(ts)
reqs.sort()
print("run %s: %d model requests in all" % (run, len(reqs)))
for (ts, args), mv, res in zip(calls, moved, results):
    between = [r for r in reqs if sec(mv) + 1 < sec(r) < sec(res)]
    after_move = [r for r in reqs if sec(mv) <= sec(r) <= sec(mv) + 1]
    after_res = [r for r in reqs if sec(res) <= sec(r) <= sec(res) + 2]
    print("wait %s" % args)
    print("  called          %s" % ts)
    print("  moved to the background %s (%.1f s after the call); model requests within 1 s of it: %d" % (mv, sec(mv) - sec(ts), len(after_move)))
    print("  result          %s (%.1f s = %.1f min after the call); model requests within 2 s of it: %d" % (res, sec(res) - sec(ts), (sec(res) - sec(ts)) / 60, len(after_res)))
    print("  model requests between the move and the result: %d %s" % (len(between), between))
