#!/bin/sh
# usage: start.sh RUN [claude arguments] - starts the run's Claude Code in the trial's tmux server (tmux -L tc364), with the hq entry of entry.json (its server's messages tapped into the run's tap.jsonl)
TC=$(cd "$(dirname "$0")" && pwd); RUN=$1; R=$TC/runs/$RUN; shift
mkdir -p "$R/work"
sed "s|@TAP_LOG@|$R/tap.jsonl|" "$TC/entry.json" > "$R/mcp.json"
"$TC/clean_env.sh" env CLAUDE_CONFIG_DIR="$TC/cfg" tmux -L tc364 new-session -d -s "$RUN" -x 170 -y 50 -c "$R/work" "sh $TC/launch.sh $RUN $*; echo CLAUDE-EXITED \$?; exec bash"
