#!/bin/sh
# usage: screen.sh RUN [LINES] - the run's screen without blank lines
TC=$(cd "$(dirname "$0")" && pwd)
"$TC/tm.sh" capture-pane -p -t "=$1:" | grep -v '^\s*$' | tail -"${2:-14}" | cut -c1-170
