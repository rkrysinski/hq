#!/bin/bash
# usage: loop.sh N OUTDIR  - runs the journey N times, one at a time, keeps failing runs' output
. $S/env.sh
cd /home/rex/hq-qa/src/.claude/worktrees/issue-34-journey-flakes
n=$1; out=$2; mkdir -p "$out"; pass=0; fail=0
echo "commit $(git rev-parse --short HEAD) $(git status --short | tr '\n' ' ')" > "$out/summary.txt"
echo "command: go test -count=1 -tags e2e ./e2e/... -run TestDashboardFollowsAgentsQuitsAndComesBack  (x$n, sequential)" >> "$out/summary.txt"
for i in $(seq 1 $n); do
  if go test -count=1 -tags e2e ./e2e/... -run TestDashboardFollowsAgentsQuitsAndComesBack > "$out/run-$i.txt" 2>&1; then
    pass=$((pass+1)); rm "$out/run-$i.txt"; echo "run $i ok" >> "$out/summary.txt"
  else
    fail=$((fail+1)); echo "run $i FAIL: $(grep -m1 'never\|journey_e2e_test.go' "$out/run-$i.txt")" >> "$out/summary.txt"
  fi
done
echo "total $n pass $pass fail $fail" >> "$out/summary.txt"
