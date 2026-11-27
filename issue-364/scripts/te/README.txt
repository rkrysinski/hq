Trial T-e (#364), after trial T-c's recipe (#361): a nested Claude Code 2.1.332 (Haiku, hooks off, the configuration
directory cfg/ of its own, its tmux server tmux -L tc364) whose hq server is bin/hq-after (built from
issue-364-send-wait-task at the commit in bin/hq-after.commit) against stub environment t364, every line between them
tapped into runs/RUN/tap.jsonl (tap.py). The supervisor is asked to send a stub agent a message with wait; the agent's
slow turn is let finish (finish.sh) more than 50 s after the send. metrics.py RUN prints the send and wait calls, their
task's polls, and the model requests of the run's transcript around them.
