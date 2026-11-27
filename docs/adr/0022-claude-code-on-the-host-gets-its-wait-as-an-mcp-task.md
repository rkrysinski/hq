# 0022 - Claude Code on the host gets its wait as an MCP task

A supervisor in Claude Code on the host followed its agents by calling `wait` in a loop (spec, Phase 2; design §3.12): a call that named no timeout returned after 50 s, and every empty return is a model request that reads the supervisor's whole context again. In four supervising sessions on the host the empty returns were 24% to 42% of the session's cost (#342). The one way to wait without them, `hq wait --timeout 0` run as a background command, has to be taught from outside hq. The rule the operator set: hq needs no script, tool or instruction from outside itself to keep token use low, and has no method of its own that raises it needlessly.

What Claude Code does was tried, not read (2026-11-23 to 2026-11-27, Claude Code 2.1.330 to 2.1.332, most of it in a sandbox against a probe server). It declares the MCP Tasks extension on each request of the 2026-07-28 protocol. Given a task handle it ends the tool call, asks the server for the task at the interval the server names with no model request (an hour, 721 times), and starts a turn when the task completes, also after `/clear` and after compaction; a resumed session asks a new server process for its old task. A call that does not become a task is moved to the background after 120 s, and its result starts a turn.

So, for Claude Code on the host:

- **`wait` holds, then goes on as a wait task.** It holds for 50 s as before, so a change within them comes back in the same call and the same turn. After that, when the request declares the Tasks extension, it answers with a task handle: Claude Code asks hq for the task without the model and starts a turn when it completes, with what `wait` would have returned, its cursor included. `send` with `wait` does the same for the answer to its one message.
- **The task's id carries the wait.** Whom it waits for, from which cursor, until when, the agents it covered when the call ended, and for `send` with `wait` the message: any `hq mcp` answers for it by taking a look, and none holds the wait in memory. The id is signed with a key in hq's state directory, so that it cannot be built from the terms alone, as the extension requires of an id.
- **No time limit unless the caller gives one.** A wait task stands until the first change. With a timeout it completes at that time with no agents and the cursor.
- **One task for each wait.** Each `wait` that goes on is a task of its own and completes once. A new `wait` replaces none: hq keeps no list of a conversation's tasks, and a server cannot end a client's task. The instructions say not to call `wait` again while one is pending.
- **Without tasks, one long call.** A request that does not declare the extension gets the call as built, and with no timeout named it lasts as long as that client allows, an hour, not 50 s. Claude Code moves it to the background after 120 s and its result starts a turn: two model requests for a call that lasts longer than 120 s, none in between.
- **The instructions say so.** Each client gets instructions of its own, and Claude Code's speak of one call, not of a loop.
- **hq writes the Tasks contract by hand.** The Go MCP SDK in use (v1.8.0) has no tasks. Its public API lets a server answer `tools/call` with a result of its own and add the task methods, so hq writes the contract itself, from the extension's specification, until the SDK has it.

## Considered Options

- **`hq wait --timeout 0` as a background command, by instruction**: no model request while it waits, but only for a supervisor that an instruction from outside hq has taught it.
- **Claude Code's messaging socket between sessions, a hook written into the user's settings, typing into the supervisor's terminal**: an interface that is internal and that a setting of the user's holds or refuses; a change to the user's own settings; another mechanism on each platform.
- **The wait in the memory of the `hq mcp` that took the call**: lost with the process. A resumed Claude Code session would ask a new process for a task none knows, for five minutes, and then end it as outcome unknown.
- **A random id and a record on disk**: hq would know a conversation's tasks, for a state of its own to keep and to clean up.
- **An id made of the terms alone**: against the extension's rule that a third party cannot guess an id, though over standard input and output the only client is the user's own.
- **Ending the older task when a new `wait` comes**: needs that record, and each replacement would start a turn.
- **Only a longer call**: two model requests an hour instead of none, and an entry written by an earlier install gives a call 61 minutes.

## Consequences

- The spec's "Watching is waiting" (Phase 2) and design §3.12 change with the spec that builds this.
- Each time Claude Code asks for a task, hq takes a look: an idle supervisor costs hq a look at every interval, tmux and sbx asked, for as long as it waits. No model request.
- An id hq cannot verify, because the key went with the state directory, is an unknown task: Claude Code asks for it for about five more minutes, then stops and starts a turn that says its outcome is unknown.
- Leaving a Claude Code session with a task pending opens a dialog of Claude Code's own ("Exit and stop tasks", "Move to background and exit", "Stay"). No choice cancels the task. After "Exit and stop tasks", the resumed session asks a new `hq mcp` for the same task. "Move to background and exit" stops the task, and the session in the background never asks for it, so a supervision moved to the background ends silently.
- An agent in a sandbox and a supervisor in Claude Desktop get no task: the sandbox's gateway passes none, and Claude Desktop declares none. They keep the loop until their own decision (#356).
- Not tried: a task across leaving and resuming and for hours on macOS; a silent call of 30 to 60 minutes over standard input and output on the host; Claude Code on Windows and WSL.
