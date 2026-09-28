#!/bin/sh
# Render a tmux pane, colours included, as a terminal screenshot for QA
# evidence (docs/agents/qa-evidence.md).
#
#   scripts/qa-screenshot.sh [-L SOCKET] [-b RRGGBB] [-f RRGGBB] TARGET OUT.png
#
# SOCKET is the tmux server (tmux -L), e.g. the one scripts/local-dev.sh
# exports as HQ_TMUX_SOCKET or an outer server used as the QA terminal; TARGET
# is the pane (tmux -t). -b and -f are the terminal's own background and
# text colour (default 171717 and freeze's own), for a look that depends on
# them. Trailing blank lines are dropped.
# Needs only Go: the renderer (charmbracelet/freeze) is run at a pinned
# version.
set -eu

FREEZE=github.com/charmbracelet/freeze@v0.2.2

socket=
bg=171717
fg=
if [ "${1:-}" = -L ]; then socket=$2; shift 2; fi
if [ "${1:-}" = -b ]; then bg=$2; shift 2; fi
if [ "${1:-}" = -f ]; then fg=$2; shift 2; fi
[ $# -eq 2 ] || { echo "usage: $0 [-L SOCKET] [-b RRGGBB] [-f RRGGBB] TARGET OUT.png" >&2; exit 1; }

ansi=$(mktemp)
trap 'rm -f "$ansi"' EXIT
# freeze ignores SGR 49 (default background), which would carry a
# background to the end of the line: it becomes the terminal's background,
# and with -f SGR 39 the terminal's text colour, a reset both.
# -N keeps the blanks at a line's end, whose background can be the point.
# tmux carries a line's colours on from the line before and freeze starts
# each line afresh, so each line starts with the sequences still in force.
tmux ${socket:+-L "$socket"} capture-pane -e -p -N -t "$1" |
    sed -e :a -e '/^\n*$/{$d;N;ba' -e '}' |
    BG=$bg FG=$fg perl -pe '
        BEGIN {
            sub rgb { my ($p, $c) = @_; sprintf "\e[%s;2;%d;%d;%dm", $p, map { hex } $c =~ /(..)(..)(..)/ }
            $bg = rgb(48, $ENV{BG}); $fg = $ENV{FG} ne "" ? rgb(38, $ENV{FG}) : "";
            $sgr = $fg . $bg;
        }
        s/\e\[49m/$bg/g; s/\e\[39m/$fg/g if $fg ne ""; s/\e\[0?m/\e[0m$fg$bg/g;
        $in = $sgr;
        for (/(\e\[[0-9;]*m)/g) { $sgr = /^\e\[0m$/ ? "" : $sgr . $_ }
        $_ = $in . $_' >"$ansi"
go run "$FREEZE" --language ansi --background "#$bg" --window --padding 20,30 --font.size 14 -o "$2" "$ansi" </dev/null >/dev/null
echo "$2"
