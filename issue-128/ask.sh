#!/bin/sh
# ask.sh NAME: has agent NAME open a three-question AskUserQuestion dialog and waits until it needs input.
P="Use the AskUserQuestion tool once, with exactly three questions in one call: 1) 'Which colour?' header Colour, options Red, Green, Blue; 2) 'Which size?' header Size, options Small, Large; 3) 'Which shape?' header Shape, options Circle, Square, Triangle. Then reply with the answers you got, nothing else."
hq send "$1" "$P" >/dev/null
for i in $(seq 1 60); do sleep 1; [ "$(hq ls --json | jq -r --arg n "$1" '.agents[]|select(.name==$n)|.state')" = "needs input" ] && break; done
hq read "$1" --json | jq -r .dialog
