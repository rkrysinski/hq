#!/bin/sh
# Check 6, nearby and unchanged: an unknown id and one without the declaration refused; a valid id cancelled
# twice at once, acknowledged twice, still working; a change within the hold returned in the call (b3 let
# finish 15 s in); Claude Desktop's wait 50 s with no handle though the request declares tasks
./qac.py get:nope get:w1 notasks "get:$(cat s1.task)" -- ./hqmcp.sh claude-code
QAC_TASK=$(cat s1.task) ./qac.py cancel2 get -- ./hqmcp.sh claude-code
./typein.sh hq-dev-t362 b3 finish 15 &
./qac.py 'call:list:{"names":["b3"]}' 'call:wait:{"names":["b3"],"since":"$since"}' -- ./hqmcp.sh claude-code
wait
./qac.py 'call:list:{"names":["b2"]}' 'call:wait:{"names":["b2"],"since":"$since"}' -- ./hqmcp.sh claude-desktop
