#!/bin/sh
# Runs the checks against the running hq, each started 8 s after the one before, so no two local-dev builds overlap
cd "$(dirname "$0")"
./a-after-handoff.sh s1 "/model test-model" > c1a-no-turn.txt 2>&1 &
sleep 8; ./a-after-handoff.sh s2 "unsent please" > c1b-left-in-box.txt 2>&1 &
sleep 8; ./a-after-handoff.sh s3 "hi" draft > c1c-user-draft.txt 2>&1 &
sleep 8; QA_ENV=t357b QA_BEFORE=1 ./a-after-handoff.sh b1 "/model test-model" > c1a-before.txt 2>&1 &
sleep 8; QA_ENV=t357b QA_BEFORE=1 ./a-after-handoff.sh b2 "unsent please" > c1b-before.txt 2>&1 &
sleep 8; ./c4-short-id.sh > c4-short-id.txt 2>&1 &
sleep 8; ./c6-wait-task.sh > c6-wait-task.txt 2>&1 &
sleep 8; ./c2-experimental.sh > c2-experimental.txt 2>&1 &
wait
echo all done
