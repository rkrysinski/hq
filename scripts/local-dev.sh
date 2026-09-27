#!/bin/sh
# Build hq from this checkout and run it, isolated from the user's own hq and
# from other worktrees: the binary goes to .local-dev/hq, and HQ_TMUX_SOCKET
# names a tmux server private to this checkout (tmux -L), so agents started
# here never appear in the user's hq session.
#
# Usage: scripts/local-dev.sh [hq arguments...]     build, then run hq with them
#        scripts/local-dev.sh --kill-server         end this checkout's tmux server
set -eu

ROOT=$(cd "$(dirname "$0")/.." && pwd)
BIN="$ROOT/.local-dev/hq"
HQ_TMUX_SOCKET="hq-dev-$(printf %s "$ROOT" | cksum | cut -d' ' -f1)"
export HQ_TMUX_SOCKET

if [ "${1:-}" = --kill-server ]; then
    tmux -L "$HQ_TMUX_SOCKET" kill-server 2>/dev/null || true
    echo "tmux server $HQ_TMUX_SOCKET ended"
    exit 0
fi

mkdir -p "$ROOT/.local-dev"
(cd "$ROOT" && go build -o "$BIN" ./cmd/hq)
exec "$BIN" "$@"
