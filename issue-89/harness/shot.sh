#!/bin/sh
# shot.sh SOCKET NAME OUT: shoots the pane and logs it.
echo "$(date +%s.%3N) SHOT $2 $3" >> /tmp/claude-1000/-Users-romankrysinski-work-repositories-hq/5be29c0e-761b-4f5f-a874-a65b830ec0ef/scratchpad/spike/events.log
CAPTURE_SOCKET=$1 /Users/romankrysinski/work/repositories/hq/tools/capture/capture.sh shot $2 /tmp/claude-1000/-Users-romankrysinski-work-repositories-hq/5be29c0e-761b-4f5f-a874-a65b830ec0ef/scratchpad/spike/shots/$3 >/dev/null
cat /tmp/claude-1000/-Users-romankrysinski-work-repositories-hq/5be29c0e-761b-4f5f-a874-a65b830ec0ef/scratchpad/spike/shots/$3.txt
