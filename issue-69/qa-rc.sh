# usage: QA_ROOT=... QA_LABEL=... bash --rcfile qa-rc.sh
S=/tmp/claude-1000/-Users-romankrysinski-work-repositories-hq/a41f9452-666c-4138-8314-b0e3a20745cb/scratchpad
export PATH=$S/bin:$PATH HQ_PLATFORM=native SBX_STUB_DIR=$S/sbx-$QA_LABEL SBX_STUB_CLAUDE=$S/fc/claude FAKE_CLAUDE_DELAY=300ms
unset TMUX TMUX_PANE
hq() { "$QA_ROOT/scripts/local-dev.sh" "$@"; }
PS1='\[\e[32m\]$QA_LABEL\[\e[0m\] $ '
cd $S/repo-$QA_LABEL
