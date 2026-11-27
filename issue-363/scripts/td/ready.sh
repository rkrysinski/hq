#!/bin/sh
# usage: ready.sh RUN - answers the trust dialog if shown and waits for the prompt
TC=$(cd "$(dirname "$0")" && pwd); i=0
while [ $i -lt 60 ]; do
  s=$("$TC/tm.sh" capture-pane -p -t "=$1:" 2>/dev/null)
  if echo "$s" | grep -q "Yes, I trust this folder"; then "$TC/tm.sh" send-keys -t "=$1:" Enter; sleep 2
  elif echo "$s" | grep -q -E "bypass permissions|\? for shortcuts|shift\+tab"; then echo "$1 ready after $i s"; exit 0; fi
  sleep 1; i=$((i + 1))
done
echo "$1 not ready after 60 s"; "$TC/screen.sh" "$1" 12; exit 1
