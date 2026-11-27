#!/bin/sh
# Check 10, nearby, the command line: hq send --wait to e10 (working) says nothing yet after its default 50 s
cd "$(dirname "$0")"
./hq.sh ls e10 >/dev/null
start=$(date +%s.%N); ./hq.sh send --wait e10 "hi"; echo "== hq send --wait e10 returned after $(echo "$(date +%s.%N) - $start" | bc) s (the build already done)"
