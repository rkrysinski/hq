#!/bin/sh
# QA #74, checks 1-3: full integration runs, the socket files they leave.
S=$(dirname "$0")/..
W=$1
t=$(mktemp -d /tmp/hqqa.XXXXXX)
echo "== 1-before. dev ($(git -C "$S/dev" rev-parse --short HEAD)), TMUX_TMPDIR=$t/dev"
mkdir -p "$t/dev"; TMUX_TMPDIR=$t/dev "$S/qa/sockets.sh" "$S/dev"
echo "== 1. this branch ($(git -C "$W" rev-parse --short HEAD)), TMUX_TMPDIR unset"
env -u TMUX_TMPDIR "$S/qa/sockets.sh" "$W"
echo "== 2. this branch, TMUX_TMPDIR=$t/br"
mkdir -p "$t/br"; TMUX_TMPDIR=$t/br "$S/qa/sockets.sh" "$W"
echo "   files left in $t/br/tmux-$(id -u): $(ls "$t/br/tmux-$(id -u)" 2>/dev/null | wc -l | tr -d ' ')"
rm -rf "$t"
