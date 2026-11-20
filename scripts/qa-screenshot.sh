#!/bin/sh
# Render a tmux pane, colours included, as a terminal screenshot for QA
# evidence (docs/hq/qa-evidence.md).
#
#   scripts/qa-screenshot.sh [-L SOCKET] [-b RRGGBB] [-f RRGGBB] TARGET OUT.png
#
# SOCKET is the tmux server (tmux -L), e.g. the one scripts/local-dev.sh
# exports as HQ_TMUX_SOCKET or an outer server used as the QA terminal; TARGET
# is the pane (tmux -t). -b and -f are the terminal's own background and
# text colour (default 171717 and freeze's own), for a look that depends on
# them. Trailing blank lines are dropped.
# Nothing is rendered from a pane whose text has not passed the check for
# private details (#277): an image is checked by no gate later. The gate of
# the checkout this script belongs to (tools/testgate private,
# docs/hq/testing.md, Private details) reads the rows the image shows and the
# pane's lines with wrapped rows joined, so that a detail split over two rows
# is seen too. On a match, without a pattern list, or when the check itself
# fails, the script exits 1 and writes no OUT.png; the gate names the row
# (image-rows) or line (pane-lines) and the pattern's number, never the text.
# Needs only Go: the renderer (charmbracelet/freeze) is run at a pinned
# version.
set -eu

FREEZE=github.com/charmbracelet/freeze@v0.2.2
root=$(cd "$(dirname "$0")/.." && pwd)

socket=
bg=171717
fg=
if [ "${1:-}" = -L ]; then socket=$2; shift 2; fi
if [ "${1:-}" = -b ]; then bg=$2; shift 2; fi
if [ "${1:-}" = -f ]; then fg=$2; shift 2; fi
[ $# -eq 2 ] || { echo "usage: $0 [-L SOCKET] [-b RRGGBB] [-f RRGGBB] TARGET OUT.png" >&2; exit 1; }

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
# freeze ignores SGR 49 (default background), which would carry a
# background to the end of the line: it becomes the terminal's background,
# and with -f SGR 39 the terminal's text colour, a reset both.
# -N keeps the blanks at a line's end, whose background can be the point.
# tmux carries a line's colours on from the line before and freeze starts
# each line afresh, so each line starts with the sequences still in force.
# The shifts tmux writes around line drawing for a client that does not take
# UTF-8 (SO, SI) are dropped: freeze cannot render them.
tmux -u ${socket:+-L "$socket"} capture-pane -e -p -N -t "$1" |
    sed -e :a -e '/^\n*$/{$d;N;ba' -e '}' |
    BG=$bg FG=$fg perl -pe '
        BEGIN {
            sub rgb { my ($p, $c) = @_; sprintf "\e[%s;2;%d;%d;%dm", $p, map { hex } $c =~ /(..)(..)(..)/ }
            $bg = rgb(48, $ENV{BG}); $fg = $ENV{FG} ne "" ? rgb(38, $ENV{FG}) : "";
            $sgr = $fg . $bg;
        }
        tr/\x0e\x0f//d;
        s/\e\[49m/$bg/g; s/\e\[39m/$fg/g if $fg ne ""; s/\e\[0?m/\e[0m$fg$bg/g;
        $in = $sgr;
        for (/(\e\[[0-9;]*m)/g) { $sgr = /^\e\[0m$/ ? "" : $sgr . $_ }
        $_ = $in . $_' >"$tmp/ansi"
# The text the image shows: the escape sequences of the colours dropped.
perl -pe 's/\e\[[0-?]*[ -\/]*[@-~]//g' "$tmp/ansi" >"$tmp/image-rows"
tmux -u ${socket:+-L "$socket"} capture-pane -p -J -t "$1" >"$tmp/pane-lines"
# Built, not go run: go run exits 1 for every failure, and the gate's 2 (an
# error of its own, such as a pattern that does not compile) must not read
# as a match.
gate=0
(cd "$root" && go build -o "$tmp/testgate" ./tools/testgate &&
    "$tmp/testgate" private "$tmp/image-rows" "$tmp/pane-lines") \
    >"$tmp/gate" 2>"$tmp/gate-err" </dev/null || gate=$?
TMP=$tmp perl -pe 's/\Q$ENV{TMP}\E\///g' "$tmp/gate" >&2
case $gate in
0) grep -q '^private: [1-9][0-9]* patterns' "$tmp/gate" || {
    echo "qa-screenshot: $2 not rendered: no pattern list, so the pane cannot be checked for private details: set HQ_PRIVATE_PATTERNS or put the list in \"\$(git rev-parse --git-common-dir)/info/hq-private-patterns\" (ask the owner for it)" >&2
    exit 1
} ;;
1)
    echo "qa-screenshot: $2 not rendered: the pane shows a private detail: take it out of the pane (a neutral directory, a placeholder) and run again" >&2
    exit 1 ;;
*)
    cat "$tmp/gate-err" >&2
    echo "qa-screenshot: $2 not rendered: the pane could not be checked for private details" >&2
    exit 1 ;;
esac
go run "$FREEZE" --language ansi --background "#$bg" --window --padding 20,30 --font.size 14 -o "$2" "$tmp/ansi" </dev/null >/dev/null
echo "$2"
