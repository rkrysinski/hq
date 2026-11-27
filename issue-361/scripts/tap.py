#!/usr/bin/env python3
"""usage: tap.py CMD [ARGS...] - runs CMD as an MCP server over standard input
and output, passing every line through unchanged, and writes each line with
its time and direction (> to the server, < from it, ! the tap's own notes:
start, signals, exit) to the JSON lines file $TAP_LOG."""
import json, os, signal, subprocess, sys, threading, time

log = open(os.environ["TAP_LOG"], "a", buffering=1)
lock = threading.Lock()


def note(d, line):
    with lock:
        log.write(json.dumps({"t": time.time(), "pid": os.getpid(), "dir": d, "line": line}) + "\n")


p = subprocess.Popen(sys.argv[1:], stdin=subprocess.PIPE, stdout=subprocess.PIPE)
note("!", "start %s" % " ".join(sys.argv[1:]))


def out():
    for line in p.stdout:
        note("<", line.decode(errors="replace").rstrip("\n"))
        sys.stdout.buffer.write(line)
        sys.stdout.buffer.flush()
    rc = p.wait()
    note("!", "exit %d" % rc)
    os._exit(0 if rc < 0 else rc)


threading.Thread(target=out, daemon=True).start()


def forward(sig, _):
    note("!", "signal %d" % sig)
    p.send_signal(sig)


signal.signal(signal.SIGINT, forward)
signal.signal(signal.SIGTERM, forward)
try:
    for line in sys.stdin.buffer:
        note(">", line.decode(errors="replace").rstrip("\n"))
        p.stdin.write(line)
        p.stdin.flush()
    note("!", "input ended")
    p.stdin.close()
except BrokenPipeError:
    pass
p.wait()
time.sleep(5)
