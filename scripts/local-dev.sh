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
# --stub MODE runs hq against the stubs of internal/testutil instead of the
# real sbx, to QA a change for Windows/WSL on any machine (#159):
#   native     the stub sbx as sbx, no Windows side
#   wsl-linux  WSL (WSL_DISTRO_NAME=Stub), the stub sbx as the Linux sbx, and
#              the stubs of the Windows side listed in internal/testutil/wsl-stubs
#              (wslpath, cmd.exe, powershell.exe, as testutil.WSLStubs)
#   wsl-exe    as wsl-linux, with the stub sbx as sbx.exe
#   none       as wsl-linux, with no sbx at all
# A real sbx or sbx.exe on PATH is hidden in every mode, and PATH
# directories under the user's HOME are reached through links. The stub
# environment lives in /tmp/hq-stub-ID (ID as the tmux socket's): the binary
# at bin/hq, a neutral HOME at home (with the preferences), the stub sbx's
# state at sbx, C:\ at win/mnt/c, the fake claude (tools/fakeclaude) the stub
# sbx runs, and a git repository at repo to start agents in, so screenshots
# show no user's path. The tmux server stays this checkout's; it remembers
# the mode it was started in, and a run in another mode is refused until
# --kill-server ends it.
#
# --before builds the binary from LOCAL_DEV_BEFORE (origin/dev by default;
# fetch it first) instead of this checkout, at the same path, so a "before"
# and an "after" binary are the same program to what registered it (hq mcp
# install). Each build is renamed into place, so a binary still running is
# replaced without "Text file busy".
#
# Usage: scripts/local-dev.sh [--stub MODE] [--before] [hq arguments...]
#        scripts/local-dev.sh --kill-server   end this checkout's tmux server
#                                             and remove its stub environment
set -eu

ROOT=$(cd "$(dirname "$0")/.." && pwd)
# LOCAL_DEV_ID names the tmux server and the stub environment; tests set it
# to stay apart from the checkout's own.
ID=${LOCAL_DEV_ID:-$(printf %s "$ROOT" | cksum | cut -d' ' -f1)}
case $ID in '' | *[!A-Za-z0-9-]*)
    echo "local-dev: LOCAL_DEV_ID may hold only letters, digits and -" >&2
    exit 1
    ;;
esac
HQ_TMUX_SOCKET="hq-dev-$ID"
export HQ_TMUX_SOCKET
STUB_ENV=/tmp/hq-stub-$ID
TESTUTIL=$ROOT/internal/testutil
GH_CONFIG_DIR=${GH_CONFIG_DIR:-${XDG_CONFIG_HOME:-$HOME/.config}/gh}
XDG_CONFIG_HOME="$ROOT/.local-dev/config"
export GH_CONFIG_DIR XDG_CONFIG_HOME

die() {
    echo "local-dev: $*" >&2
    exit 1
}

kill_server() {
    path=$(tmux -L "$HQ_TMUX_SOCKET" display-message -p '#{socket_path}' 2>/dev/null || true)
    pids=$(tmux -L "$HQ_TMUX_SOCKET" list-panes -a -F '#{pane_pid}' 2>/dev/null || true)
    tmux -L "$HQ_TMUX_SOCKET" kill-server 2>/dev/null || true
    # The panes' programs (the stub sbx among them) exit after tmux returns;
    # let them before their directory goes.
    for pid in $pids; do
        i=0
        while [ $i -lt 50 ] && kill -0 "$pid" 2>/dev/null; do
            sleep 0.1
            i=$((i + 1))
        done
    done
    # tmux 3.6 leaves the socket file behind (#74).
    [ -z "$path" ] || rm -f "$path"
    echo "tmux server $HQ_TMUX_SOCKET ended"
    if [ -d "$STUB_ENV" ]; then
        rm -rf "$STUB_ENV"
        echo "stub environment $STUB_ENV removed"
    fi
}

mode= before=
while [ $# -gt 0 ]; do
    case $1 in
    --kill-server)
        kill_server
        exit 0
        ;;
    --stub)
        [ $# -ge 2 ] || die "--stub needs a mode: native, wsl-linux, wsl-exe or none"
        mode=$2
        shift 2
        ;;
    --stub=*)
        mode=${1#--stub=}
        shift
        ;;
    --before)
        before=1
        shift
        ;;
    *) break ;;
    esac
done
case $mode in
'' | native | wsl-linux | wsl-exe | none) ;;
*) die "unknown --stub mode $mode: native, wsl-linux, wsl-exe or none" ;;
esac

# A tmux server keeps the environment of the hq that started it, which its
# panes (the stub sbx) run with: refuse to mix modes on one server.
HQ_LOCAL_DEV_MODE=${mode:-real}
export HQ_LOCAL_DEV_MODE
if tmux -L "$HQ_TMUX_SOCKET" has-session 2>/dev/null; then
    was=$(tmux -L "$HQ_TMUX_SOCKET" show-environment -g HQ_LOCAL_DEV_MODE 2>/dev/null || true)
    was=${was#HQ_LOCAL_DEV_MODE=}
    case $was in '' | -*) was=real ;; esac
    if [ "$was" != "$HQ_LOCAL_DEV_MODE" ]; then
        name() { if [ "$1" = real ]; then echo "the real sbx"; else echo "--stub $1"; fi; }
        die "this checkout's tmux server runs with $(name "$was"), not $(name "$HQ_LOCAL_DEV_MODE"); end it first with scripts/local-dev.sh --kill-server"
    fi
