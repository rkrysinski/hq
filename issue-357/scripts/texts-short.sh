#!/bin/sh
# For the screenshot of checks 3, 7 and 8: the lines of texts.py's output that the checks read, after and before
cd "$(dirname "$0")"
echo "Checks 3, 7 and 8 after (6bda954), from texts.py:"
grep -E '^== |^watching line|^past the|^phrase|^own tasks' c3-texts.txt | cut -c1-330
echo
echo "Before (integration branch fe29725), Claude Code:"
grep -E '^== |^watching line|^phrase|^own tasks' c3-texts-before.txt | cut -c1-330
