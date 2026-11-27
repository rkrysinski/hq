#!/bin/sh
# Checks 3, 4, 7: a declaring wait on e3 (working) gets a task handle after 50 s; tasks/get works until e3
# finishes (typed in 70 s after the start), then completes with the wait's result; a wait from that
# result's next_since gets a handle again after 50 s (the change is not returned twice); cancelling it
# twice is acknowledged twice and changes nothing (hq ls e3 before and after, the task still working)
./typein.sh hq-dev-t361 e3 finish 72 &
QAC_TASK_FILE=s3.task ./qac.py 'call:list:{"names":["e3"]}' 'call:wait:{"names":["e3"],"since":"$since"}' get poll \
  'call:wait:{"names":["e3"],"since":"$since"}' -- ./hqmcp.sh claude-code
wait
echo "== hq ls e3 before the cancels"; ./hq.sh ls e3
QAC_TASK=$(cat s3.task) ./qac.py cancel2 get -- ./hqmcp.sh claude-code
echo "== hq ls e3 after the cancels"; ./hq.sh ls e3
