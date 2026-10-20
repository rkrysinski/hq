#!/bin/sh
# check2.sh HQ SOCKET NAME: a message queued behind a draft the user deletes
# without sending is typed in by the next look.
H=$1 S=$2 N=$3; clear
hq() { "$H" "$@"; }
echo "# the user types a draft in $N's box"
tmux -L "$S" send-keys -t "$N" -l "a draft"; sleep 1
printf '$ hq send %s "Reply with the single word: pong"\n' $N
hq send $N "Reply with the single word: pong"
echo "# the user deletes the draft without sending it"
tmux -L "$S" send-keys -t "$N" C-u; sleep 7
echo '$ hq ls --json | jq -c ...   # 7 s later'
hq ls --json | jq -c '.agents[] | {name,state,pending}'
sleep 12
echo '$ hq ls --json | jq -c ...   # once its turn is over'
hq ls --json | jq -c '.agents[] | {name,state,pending,last}'