fi

if [ -n "$mode" ]; then
    BIN=$STUB_ENV/bin/hq
else
    BIN=$ROOT/.local-dev/hq
fi
mkdir -p "$(dirname "$BIN")"
src=$ROOT
if [ -n "$before" ]; then
    ref=${LOCAL_DEV_BEFORE:-origin/dev}
    git -C "$ROOT" rev-parse -q --verify "$ref^{commit}" >/dev/null || die "--before: no commit $ref (fetch it first)"
    src=$(mktemp -d)
    trap 'rm -rf "$src"' EXIT
    git -C "$ROOT" archive "$ref" | tar -x -C "$src"
fi
(cd "$src" && go build ${LOCAL_DEV_VERSION:+-ldflags "-X github.com/rkrysinski/hq/internal/version.Version=$LOCAL_DEV_VERSION"} -o "$BIN.new" ./cmd/hq)
if [ "$src" != "$ROOT" ]; then
    rm -rf "$src"
    trap - EXIT
fi
mv -f "$BIN.new" "$BIN"

if [ -n "$mode" ]; then
    # Built with the user's HOME, for Go's caches.
    (cd "$ROOT" && go build -o "$STUB_ENV/claude.new" ./tools/fakeclaude)
    mv -f "$STUB_ENV/claude.new" "$STUB_ENV/claude"

    rm -rf "$STUB_ENV/stubs" "$STUB_ENV/path"
    mkdir -p "$STUB_ENV/stubs" "$STUB_ENV/path" "$STUB_ENV/home" "$STUB_ENV/sbx" "$STUB_ENV/win/mnt/c"
    case $mode in
    native | wsl-linux) ln -s "$TESTUTIL/sbx-stub" "$STUB_ENV/stubs/sbx" ;;
    wsl-exe) ln -s "$TESTUTIL/sbx-stub" "$STUB_ENV/stubs/sbx.exe" ;;
    esac
    if [ "$mode" != native ]; then
        # The list testutil.WSLStubs reads; a stub not there yet is skipped.
        while read -r cmd stub || [ -n "$cmd" ]; do
            case $cmd in '' | '#'*) continue ;; esac
            if [ -e "$TESTUTIL/$stub" ]; then
                ln -s "$TESTUTIL/$stub" "$STUB_ENV/stubs/$cmd"
            fi
        done <"$TESTUTIL/wsl-stubs"
    fi

    # Hide a real sbx, and the user's paths from what hq shows or registers
    # (hq mcp install sbx keeps PATH): a PATH directory holding sbx or
    # sbx.exe, or under the user's HOME, is replaced by one in the stub
    # environment with links to everything else in it.
    newpath= n=0
    set -f
    IFS=:
    for d in $PATH; do
        case $d in "$HOME" | "$HOME"/*) mine=1 ;; *) mine= ;; esac
        if [ -n "$d" ] && { [ -n "$mine" ] || [ -e "$d/sbx" ] || [ -e "$d/sbx.exe" ]; }; then
            n=$((n + 1))
            mirror=$STUB_ENV/path/$n
            mkdir "$mirror"
            set +f
            for f in "$d"/* "$d"/.[!.]*; do
                [ -e "$f" ] || continue
                case ${f##*/} in sbx | sbx.exe) ;; *) ln -s "$f" "$mirror/" ;; esac
            done
            set -f
            d=$mirror
        fi
        newpath=$newpath${newpath:+:}$d
    done
    unset IFS
    set +f
    PATH=$STUB_ENV/bin:$STUB_ENV/stubs:$newpath

    if [ ! -d "$STUB_ENV/repo/.git" ]; then
        git init -q -b main "$STUB_ENV/repo"
        git -C "$STUB_ENV/repo" -c user.email=qa@example.com -c user.name=qa commit -q --allow-empty -m init
    fi

    HOME=$STUB_ENV/home
    XDG_CONFIG_HOME=$HOME/.config
    SBX_STUB_DIR=$STUB_ENV/sbx
    SBX_STUB_CLAUDE=$STUB_ENV/claude
    WSL_STUB_ROOT=$STUB_ENV/win
    export PATH HOME XDG_CONFIG_HOME SBX_STUB_DIR SBX_STUB_CLAUDE WSL_STUB_ROOT
    if [ "$mode" = native ]; then
        HQ_PLATFORM=native
        export HQ_PLATFORM
        unset WSL_DISTRO_NAME
    else
        # Detected as WSL, as on a real one; wslpath-stub's distribution.
        WSL_DISTRO_NAME=Stub
        export WSL_DISTRO_NAME
        unset HQ_PLATFORM
    fi
fi
exec "$BIN" "$@"
