#!/bin/sh
# usage: typein.sh SOCKET NAME LINE [DELAY [LINE2]] - after DELAY seconds, types LINE into agent NAME's pane on tmux server SOCKET, as the user does; given LINE2, it is typed in the same keystrokes, right after LINE
[ -n "$4" ] && sleep "$4"
id=$(tmux -u -L "$1" list-windows -a -F '#{window_id} #{@hq_name}' | awk -v n="$2" '$2 == n { print $1 }')
if [ -n "$5" ]; then
  tmux -u -L "$1" send-keys -t "$id" "$3" Enter "$5" Enter && echo "$(date -u +%H:%M:%SZ) typed '$3' and '$5' into $2"
else
  tmux -u -L "$1" send-keys -t "$id" "$3" Enter && echo "$(date -u +%H:%M:%SZ) typed '$3' into $2"
fi
