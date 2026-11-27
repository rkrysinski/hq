#!/bin/sh
# usage: shot.sh RUN NAME - renders the run's screen as shots/NAME.png: its text, colours included, with the
# trial folder's path written <trial>, shown in a viewer pane of tmux -L qa364tc and rendered by qa-screenshot.sh,
# which checks it for private details first (T-c's, copied)
TC=$(cd "$(dirname "$0")" && pwd)
WT=<worktree>
"$TC/tm.sh" capture-pane -e -p -t "=$1:" | sed "s|$TC|<trial>|g; s|/tmp/claude-[0-9]*/[^ ]*|<scratchpad>|g" > "$TC/shots-raw/$2.ans"
tmux -L qa364tc has-session -t v 2>/dev/null || tmux -L qa364tc new-session -d -s v -x 170 -y 50 "sleep 86400"
tmux -L qa364tc respawn-pane -k -t v "cat '$TC/shots-raw/$2.ans'; sleep 86400"
sleep 1
"$WT/scripts/qa-screenshot.sh" -L qa364tc v "$TC/shots/$2.png"
