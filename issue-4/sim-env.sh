# Simulated sandbox layer: the real hq v0.3.0 binary, real tmux, real wslpath /
# cmd.exe / code / powershell.exe - only sbx.exe is the repository's test stub
# (internal/testutil/sbx-stub) running the repository's fake Claude
# (tools/fakeclaude), because this machine cannot start a real microVM.
. /home/rex/hq-qa/env.sh
export PATH="/home/rex/hq-qa/sim/bin:$PATH"
export SBX_STUB_DIR=/home/rex/hq-qa/sim/sbx
export SBX_STUB_CLAUDE=/home/rex/hq-qa/sim/fakeclaude
export HQ_TMUX_SOCKET=hq-qa-sim
