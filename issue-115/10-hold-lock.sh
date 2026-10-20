#!/bin/sh
# hold-lock FILE SECONDS: hold FILE.lock as a live proper-lockfile holder does (refreshed), then give it up.
mkdir "$1.lock" || exit 1
n=$(( $2 * 2 )); while [ $n -gt 0 ]; do touch "$1.lock"; sleep 0.5; n=$((n-1)); done
rmdir "$1.lock"; echo "lock given up at $(date +%T.%N | cut -c1-12)"
