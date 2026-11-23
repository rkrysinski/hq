#!/bin/sh
# For each hq command, how often hq reads the journals' counter (opens the
# state directory's counter file) and lists tmux's windows (tmux
# list-windows), traced with strace -f; the build local-dev.sh runs first
# is traced too and does neither. $1: the checkout, $2: after or before,
# LOCAL_DEV_BEFORE naming the commit for before.
co=$1; which=$2
flag=""; [ "$which" = before ] && flag=--before
cd "$($co/scripts/local-dev.sh --stub native --stub-env)/repo" || exit 1
run() { "$co/scripts/local-dev.sh" --stub native $flag "$@"; }
run ls >/dev/null 2>&1; run ls >/dev/null 2>&1   # nothing left to append
for args in "ls" "ls --json" "__chord next" "read a --json" "explain a"; do
  t=$(mktemp)
  strace -f -qq -e trace=openat,execve -o "$t" "$co/scripts/local-dev.sh" --stub native $flag $args >/dev/null 2>&1
  counter=$(grep -c 'openat(.*/hq/counter"' "$t")
  windows=$(grep -c 'execve(.*"list-panes"' "$t")
  printf '%-16s counter read %d, windows listed %d\n' "hq $args" "$counter" "$windows"
  rm -f "$t"
done
