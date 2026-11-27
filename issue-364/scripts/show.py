#!/usr/bin/env python3
"""usage: show.py TITLE FILE... - for a screenshot: TITLE, then each qac.py log as summarize.py gives it,
with the polls answered working between the first and the last folded into one line, and other lines as
they are."""
import subprocess, sys
print(sys.argv[1])
for f in sys.argv[2:]:
    out = subprocess.run([sys.executable, "summarize.py", f], capture_output=True, text=True).stdout.splitlines()
    run = []
    def flush():
        if len(run) > 2:
            print(run[0]); print("      ... %d more tasks/get answered working ..." % (len(run) - 2)); print(run[-1])
        else:
            for l in run: print(l)
        run.clear()
    for l in out:
        if ">> tasks/get" in l:
            continue
        if l.rstrip().endswith(" working") and "<< task " in l:
            run.append(l); continue
        flush(); print(l)
    flush()
