#!/usr/bin/env python3
"""usage: metrics.py RUN - the run's hq wait calls and wait tasks as Claude
Code and hq exchanged them (tap.jsonl), with the model requests of the run's
transcripts (api-request records) around them: when each wait was called and
answered (a task handle or a result), each task's polls (tasks/get) with
their gaps and how long hq took to answer them, its completion, the model
requests between the handle and the completion and right after it, and how
long after the agent finished (keys.log) the task completed."""
import glob, json, os, statistics, sys
from datetime import datetime, timezone

TC = os.path.dirname(os.path.abspath(__file__))
run = sys.argv[1]
R = os.path.join(TC, "runs", run)


def iso(t):
    return datetime.fromtimestamp(t, timezone.utc).strftime("%H:%M:%S.%f")[:-3] + "Z"


def sec(x):
    return datetime.strptime(x, "%Y-%m-%dT%H:%M:%S.%fZ").replace(tzinfo=timezone.utc).timestamp()


reqs, calls, polls, cancels, procs = {}, [], {}, [], []
for l in open(os.path.join(R, "tap.jsonl")):
    e = json.loads(l)
    if e["dir"] == "!":
        procs.append((e["t"], e["pid"], e["line"].split(" /")[0]))
        continue
    try:
        m = json.loads(e["line"])
    except ValueError:
        continue
    key = (e["pid"], m.get("id"))
    if e["dir"] == ">" and "method" in m:
        reqs[key] = (e["t"], m["method"], m.get("params") or {})
    elif e["dir"] == "<" and key in reqs:
        t0, method, params = reqs[key]
        res = m.get("result") or {}
        if method == "tools/call" and params.get("name") == "wait":
            calls.append({"at": t0, "answered": e["t"], "args": params.get("arguments"), "pid": e["pid"], "res": res})
        elif method == "tasks/get":
            status = res.get("status") or "error %s" % (m.get("error") or {}).get("code")
            polls.setdefault(params.get("taskId"), []).append((t0, e["t"], status, e["pid"], res))
        elif method == "tasks/cancel":
            cancels.append((t0, params.get("taskId"), "error" if "error" in m else "acknowledged"))
api, turns = [], []
for f in glob.glob(os.path.join(TC, "cfg", "projects", "*runs-%s-work" % run, "*.jsonl")):
    for l in open(f):
        e = json.loads(l)
        if e.get("type") == "api-request":
            api.append(sec(e["timestamp"]))
        if e.get("type") == "system" and e.get("subtype") == "turn_duration":
            turns.append(sec(e["timestamp"]))
api.sort()
turns.sort()
keys = []
if os.path.exists(os.path.join(R, "keys.log")):
    for l in open(os.path.join(R, "keys.log")):
        t, _, what = l.strip().partition(" ")
        keys.append((sec(t), what))


def between(a, b):
    return [x for x in api if a <= x < b]


print("run %s: %d hq processes started, %d model requests in the run's transcripts" % (run, sum(1 for p in procs if p[2].startswith("start")), len(api)))
for t, pid, what in procs:
    print("  %s  hq process %d: %s" % (iso(t), pid, what))
for t, what in keys:
    print("  %s  %s" % (iso(t), what))
for c in calls:
    res = c["res"]
    print("wait %s, called %s by process %d" % (json.dumps(c["args"]), iso(c["at"]), c["pid"]))
    if res.get("resultType") != "task":
        print("  answered with a result after %.1f s: %s" % (c["answered"] - c["at"], ((res.get("content") or [{}])[0].get("text") or "").replace("\n", " ")[:160]))
        continue
    tid = res["taskId"]
    print("  answered with a task handle after %.1f s: taskId %s, status %s, ttlMs %s, pollIntervalMs %s" % (c["answered"] - c["at"], tid, res.get("status"), res.get("ttlMs"), res.get("pollIntervalMs")))
    ps = polls.get(tid, [])
    if not ps:
        print("  no tasks/get for it")
        continue
    done = [p for p in ps if p[2] != "working"]
    end = done[0] if done else None
    print("  tasks/get: %d, the first %.3f s after the handle, by processes %s" % (len(ps), ps[0][0] - c["answered"], sorted({p[3] for p in ps})))
    gaps = []
    for a, b in zip(ps, ps[1:]):
        if a[3] == b[3]:
            gaps.append(b[0] - a[0])
    if gaps:
        print("  gaps between tasks/get of one process: min %.2f s, median %.2f s, max %.2f s (%d gaps)" % (min(gaps), statistics.median(gaps), max(gaps), len(gaps)))
    took = [p[1] - p[0] for p in ps]
    print("  hq's time to answer a tasks/get: median %.1f ms, max %.1f ms" % (statistics.median(took) * 1000, max(took) * 1000))
    for p in ps:
        if p[2] != "working" or p is ps[0]:
            pass
    switches = [(a, b) for a, b in zip(ps, ps[1:]) if a[3] != b[3]]
    for a, b in switches:
        print("  process %d's last tasks/get %s, process %d's first %s (%.1f s later)" % (a[3], iso(a[0]), b[3], iso(b[0]), b[0] - a[0]))
    ended = [t for t in turns if t >= c["answered"]]
    turn_end = ended[0] if ended else c["answered"]
    own = between(c["answered"], turn_end)
    print("  the turn that took the handle ended %s, %.1f s after it, with %d model request(s) after the handle %s" % (iso(turn_end), turn_end - c["answered"], len(own), [iso(x) for x in own]))
    if end:
        r = end[4].get("result") or {}
        text = ((r.get("content") or [{}])[0].get("text") or "").replace("\n", " ")
        print("  %s at %s (%.1f s = %.1f min after the call), structuredContent %s, isError %s: %s" % (end[2], iso(end[1]), end[1] - c["at"], (end[1] - c["at"]) / 60, "structuredContent" in r, r.get("isError", False), text[:200]))
        mid = between(turn_end, end[1])
        print("  model requests between the end of that turn and the completion (%.1f s): %d %s" % (end[1] - turn_end, len(mid), [iso(x) for x in mid]))
        nxt = [x for x in api if x >= end[1]]
        if nxt:
            print("  first model request after the completion: %s, %.0f ms after it" % (iso(nxt[0]), (nxt[0] - end[1]) * 1000))
        fin = [t for t, what in keys if what.startswith("finished") and t < end[1]]
        if fin:
            print("  completed %.2f s after the agent was let finish (%s)" % (end[1] - fin[-1], iso(fin[-1])))
    else:
        print("  still working at the last tasks/get, %s" % iso(ps[-1][0]))
for t, tid, what in cancels:
    print("tasks/cancel %s for %s: %s" % (iso(t), tid, what))
