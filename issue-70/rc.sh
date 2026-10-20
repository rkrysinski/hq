# usage: QA_ROOT=... QA_LABEL=... bash --rcfile rc.sh
Q=/tmp/claude-1000/-home-dev-work-repositories-hq/00000031-0000-4000-8000-000000000031/scratchpad/qa70
export PATH=$Q/bin:$PATH HQ_PLATFORM=native SBX_STUB_DIR=$Q/sbx-$QA_LABEL SBX_STUB_CLAUDE=$Q/claude
unset TMUX TMUX_PANE
hq() { "$QA_ROOT/scripts/local-dev.sh" "$@"; }
PS1='\[\e[32m\]$QA_LABEL\[\e[0m\] $ '
cd $Q/repo-$QA_LABEL/app
