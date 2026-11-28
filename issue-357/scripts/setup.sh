#!/bin/sh
# usage: [QA_ENV=ID] [QA_BEFORE=1] setup.sh NAME... - starts each agent in stub environment ID (default t357r): one whose
# name starts with p at its prompt after a short first turn, any other working on a slow job until a line typed into
# its pane lets it finish, or esc interrupts it with no hook
cd "$(dirname "$0")"
env=${QA_ENV:-t357r}
for n in "$@"; do
  case $n in
  p*) ./hq.sh new "$n" "/tmp/hq-stub-$env/repo" "hello from $n" ;;
  *) ./hq.sh new "$n" "/tmp/hq-stub-$env/repo" "slow job of $n" ;;
  esac
done
sleep 4; ./hq.sh ls
