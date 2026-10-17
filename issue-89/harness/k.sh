#!/bin/sh
# k.sh SOCKET NAME MARK KEYS...: sends keys and logs a mark.
sock=$1 name=$2 mark=$3; shift 3
echo "$(date +%s.%3N) KEY $name $mark" >> /tmp/claude-1000/-Users-romankrysinski-work-repositories-hq/5be29c0e-761b-4f5f-a874-a65b830ec0ef/scratchpad/spike/events.log
CAPTURE_SOCKET=$sock /Users/romankrysinski/work/repositories/hq/tools/capture/capture.sh keys $name "$@"
