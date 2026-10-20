# usage: QA_ROOT=... QA_LABEL=... bash --rcfile qa-rc.sh
S=/tmp/claude-1000/-home-dev-work-repositories-hq/00000031-0000-4000-8000-000000000031/scratchpad
export PATH=$S/bin:$PATH HQ_PLATFORM=native SBX_STUB_DIR=$S/sbx-$QA_LABEL SBX_STUB_CLAUDE=$S/fc/claude FAKE_CLAUDE_DELAY=300ms
unset TMUX TMUX_PANE
hq() { "$QA_ROOT/scripts/local-dev.sh" "$@"; }
PS1='\[\e[32m\]$QA_LABEL\[\e[0m\] $ '
cd $S/repo-$QA_LABEL
