#!/bin/sh
# Capture real Claude Code screens as fixtures for hq's screen readers
# (internal/state/testdata, see its README.md; how to: tools/capture/README.md).
#
#   tools/capture/capture.sh start NAME COLSxROWS DIR [claude args...]
#   tools/capture/capture.sh keys NAME KEYS...
#   tools/capture/capture.sh show NAME
#   tools/capture/capture.sh shot NAME OUT
#   tools/capture/capture.sh mcp DIR
#   tools/capture/capture.sh stop
#
# start runs claude (from PATH) in DIR, in a pane of COLSxROWS on a private
# tmux server, never the user's: CAPTURE_SOCKET, default hq-capture. The
# variables of a Claude Code or hq session the script runs in (CLAUDE*,
# HQ_*; CLAUDE_ENV_FILE stays) are dropped, so claude starts as a fresh
# session.
# keys sends tmux keys (tmux send-keys), show prints the screen, shot saves
# it as hq reads it (capture-pane -p, OUT.txt) and styled (-e, OUT.ansi.txt).
# mcp builds the elicitation MCP server (tools/capture/elicit) into DIR and
# writes DIR/mcp.json for claude --mcp-config. stop ends the server and
# removes its socket.
set -eu

socket=${CAPTURE_SOCKET:-hq-capture}
here=$(cd "$(dirname "$0")" && pwd)
t() { tmux -u -L "$socket" "$@"; }
usage() { sed -n '5,10p' "$0" | sed 's/^# *//' >&2; exit 1; }

[ $# -ge 1 ] || usage
cmd=$1
shift
case $cmd in
start)
	[ $# -ge 3 ] || usage
	name=$1 size=$2 dir=$3
	shift 3
	drop=$(env | sed -n -E 's/^((CLAUDE|HQ_)[A-Za-z0-9_]*)=.*/-u \1/p' | grep -v '^-u CLAUDE_ENV_FILE$' | tr '\n' ' ')
	args=
	for a in "$@"; do args="$args '$(printf '%s' "$a" | sed "s/'/'\\\\''/g")'"; done
	# The pane stays after claude exits, so its last screen can be shot.
	t new-session -d -s "$name" -x "${size%x*}" -y "${size#*x}" -c "$dir" \
		"env $drop TERM=xterm-256color claude $args; sleep 3600"
	;;
keys)
	[ $# -ge 2 ] || usage
	name=$1
	shift
	t send-keys -t "$name" "$@"
	;;
show)
	[ $# -eq 1 ] || usage
	t capture-pane -p -t "$1"
	;;
shot)
	[ $# -eq 2 ] || usage
	t capture-pane -p -t "$1" >"$2.txt"
	t capture-pane -p -e -t "$1" >"$2.ansi.txt"
	echo "$2.txt $2.ansi.txt"
	;;
mcp)
	[ $# -eq 1 ] || usage
	dir=$(cd "$1" && pwd)
	(cd "$here/../.." && go build -o "$dir/elicit" ./tools/capture/elicit)
	printf '{"mcpServers":{"elicit":{"type":"stdio","command":"%s"}}}\n' "$dir/elicit" >"$dir/mcp.json"
	echo "$dir/mcp.json"
	;;
stop)
	path=$(t display-message -p '#{socket_path}' 2>/dev/null || true)
	t kill-server 2>/dev/null || true
	# tmux 3.6 leaves the socket file behind (#74).
	[ -z "$path" ] || rm -f "$path"
	;;
*) usage ;;
esac
