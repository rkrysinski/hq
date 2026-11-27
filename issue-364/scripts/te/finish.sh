#!/bin/sh
# usage: finish.sh RUN AGENT - lets the stub agent's slow turn finish (a line typed into its pane in stub environment t364), noting when
TC=$(cd "$(dirname "$0")" && pwd)
id=$(tmux -u -L hq-dev-t364 list-windows -a -F '#{window_id} #{@hq_name}' | awk -v n="$2" '$2 == n { print $1 }')
tmux -u -L hq-dev-t364 send-keys -t "$id" "finish" Enter && echo "$(date -u +%FT%T.%3NZ) finished $2" | tee -a "$TC/runs/$1/keys.log"
