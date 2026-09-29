#!/bin/sh
# Build hq from this checkout and run it, isolated from the user's own hq and
# from other worktrees: the binary goes to .local-dev/hq, and HQ_TMUX_SOCKET
# names a tmux server private to this checkout (tmux -L), so agents started
# here never appear in the user's hq session. XDG_CONFIG_HOME gives the
# checkout its own preferences file, so QA never changes the user's sort,
# view or update check; gh keeps the user's login through GH_CONFIG_DIR.
# The build is a dev build, which never looks for updates; to check update
# behaviour, LOCAL_DEV_VERSION=vX.Y.Z bakes that version in, and
# HQ_RELEASES_URL points hq at a stub release server (design §3.9).
#
# Usage: scripts/local-dev.sh [hq arguments...]     build, then run hq with them
#        scripts/local-dev.sh --kill-server         end this checkout's tmux server
set -eu

ROOT=$(cd "$(dirname "$0")/.." && pwd)
BIN="$ROOT/.local-dev/hq"
HQ_TMUX_SOCKET="hq-dev-$(printf %s "$ROOT" | cksum | cut -d' ' -f1)"
export HQ_TMUX_SOCKET
GH_CONFIG_DIR=${GH_CONFIG_DIR:-${XDG_CONFIG_HOME:-$HOME/.config}/gh}
XDG_CONFIG_HOME="$ROOT/.local-dev/config"
export GH_CONFIG_DIR XDG_CONFIG_HOME

if [ "${1:-}" = --kill-server ]; then
    tmux -L "$HQ_TMUX_SOCKET" kill-server 2>/dev/null || true
    echo "tmux server $HQ_TMUX_SOCKET ended"
    exit 0
fi

mkdir -p "$ROOT/.local-dev"
(cd "$ROOT" && go build ${LOCAL_DEV_VERSION:+-ldflags "-X github.com/rkrysinski/hq/internal/version.Version=$LOCAL_DEV_VERSION"} -o "$BIN" ./cmd/hq)
exec "$BIN" "$@"
