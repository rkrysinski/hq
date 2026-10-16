# usage: QA_ROOT=... QA_LABEL=... bash --rcfile rc.sh
Q=/tmp/claude-1000/-Users-romankrysinski-work-repositories-hq/a41f9452-666c-4138-8314-b0e3a20745cb/scratchpad/qa70
export PATH=$Q/bin:$PATH HQ_PLATFORM=native SBX_STUB_DIR=$Q/sbx-$QA_LABEL SBX_STUB_CLAUDE=$Q/claude
unset TMUX TMUX_PANE
hq() { "$QA_ROOT/scripts/local-dev.sh" "$@"; }
PS1='\[\e[32m\]$QA_LABEL\[\e[0m\] $ '
cd $Q/repo-$QA_LABEL/app
