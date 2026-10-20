#!/bin/bash
# d.sh LABEL CMD ...   drive the #70 QA for LABEL (before|after)
#   start                 outer terminal (tmux -L qa-70-LABEL) running a shell set up by rc.sh
#   type TEXT             type TEXT + Enter in the outer terminal
#   keys KEYS...          send keys to the outer terminal
#   akeys AGENT KEYS...   send keys to an agent's own pane (hq's private server)
#   apane AGENT           print an agent's pane
#   ls                    hq ls --json, compact
#   shot NAME             screenshot of the outer terminal (png + txt) into out/
#   ashot AGENT NAME      screenshot of an agent's pane
Q=/tmp/claude-1000/-home-dev-work-repositories-hq/00000031-0000-4000-8000-000000000031/scratchpad/qa70
W=/home/dev/work/repositories/hq/.claude/worktrees/issue-70-dialog-verdict
L=$1; shift; cmd=$1; shift
case $L in before) ROOT=$Q/before-src ;; *) ROOT=$W ;; esac
SOCK=hq-dev-$(printf %s "$ROOT" | cksum | cut -d' ' -f1)
T="tmux -u -L qa-70-$L"
pane() { tmux -u -L $SOCK list-panes -a -F '#{@hq_name} #{pane_id}' | awk -v n="$1" '$1==n{print $2; exit}'; }
case $cmd in
start)
  $T kill-server 2>/dev/null; tmux -L $SOCK kill-server 2>/dev/null; rm -rf $ROOT/.local-dev/config
  $T new-session -d -s qa -x 150 -y 42 -e QA_ROOT=$ROOT -e QA_LABEL=$L "bash --rcfile $Q/rc.sh -i" ;;
type) $T send-keys -t qa "$1" Enter ;;
keys) $T send-keys -t qa "$@" ;;
akeys) p=$(pane "$1"); shift; tmux -u -L $SOCK send-keys -t "$p" "$@" ;;
apane) tmux -u -L $SOCK capture-pane -p -t "$(pane "$1")" ;;
ls) PATH=$Q/bin:$PATH HQ_PLATFORM=native SBX_STUB_DIR=$Q/sbx-$L HQ_TMUX_SOCKET=$SOCK XDG_CONFIG_HOME=$ROOT/.local-dev/config $ROOT/.local-dev/hq ls ;;
hq) PATH=$Q/bin:$PATH HQ_PLATFORM=native SBX_STUB_DIR=$Q/sbx-$L HQ_TMUX_SOCKET=$SOCK XDG_CONFIG_HOME=$ROOT/.local-dev/config $ROOT/.local-dev/hq "$@" ;;
shot) $W/scripts/qa-screenshot.sh -L qa-70-$L qa "$Q/out/$1-$L.png" >/dev/null; $T capture-pane -p -t qa | sed -e :a -e '/^\n*$/{$d;N;ba' -e '}' >"$Q/out/$1-$L.txt" ;;
ashot) p=$(pane "$1"); $W/scripts/qa-screenshot.sh -L $SOCK "$p" "$Q/out/$2-$L.png" >/dev/null; tmux -u -L $SOCK capture-pane -p -t "$p" | sed -e :a -e '/^\n*$/{$d;N;ba' -e '}' >"$Q/out/$2-$L.txt" ;;
esac
