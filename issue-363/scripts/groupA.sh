#!/bin/sh
# Checks 1, 1b, 2, 3, 4 and 7 side by side, each started 5 s after the one before (#368), each into its own file
cd "$(dirname "$0")"
for s in s1-limit s1b-before-deadline s2-together s3-killed s4-error s7-nearby; do
  ./$s.sh > $s.txt 2>&1 &
  sleep 5
done
wait
