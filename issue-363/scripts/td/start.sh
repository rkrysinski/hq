#!/bin/sh
# usage: start.sh RUN [claude arguments] - starts the run's Claude Code in the trial's tmux server, with the hq entry of entry.json
# pointing at the run's own copy of the build (runs/RUN/hq, so one run can stand an update in for itself) and its messages tapped into
# the run's tap.jsonl (T-c's, for T-d of #363)
TD=$(cd "$(dirname "$0")" && pwd); RUN=$1; R=$TD/runs/$RUN; shift
mkdir -p "$R/work"
cp "$TD/bin/hq-after" "$R/hq"
sed "s|@TAP_LOG@|$R/tap.jsonl|; s|@HQ@|$R/hq|" "$TD/entry.json" > "$R/mcp.json"
"$TD/clean_env.sh" env CLAUDE_CONFIG_DIR="$TD/cfg" tmux -L td363 new-session -d -s "$RUN" -x 170 -y 50 -c "$R/work" "sh $TD/launch.sh $RUN $*; echo CLAUDE-EXITED \$?; exec bash"
