#!/bin/sh
# QA for #116: two agents whose turns end 150 ms apart while hq wait runs.
# Usage: two-finish.sh HQ_BIN LABEL   (runs on its own tmux server and sbx stub)
set -eu
HQ=$1 LABEL=$2
QA=$(cd "$(dirname "$0")" && pwd)
W="$QA/run-$LABEL"
rm -rf "$W" && mkdir -p "$W/bin" "$W/sbx"
ln -s "$REPO/internal/testutil/sbx-stub" "$W/bin/sbx"
export PATH="$W/bin:$PATH" SBX_STUB_DIR="$W/sbx" SBX_STUB_CLAUDE="$QA/claude" HQ_PLATFORM=native
export HQ_TMUX_SOCKET="qa116-$LABEL" XDG_CONFIG_HOME="$W/config" TMUX=
tmux -L "$HQ_TMUX_SOCKET" kill-server 2>/dev/null || true
for r in app lib; do
    git init -q "$W/$r" && git -C "$W/$r" -c user.name=qa -c user.email=qa@x commit -q --allow-empty -m init
done
hq() { "$HQ" "$@"; }
type_in() { # NAME LINE
    id=$(tmux -L "$HQ_TMUX_SOCKET" list-windows -a -F '#{window_id} #{@hq_name}' | awk -v n="$1" '$2==n {print $1}')
    tmux -L "$HQ_TMUX_SOCKET" send-keys -t "$id" "$2" Enter
}
hq new a "$W/app" "slow job" >/dev/null && hq new b "$W/lib" "slow job" >/dev/null
until [ "$(hq ls --json | jq -r '[.agents[] | select(.state=="working")] | length')" = 2 ]; do sleep 0.2; done
echo "\$ hq ls"; hq ls
since=$(hq ls --json | jq -r .next_since)
echo
echo "# a's and b's turns end 150 ms apart while hq wait runs"
echo "\$ hq wait a b --since $since"
(sleep 1; type_in a "go on"; sleep 0.15; type_in b "go on") &
out=$(hq wait a b --since "$since")
echo "$out"
next=$(echo "$out" | sed -n 's/^next: hq wait a b --since //p')
echo
echo "# the next call, from its moment: nothing more"
echo "\$ hq wait a b --since $next --timeout 2s"
hq wait a b --since "$next" --timeout 2s
tmux -L "$HQ_TMUX_SOCKET" kill-server 2>/dev/null || true
