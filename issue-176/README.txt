QA of #176, run on a fresh clone of issue-176-check-script at 7564fb8 in /tmp/qa176/hq.
1-clean      cd / && /tmp/qa176/hq/scripts/check.sh            (clean tree, run from another directory)
2-untracked  touch hq/untracked.txt; cd / && /tmp/qa176/hq/scripts/check.sh
3-format     rm hq/untracked.txt; printf '\nvar  unformatted = 1\n' >> hq/cmd/hq/main.go; cd /tmp/qa176 && hq/scripts/check.sh
Each .txt is the script's output followed by "exit <status>".
