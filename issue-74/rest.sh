#!/bin/sh
# QA #74, checks 3-6.
S=$(dirname "$0")/..
W=$1; cd "$W"
echo "== 1-again. this branch, TMUX_TMPDIR unset"
env -u TMUX_TMPDIR "$S/qa/sockets.sh" "$W"
echo "== 3. a test that never starts a server"
go test -count=1 -v -tags integration -run 'TestATestThatNeverStartsAServerCleansUpWithoutError' ./internal/testutil/ 2>&1
echo "== 4. end-to-end journeys (TmuxSocket's other users)"
env -u TMUX_TMPDIR "$S/qa/sockets.sh" "$W" -tags=e2e
echo "== 5. scripts/local-dev.sh --kill-server"
sock="hq-dev-$(printf %s "$W" | cksum | cut -d' ' -f1)"
tmux -L "$sock" new-session -d -s qa 'sleep 60'
path=$(tmux -L "$sock" display-message -p '#{socket_path}')
ls -l "$path" | cut -c1-10,40-
scripts/local-dev.sh --kill-server
ls -l "$path" 2>&1
