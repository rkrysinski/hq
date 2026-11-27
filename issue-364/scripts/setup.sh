#!/bin/sh
# Starts the agents of the checks in stub environment t364: e1, e5, e6, e8 at their prompt after a short first
# turn; e2, e3, e4, e7, e9, e10 working on a slow job until a line typed into their pane lets them finish
cd "$(dirname "$0")"
for n in e1 e5 e6 e8; do ./hq.sh new $n /tmp/hq-stub-t364/repo "hello from $n"; done
for n in e2 e3 e4 e7 e9 e10; do ./hq.sh new $n /tmp/hq-stub-t364/repo "slow job of $n"; done
sleep 4; ./hq.sh ls
