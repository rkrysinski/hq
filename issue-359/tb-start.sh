#!/bin/sh
# usage: start.sh RUN [claude arguments] - starts the run's Claude Code in the trial's tmux server, with the hq entry tb/entry.json as its MCP config
TB=$(cd "$(dirname "$0")" && pwd); RUN=$1; R=$TB/runs/$RUN; shift
mkdir -p "$R/work"
cp "$TB/entry.json" "$R/mcp.json"
"$TB/clean_env.sh" env CLAUDE_CONFIG_DIR="$TB/cfg" tmux -L tb359 new-session -d -s "$RUN" -x 170 -y 50 -c "$R/work" "sh $TB/launch.sh $RUN $*; echo CLAUDE-EXITED \$?; sleep 86400"
