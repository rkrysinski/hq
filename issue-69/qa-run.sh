#!/bin/bash
# usage: qa-run.sh LABEL ROOT   - drive hq built from ROOT through the #69 QA scenario
set -u
L=$1 ROOT=$2
S=/tmp/claude-1000/-Users-romankrysinski-work-repositories-hq/a41f9452-666c-4138-8314-b0e3a20745cb/scratchpad
OUT=$S/qa-out; mkdir -p $OUT
SOCK=hq-dev-$(printf %s "$ROOT" | cksum | cut -d' ' -f1)
T="tmux -L qa-69-$L"
shot() { /Users/romankrysinski/work/repositories/hq/.claude/worktrees/issue-69-the-look/scripts/qa-screenshot.sh -L qa-69-$L qa-69-$L "$OUT/$1-$L.png" >/dev/null; $T capture-pane -p -t qa-69-$L | sed -e :a -e '/^\n*$/{$d;N;ba' -e '}' >"$OUT/$1-$L.txt"; }
type() { $T send-keys -t qa-69-$L "$1" Enter; }
tmux -L $SOCK kill-server 2>/dev/null
rm -rf $S/repo-$L $S/sbx-$L $ROOT/.local-dev/config; mkdir -p $S/repo-$L/app; (cd $S/repo-$L/app && git init -q && git commit -q --allow-empty -m init)
$T kill-server 2>/dev/null
$T new-session -d -s qa-69-$L -x 160 -y 40 -e QA_ROOT=$ROOT -e QA_LABEL=$L "bash --rcfile $S/qa-rc.sh -i"
sleep 1
type 'T0=$(date -u +%FT%TZ); hq --version'; sleep 6
type 'hq new a app hi'; sleep 5
type 'hq new b app "a question for input"'; sleep 4
type 'hq new c app "slow work"'; sleep 4
type 'hq new d app "background research"'; sleep 4
type 'hq new e app hello'; sleep 5
pane() { tmux -L $SOCK list-panes -a -F '#{window_name} #{pane_id}' | awk -v n=$1 '$1==n{print $2}'; }
tmux -L $SOCK send-keys -t "$(pane c)" esc Enter
tmux -L $SOCK send-keys -t "$(pane e)" '/effort max' Enter
sleep 3
type clear; type 'hq ls'; sleep 4; shot 1-ls
type clear; type 'hq wait --since $T0 --timeout 5s'; sleep 4; shot 2-wait
{ for n in a b c d e; do echo "== $n"; tmux -L $SOCK show-options -w -t "hq:$n" | grep -E '@hq_(turnend|restseen|dialogseen|endseen|wokeseen)' | sed -E 's/ "?[0-9]{19}/ <nanos>/g; s/[0-9]{19}/<nanos>/g'; done; } >"$OUT/3-records-$L.txt"
type clear; type 'hq'; sleep 5; shot 4-dashboard
$T send-keys -t qa-69-$L a; sleep 2; shot 5-dashboard-all
$T send-keys -t qa-69-$L M-j; sleep 2; shot 6-chord-next
$T send-keys -t qa-69-$L M-j; sleep 2; shot 7-chord-next-again
$T send-keys -t qa-69-$L M-k; sleep 2; shot 8-chord-previous
$T send-keys -t qa-69-$L M-a; sleep 2; shot 9-chord-attention
echo done $L
