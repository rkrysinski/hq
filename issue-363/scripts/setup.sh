#!/bin/sh
# Starts the agents h1-h9 of the checks in stub environment t363, each working on a slow job until a line typed into its pane lets it finish
cd "$(dirname "$0")"
for n in h1 h2 h3 h4 h5 h6 h7 h8 h9; do ./hq.sh new $n /tmp/hq-stub-t363/repo "slow job of $n"; done
sleep 3; ./hq.sh ls
