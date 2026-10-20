#!/bin/bash
# stress.sh HQ DIR: Claude Code saves 12 servers while hq rewrites the same .claude.json in a loop; prints what was lost.
hq=$1 dir=$2
rm -rf "$dir"; mkdir -p "$dir"; echo '{}' > "$dir/.claude.json"
export CLAUDE_CONFIG_DIR=$dir HQ_PLATFORM=native
( i=0; while [ ! -e "$dir/stop" ]; do i=$((i+1)); rm -f "$dir"/.claude.json.hq-backup-*; HQ_TMUX_SOCKET=v$((i%2)) "$hq" mcp install claude-code >/dev/null 2>>"$dir/hq.err"; done; echo "$i" > "$dir/hq-runs" ) &
for n in $(seq 1 12); do timeout 60 claude mcp add-json --scope user "s$n" '{"command":"/bin/true"}' </dev/null >/dev/null 2>&1; done
touch "$dir/stop"; wait
got=$(jq -r '.mcpServers|keys[]' "$dir/.claude.json" | grep -c '^s')
echo "$(basename "$hq"): hq runs $(cat "$dir/hq-runs"), hq errors $(sort -u "$dir/hq.err" | head -3 | tr '\n' ' '), servers kept $got of 12, lost: $(for n in $(seq 1 12); do jq -e ".mcpServers.s$n" "$dir/.claude.json" >/dev/null || printf 's%s ' $n; done)"
