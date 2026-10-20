#!/bin/sh
# shot.sh SOCKET NAME OUT: shoots the pane and logs it.
echo "$(date +%s.%3N) SHOT $2 $3" >> /tmp/claude-1000/-home-dev-work-repositories-hq/0000001d-0000-4000-8000-00000000001d/scratchpad/spike/events.log
CAPTURE_SOCKET=$1 /home/dev/work/repositories/hq/tools/capture/capture.sh shot $2 /tmp/claude-1000/-home-dev-work-repositories-hq/0000001d-0000-4000-8000-00000000001d/scratchpad/spike/shots/$3 >/dev/null
cat /tmp/claude-1000/-home-dev-work-repositories-hq/0000001d-0000-4000-8000-00000000001d/scratchpad/spike/shots/$3.txt
