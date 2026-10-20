#!/bin/sh
# k.sh SOCKET NAME MARK KEYS...: sends keys and logs a mark.
sock=$1 name=$2 mark=$3; shift 3
echo "$(date +%s.%3N) KEY $name $mark" >> /tmp/claude-1000/-home-dev-work-repositories-hq/0000001d-0000-4000-8000-00000000001d/scratchpad/spike/events.log
CAPTURE_SOCKET=$sock /home/dev/work/repositories/hq/tools/capture/capture.sh keys $name "$@"
