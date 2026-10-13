#!/bin/bash
# qa-run.sh HQ OUT: plays the issue #62 checks against hq binary HQ, evidence to OUT.
W=/Users/romankrysinski/work/repositories/hq/.claude/worktrees/issue-62-monitor-task
S=$(dirname "$0"); HQ=$1; OUT=$2; mkdir -p "$OUT"
. "$S/qa-env.sh"
T="tmux -L qa-62-term"
rm -rf "$SBX_STUB_DIR" "$S/repo" "$FAKE_CLAUDE_NOTIFIED"; mkdir -p "$S/repo" "$SBX_STUB_DIR"
git -C "$S/repo" init -q -b main && git -C "$S/repo" commit -q --allow-empty -m init
tmux -L $HQ_TMUX_SOCKET kill-server 2>/dev/null; $T kill-server 2>/dev/null
$T -f /dev/null new-session -d -x 150 -y 30 -s term "bash --noprofile --norc"
$T send-keys -t term ". $S/qa-env.sh; alias hq=$HQ; cd $S/repo; clear" Enter; sleep 1
# term CMD: type CMD in the QA terminal, wait for it
term() { $T send-keys -t term "$1" Enter; sleep "${2:-2}"; }
log() { printf '%s %s\n' "$(date +%T)" "$*" >>"$OUT/timeline.txt"; }
type_in() { tmux -L $HQ_TMUX_SOCKET send-keys -t "hq:$1" "$2" Enter; log "typed in $1: $2"; sleep 2; }
shot() { $T capture-pane -p -t term >"$OUT/$1.txt"; "$W/scripts/qa-screenshot.sh" -L qa-62-term term "$OUT/$1.png" >/dev/null 2>&1; }
ls_() { log "hq ls: $($HQ ls 2>&1 | grep -E "^ *$1 " | tr -s ' ')"; }
: >"$OUT/timeline.txt"
# 1: the agent publishes an artifact, which arms a watch; hq wait returns once
term "clear; hq new a1 . 'say hi'" 4
ls_ a1
( $HQ wait a1 --timeout 20s >"$OUT/wait.txt" 2>&1; log "hq wait a1 returned: $(cat "$OUT/wait.txt")" ) &
sleep 1
type_in a1 "publish the report"; sleep 3; ls_ a1
term "clear; hq ls" ; shot 1-publish
sleep 20
ls_ a1
log "hq wait a1 again: $($HQ wait a1 --timeout 2s 2>&1 | head -1)"
type_in a1 "a question"; ls_ a1
term "clear; hq ls" ; shot 1-later-question
type_in a1 "go on"; ls_ a1
term "clear; hq ls" ; shot 1-later-done
# 2: the agent keeps a watch itself with the Monitor tool
term "clear; hq new a2 . 'watch the file'" 4
ls_ a2
term "clear; hq ls" ; shot 2-own-watch
# 3: a subagent waits with the Monitor tool (#60 holds)
term "clear; hq new a3 . 'start one background subagent'" 4
type_in a3 "detach monitor"; sleep 3; ls_ a3
term "clear; hq ls; hq read a3"; shot 3-subagent-watch
type_in a3 "wake"; ls_ a3
term "clear; hq ls"; shot 3-closing-turn
# 4: #60 and #38 unchanged: a subagent's command, the agent's own command
term "clear; hq new a4 . 'start one background subagent'" 4
type_in a4 "detach sleep 180"; sleep 3; ls_ a4
term "clear; hq new a5 . 'start a server'" 4
ls_ a5
term "clear; hq ls"; shot 4-unchanged
sleep 1
cp "$FAKE_CLAUDE_NOTIFIED" "$OUT/notified.txt"
$HQ stop -y >/dev/null 2>&1
tmux -L $HQ_TMUX_SOCKET kill-server 2>/dev/null; $T kill-server 2>/dev/null
