#!/bin/bash
# 450 runs: three test processes of 150 at once, every core kept busy by a busy loop of its own.
cores=$(nproc)
for i in $(seq "$cores"); do sh -c 'while :; do :; done' & busy+=($!); done
trap 'kill "${busy[@]}" 2>/dev/null' EXIT
echo "cores: $cores, busy processes: ${#busy[@]}, started $(date -u +%H:%M:%SZ)"
for p in 1 2 3; do
  go test -tags integration -count=150 -run 'TestHomeWindowKeepsOptionsArgumentsAndOutputAfterExit$' ./internal/tmux/ > "$Q/run$p.txt" 2>&1 &
  runs+=($!)
done
rc=0; for r in "${runs[@]}"; do wait "$r" || rc=1; done
echo "ended $(date -u +%H:%M:%SZ), load average: $(cut -d' ' -f1-3 /proc/loadavg)"
for p in 1 2 3; do echo "process $p:"; tail -2 "$Q/run$p.txt"; done
exit $rc
