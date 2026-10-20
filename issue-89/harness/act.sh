#!/bin/sh
# act.sh EVENT: holds up to ctl/hold-EVENT seconds for ctl/ans-EVENT.jq,
# then prints that jq program's output over the payload.
S=/tmp/claude-1000/-home-dev-work-repositories-hq/0000001d-0000-4000-8000-00000000001d/scratchpad/spike
ev=$1
now() { date +%s.%3N; }
p=$(cat)
echo "$(now) act-start $ev pid=$$" >> $S/events.log
trap 'echo "$(now) act-TERM $ev pid=$$" >> $S/events.log; exit 143' TERM
trap 'echo "$(now) act-INT $ev pid=$$" >> $S/events.log; exit 130' INT
trap 'echo "$(now) act-HUP $ev pid=$$" >> $S/events.log; exit 129' HUP
hold=0; [ -f $S/ctl/hold-$ev ] && hold=$(cat $S/ctl/hold-$ev)
end=$(( $(date +%s) + hold ))
while :; do
	if mv $S/ctl/ans-$ev.jq $S/ctl/taken-$ev-$$.jq 2>/dev/null; then
		out=$(printf '%s' "$p" | jq -c -f $S/ctl/taken-$ev-$$.jq)
		echo "$(now) act-answer $ev pid=$$ $out" >> $S/events.log
		printf '%s\n' "$out"
		exit 0
	fi
	[ $(date +%s) -ge $end ] && break
	sleep 0.1
done
echo "$(now) act-none $ev pid=$$" >> $S/events.log
