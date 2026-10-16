#!/bin/sh
# QA #74: a full integration run, then the hq-test-<pid>-* socket files of
# that run's own test binaries (pids recorded by pid-exec.sh) in tmux's
# socket directory ($TMUX_TMPDIR or /tmp, then tmux-<uid>), before and after.
# Usage: sockets.sh WORKTREE [go test args...]
cd "$1"; shift
here=$(dirname "$0")
dir=${TMUX_TMPDIR:-/tmp}/tmux-$(id -u)
QA_PIDS=$(mktemp); export QA_PIDS
count() { n=0; for p in $(cat "$QA_PIDS"); do n=$((n + $(ls "$dir" 2>/dev/null | grep -c "^hq-test-$p-"))); done; echo $n; }
echo "TMUX_TMPDIR=${TMUX_TMPDIR:-(unset)}  socket dir: $dir"
echo "all hq-test-* files in it before: $(ls "$dir" 2>/dev/null | grep -c '^hq-test-')"
log=$(mktemp)
go test -count=1 -exec "$here/pid-exec.sh" "${@:--tags=integration}" ./... >"$log" 2>&1
rc=$?
echo "go test ${*:--tags=integration} ./...: exit $rc, $(grep -c '^ok' "$log") packages ok"
grep -E '^(FAIL|--- FAIL)' "$log" | head
echo "test binaries (pids): $(wc -l <"$QA_PIDS" | tr -d ' ')"
echo "hq-test-<pid>-* of this run's pids, after: $(count)"
echo "all hq-test-* files in it after: $(ls "$dir" 2>/dev/null | grep -c '^hq-test-')"
