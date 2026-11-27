#!/bin/sh
# usage: shoot.sh NAME COMMAND... - runs COMMAND in a pane of tmux -L qa364 (170 columns) and renders the pane as shots/NAME.png
# with scripts/qa-screenshot.sh, which checks its text for private details first
cd "$(dirname "$0")"; mkdir -p shots
name=$1; shift
tmux -L qa364 has-session -t v 2>/dev/null || tmux -L qa364 new-session -d -s v -x 170 -y 60 "sleep 86400"
tmux -L qa364 respawn-pane -k -t v "cd '$PWD' && $*; sleep 86400"
sleep 2
<worktree>/scripts/qa-screenshot.sh -L qa364 v "shots/$name.png"
