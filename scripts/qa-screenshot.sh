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
# pane's lines with wrapped rows joined, both of the same moment, so that a
# detail split over two rows is seen too. On a match, without a pattern list,
# or when the check itself fails, the script exits 1 and writes no OUT.png;
# on a match the gate names the row (image-rows) or line (pane-lines) and the
# pattern's number, never the text.
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
out=$2

refuse() {
    echo "qa-screenshot: $out not rendered: $*" >&2
    exit 1
}

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
# One tmux command list, so that the rows and the joined lines are the same
# frame of a pane that redraws: the pane's height, then that many rows with
# their colours (the image), then the lines with wrapped rows joined.
tmux -u ${socket:+-L "$socket"} display-message -p -t "$1" '#{pane_height}' \; \
    capture-pane -e -p -N -t "$1" \; capture-pane -p -J -t "$1" >"$tmp/capture"
# The paths through the environment: awk -v would read a backslash in them.
ROWS=$tmp/rows LINES=$tmp/pane-lines awk '
    BEGIN { rows = ENVIRON["ROWS"]; lines = ENVIRON["LINES"]; printf "" >rows; printf "" >lines }
    NR == 1 { height = $0; next }
    NR <= height + 1 { print >rows; next }
    { print >lines }' "$tmp/capture"
# freeze ignores SGR 49 (default background), which would carry a
# background to the end of the line: it becomes the terminal's background,
# and with -f SGR 39 the terminal's text colour, a reset both.
# -N keeps the blanks at a line's end, whose background can be the point.
# tmux carries a line's colours on from the line before and freeze starts
# each line afresh, so each line starts with the sequences still in force.
# The shifts tmux writes around line drawing for a client that does not take
# UTF-8 (SO, SI) are dropped: freeze cannot render them.
sed -e :a -e '/^\n*$/{$d;N;ba' -e '}' "$tmp/rows" |
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

# Built, not go run: go run exits 1 on every failure, and only the gate's
# own 1 is a match (its 2 is an error, such as a pattern that does not
# compile).
(cd "$root" && go build -o "$tmp/testgate" ./tools/testgate) >"$tmp/build" 2>&1 </dev/null || {
    cat "$tmp/build" >&2
    refuse "the gate that checks the pane for private details does not build"
}
gate_exit=0
(cd "$root" && "$tmp/testgate" private "$tmp/image-rows" "$tmp/pane-lines") \
    >"$tmp/gate" 2>"$tmp/gate-err" </dev/null || gate_exit=$?
TMP=$tmp perl -pe 's/\Q$ENV{TMP}\E\///g' "$tmp/gate" >&2
case $gate_exit in
0) grep -q '^private: [1-9][0-9]* patterns' "$tmp/gate" ||
    refuse "no pattern list, so the pane cannot be checked for private details: set HQ_PRIVATE_PATTERNS or put the list in \"\$(git rev-parse --git-common-dir)/info/hq-private-patterns\" (ask the owner for it)" ;;
1) refuse "the pane shows a private detail: take it out of the pane (a neutral directory, a placeholder) and run again" ;;
*)
    cat "$tmp/gate-err" >&2
    refuse "the pane could not be checked for private details" ;;
esac
go run "$FREEZE" --language ansi --background "#$bg" --window --padding 20,30 --font.size 14 -o "$out" "$tmp/ansi" </dev/null >/dev/null
echo "$out"
