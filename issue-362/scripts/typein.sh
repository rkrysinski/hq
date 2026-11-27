#!/bin/sh
# usage: typein.sh SOCKET NAME LINE [DELAY] - after DELAY seconds, types LINE into agent NAME's pane on tmux server SOCKET, as the user does
[ -n "$4" ] && sleep "$4"
id=$(tmux -u -L "$1" list-windows -a -F '#{window_id} #{@hq_name}' | awk -v n="$2" '$2 == n { print $1 }')
tmux -u -L "$1" send-keys -t "$id" "$3" Enter && echo "$(date -u +%H:%M:%SZ) typed '$3' into $2"
