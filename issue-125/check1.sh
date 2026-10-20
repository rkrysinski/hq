#!/bin/sh
# check1.sh HQ NAME: two sends in a row to a fresh agent, then ls 8 s later.
H=$1 N=$2; clear
hq() { "$H" "$@"; }
printf '$ hq send %s "/model claude-opus-5-5"; hq send %s "/effort medium"\n' $N $N
hq send $N "/model claude-opus-5-5"; hq send $N "/effort medium"
sleep 8
echo '$ hq ls --json | jq -c ...   # 8 s later'
hq ls --json | jq -c '.agents[] | {name,state,pending}'
