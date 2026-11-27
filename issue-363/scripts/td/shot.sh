#!/bin/sh
# usage: shot.sh RUN NAME - renders the run's screen as shots/NAME.png: its text, colours included, with the trial folder's path
# written <trial>, shown in a viewer pane of tmux -L qa363 and rendered by qa-screenshot.sh, which checks it for private details first
TD=$(cd "$(dirname "$0")" && pwd)
WT=<worktree>
"$TD/tm.sh" capture-pane -e -p -t "=$1:" | sed "s|$TD|<trial>|g; s|/tmp/claude-1000/[^ ]*|<scratchpad>|g" > "$TD/shots-raw/$2.ans"
tmux -L qa363 has-session -t v 2>/dev/null || tmux -L qa363 new-session -d -s v -x 170 -y 50 "sleep 86400"
tmux -L qa363 respawn-pane -k -t v "cat '$TD/shots-raw/$2.ans'; sleep 86400"
sleep 1
"$WT/scripts/qa-screenshot.sh" -L qa363 v "$TD/shots/$2.png"
