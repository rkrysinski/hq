#!/bin/sh
# usage: shoot.sh NAME COMMAND... - runs COMMAND in a pane of tmux -L qa357r (170 columns, H rows, 60 by default) and renders the pane as shots/NAME.png
# with scripts/qa-screenshot.sh, which checks its text for private details first
cd "$(dirname "$0")"; mkdir -p shots
name=$1; shift
tmux -L qa357r has-session -t v 2>/dev/null || tmux -L qa357r new-session -d -s v -x 170 -y ${H:-60} "sleep 86400"
tmux -L qa357r respawn-pane -k -t v "cd '$PWD' && $*; sleep 86400"
sleep 2
<worktree>/scripts/qa-screenshot.sh -L qa357r v "shots/$name.png"
