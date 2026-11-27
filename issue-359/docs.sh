#!/bin/sh
# QA of #359, check 8: the sentences of the spec, the design and the README that said Claude Code's wait lasts 50 s unless given a timeout, or watches in a loop, on the integration branch (before) and on the issue's branch (after)
for ref in spec-357-wait-task HEAD; do
  echo "== $ref ($(git -C <worktree> rev-parse --short $ref))"
  git -C <worktree> grep -n -o -E ".{0,60}(default stays 50 s|may last longer than 50 s|called in a loop|Claude Code's may wait up to an hour|watch with .wait. in a loop|may last up to an hour, where).{0,40}" $ref -- docs/requirements/spec.md docs/design/hq.md README.md GLOSSARY.md > /tmp/hq-stub-2113981193/out/g.txt; sed "s/^$ref://" /tmp/hq-stub-2113981193/out/g.txt; [ -s /tmp/hq-stub-2113981193/out/g.txt ] || echo "(none)"
done
