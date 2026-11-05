#!/bin/sh
# QA for #154, check 6: macOS / native unchanged (HOME under /tmp/mac).
rm -rf /tmp/mac; mkdir -p "/tmp/mac/Library/Application Support/Claude" /tmp/mac/.config/Claude
export PATH=/tmp/qa154/bin:/usr/bin:/bin HOME=/tmp/mac HQ_PLATFORM=native WSL_DISTRO_NAME= HQ_TMUX_SOCKET= XDG_CONFIG_HOME= TMUX_TMPDIR=
for h in hq-before hq; do echo "\$ $h mcp install"; $h mcp install; echo "(exit $?)"; rm -f /tmp/mac/.config/Claude/* "/tmp/mac/Library/Application Support/Claude/"*; echo; done
