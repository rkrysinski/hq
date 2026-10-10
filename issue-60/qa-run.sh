#!/bin/bash
# qa-run.sh HQ OUT: plays the issue #60 checks against hq binary HQ, evidence to OUT.
W=/Users/romankrysinski/work/repositories/hq/.claude/worktrees/issue-60-subagent-shell-counts
S=$(dirname "$0"); HQ=$1; OUT=$2; mkdir -p "$OUT"
. "$S/qa-env.sh"
T="tmux -L qa-60-term"
rm -rf "$SBX_STUB_DIR" "$S/repo" "$FAKE_CLAUDE_NOTIFIED"; mkdir -p "$S/repo" "$SBX_STUB_DIR"
git -C "$S/repo" init -q -b main && git -C "$S/repo" commit -q --allow-empty -m init
tmux -L $HQ_TMUX_SOCKET kill-server 2>/dev/null; $T kill-server 2>/dev/null
$T -f /dev/null new-session -d -x 150 -y 30 -s term "bash --noprofile --norc"
$T send-keys -t term ". $S/qa-env.sh; alias hq=$HQ; cd $S/repo; clear" Enter; sleep 1
# term CMD: type CMD in the QA terminal, wait for it
term() { $T send-keys -t term "$1" Enter; sleep "${2:-2}"; }
log() { printf '%s %s\n' "$(date +%T)" "$*" >>"$OUT/timeline.txt"; }
type_in() { tmux -L $HQ_TMUX_SOCKET send-keys -t "hq:$1" "$2" Enter; log "typed in $1: $2"; sleep 2; }
shot() { $T capture-pane -p -t term >"$OUT/$1.txt"; "$W/scripts/qa-screenshot.sh" -L qa-60-term term "$OUT/$1.png" >/dev/null 2>&1; }
ls_() { log "hq ls: $($HQ ls 2>&1 | grep -E "^ *$1 " | tr -s ' ')"; }
: >"$OUT/timeline.txt"
# 1 + 4: a subagent waits on sleep 180
term "clear; hq new a1 . 'start one background subagent'" 4
ls_ a1
( $HQ wait a1 --timeout 0 >"$OUT/wait.txt" 2>&1; log "hq wait a1 returned: $(cat "$OUT/wait.txt")" ) &
sleep 1
type_in a1 "detach sleep 180"; ls_ a1
term "clear; hq ls" ; shot 1-subagent-waits
type_in a1 "wake"; ls_ a1
term "clear; hq ls"; shot 1-closing-turn
# 2: a subagent leaves a server running
term "clear; hq new a2 . 'start one background subagent'" 4
type_in a2 "detach python3 -m http.server"; ls_ a2
term "clear; hq ls; hq read a2"; shot 2-server-left-running
type_in a2 "wake"; ls_ a2
term "clear; hq ls"; shot 2-server-killed
# 3: the agent's own background command
term "clear; hq new a3 . 'start a server'" 4
ls_ a3
term "clear; hq ls"; shot 3-own-command
sleep 1
cp "$FAKE_CLAUDE_NOTIFIED" "$OUT/notified.txt"
$HQ stop -y >/dev/null 2>&1
tmux -L $HQ_TMUX_SOCKET kill-server 2>/dev/null; $T kill-server 2>/dev/null
