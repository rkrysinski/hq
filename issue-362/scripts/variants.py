#!/usr/bin/env python3
"""usage: variants.py ID - the wait task id ID with one change each, one per line, "LABEL ID":
the cursor one back, the agent's name another's (b1 <-> b2), a character of the nonce changed,
the moment one millisecond on, the signature's last character changed (when it has one), a
character added at the end, the last character cut."""
import sys

tid = sys.argv[1]
f = tid.split(":")


def chg(c):
    return "A" if c != "A" else "B"


def out(label, fields):
    print(label, ":".join(fields))


g = list(f); g[1] = "c%d" % (int(f[1][1:]) - 1); out("cursor", g)
g = list(f); g[4] = ",".join({"b1": "b2", "b2": "b1"}.get(n, n) for n in f[4].split(",")); out("names", g)
g = list(f); g[3] = chg(f[3][0]) + f[3][1:]; out("nonce", g)
g = list(f)
digits = "0123456789abcdefghijklmnopqrstuvwxyz"
n = 0
for ch in f[2]:
    n = n * 36 + digits.index(ch)
n += 1
s = ""
while n:
    s = digits[n % 36] + s
    n //= 36
g[2] = s; out("moment", g)
if len(f) > 5:
    g = list(f); g[5] = f[5][:-1] + chg(f[5][-1]); out("signature", g)
print("added", tid + "x")
print("cut", tid[:-1])
