#!/bin/sh
# Render a tmux pane, colours included, as a terminal screenshot for QA
# evidence (docs/agents/qa-evidence.md).
#
#   scripts/qa-screenshot.sh [-L SOCKET] TARGET OUT.png
#
# SOCKET is the tmux server (tmux -L), e.g. the one scripts/local-dev.sh
# exports as HQ_TMUX_SOCKET or an outer server used as the QA terminal; TARGET
# is the pane (tmux -t). Trailing blank lines are dropped. Needs only Go: the
# renderer (charmbracelet/freeze) is run at a pinned version.
set -eu

FREEZE=github.com/charmbracelet/freeze@v0.2.2

socket=
if [ "${1:-}" = -L ]; then socket=$2; shift 2; fi
[ $# -eq 2 ] || { echo "usage: $0 [-L SOCKET] TARGET OUT.png" >&2; exit 1; }

ansi=$(mktemp)
trap 'rm -f "$ansi"' EXIT
tmux ${socket:+-L "$socket"} capture-pane -e -p -t "$1" |
    sed -e :a -e '/^\n*$/{$d;N;ba' -e '}' >"$ansi"
go run "$FREEZE" --language ansi --window --padding 20,30 --font.size 14 -o "$2" "$ansi" </dev/null >/dev/null
echo "$2"
