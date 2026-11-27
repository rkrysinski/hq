#!/bin/sh
# Inside the trial's tmux pane: Claude Code for one run, with the run's hq entry and a debug file (T-c's, for T-d of #363).
TD=$(cd "$(dirname "$0")" && pwd); R=$TD/runs/$1; shift
cd "$R/work" || exit 1
echo "$(date -u +%FT%T.%3NZ) launch pid $$ args: $*" >> "$R/launch.log"
exec claude --settings '{"disableAllHooks":true,"promptSuggestionEnabled":false}' --model haiku --permission-mode bypassPermissions \
  --strict-mcp-config --mcp-config "$R/mcp.json" --debug-file "$R/debug.txt" "$@"
