package state

import (
	"bytes"
	"encoding/json"
	"strings"
)

// hookSource runs inside the sandbox on each lifecycle event, in Claude's
// current directory, with the event payload on stdin, the event's kind in $1
// and, on stop, dialog and input, the notification sequence in $2. It writes
// the branch checked out there as a header line, then the payload, to the
// main repository's .git/hq/agents/$HQ_ID, replacing it in one step, and
// keeps the latest Stop event beside it (.stop) for the last message. An
// open dialog (the latest event is a PermissionRequest, or an Elicitation:
// an MCP server's form, which dialog reports too and decides nothing, so
// Claude draws it) decides three kinds: answer (a tool ran) is written only
// when it closes that dialog, the same tool having run (for a form, a tool
// of its server: some other Elicitation hook answered it), input (Claude's
// Notification) only when no dialog is open, so a dialog is reported once,
// when it opens, and result (the form answered at the agent,
// ElicitationResult) only when it closes a form (design §3.4, #100). A resumed
// session (resume) first keeps the event before it (.prev) for the branch,
// worktree and last message, unless that event is itself a resumed start,
// which keeps the one it kept. On stop,
// dialog and input it then prints the desktop notification for Claude to
// write to its terminal (design §3.5).
//
// It also delivers the messages hq send left in the agent's inbox (ADR
// 0012): it takes them out of the inbox by renaming them, so each is
// delivered once, and prints them, oldest first, as Claude's hook output.
// On stop it blocks the stop with them as the reason, so Claude goes on
// and stops again; the blocked stop is not reported and notifies nobody,
// and the agent stays working. On prompt they ride along with the prompt;
// on the end of a tool (PostToolUse only, and not a subagent's) they come
// with its result, but only once one of them asks for that (--now). With
// no message waiting it prints what it printed before.
//
// A turn that ends while background work still runs (hookAwk) is not the
// agent's end: Claude goes on by itself when that work has finished, with
// a prompt of its own (wakeEvent). Such a stop, and such a prompt while
// the agent works, are kept beside the state file (.on, Report.Kept) and
// leave the file alone, so the agent stays working since the user's
// prompt; the stop still gives the last message (.stop) and notifies
// nobody. When the file does not say working, the stop is written there
// too, and Kept reads it as working; but not over a subagent's dialog
// (its PermissionRequest carries agent_id), which is still open: the
// agent needs input until it is answered. Any other prompt, stop or
// session event drops what was kept. An id owed a turn (.owe) lasts
// until Claude wakes for it; one that has held back a turn end (!) goes
// with the user's next prompt, or at the next Stop when no wake-up for a
// task came in between, so a turn that never comes holds back one turn
// end, which the host sees by the agent at rest (design §3.4); a wake-up
// that names no task, or a session's start or end, forgets them all. On
// answer it first marks that Claude has acted on the turn when the tool is
// the agent's own (.acted, Report.Acted, holding the turn's prompt_id), and
// adds to .owe the background work the tool started, if any (hookAwk); the
// mark lasts until the next prompt, stop (a kept one too) or session event,
// but a wake-up kept while the agent works goes on with the same turn and
// leaves it, as do a stop blocked to deliver messages and a prompt Claude
// queues.
//
// Claude runs the prompt hook when it queues a prompt typed while the
// agent works, with the prompt_id of the turn at work, and no hook when it
// takes it (design §3.4, #82). Such a prompt (the state file says working,
// and its prompt_id is the state file's or .acted's) is written as any
// other and kept beside the state file (.queued). Claude takes it into the
// turn at the next end of a tool of the agent's own, which drops it, or
// right after the turn's Stop, when no tool of its own has ended since: a
// Stop with the prompt_id of .queued hands over (a stop blocked to deliver
// messages goes on with the same turn, and Claude takes nothing into it).
// It gives .stop and makes the state file the queued prompt anew, so the
// agent works on it from now, and notifies nobody. .queued stays, its
// prompt_id now .stop's, for a /clear queued before the prompt, which
// Claude runs in between: its SessionEnd leaves every file alone, and its
// SessionStart writes the queued prompt again under the new session's id,
// then forgets what a session's start forgets. Or the user interrupts the
// turn with Esc, which fires no Stop, and Claude runs the queued prompt at
// once as a turn of its own, under a prompt_id of its own, with no hook
// until its first tool ends (#88): the end of a tool of the agent's own
// whose prompt_id is not .queued's (the turn at work's, which the state
// file holds too), .queued still pending (not taken at a Stop) and no
// wake-up kept (.on: Claude went on with the turn at work), hands over the
// same way, less .stop, and the mark that Claude has acted is that turn's.
// hq hands over before that when the agent's screen shows it (TakeQueued).
// Any other prompt, stop or session event drops .queued.
//
// It also knows whether the turn is a supervised turn (spec §5, design
// §3.5): one whose every prompt came from the supervisor, whose end
// notifies nobody. The mark is a file beside the state file (.sup), there
// while everything the agent works on or owes a turn for is the
// supervisor's. A prompt that is not Claude's own takes hq's word that it
// is the supervisor's (.next, Announce) and sets the mark, unless something
// of the user's may be under way: a message of theirs rides along,
// background work is owed a turn (held back or not; a line of .owe but the
// mark of the agent's own watch, =ID) or the state file says working, with
// no mark there already. A prompt without that word, and any
// message of the user's the hook delivers (one whose name lacks
// supervisorSuffix), removes the mark. On stop it prints no notification
// while the mark is there, and removes it when no turn is owed any more;
// dialog and input notify regardless, and Claude's wake-ups leave the mark
// alone, so closing turns follow the turn that started the work. A
// session's start or end removes it.
//
// On hold, the kind of a second hook on a question dialog's PermissionRequest
// (AskUserQuestion), run beside the dialog's own, it writes nothing and
// holds for hq's answer (hq answer, ADR 0013): once the state file is the
// dialog's report, the same bytes as its own copy of the payload (the
// dialog's hook may report it a moment later), it looks every 0.2 s for the
// answer hq leaves in the inbox, named for that report by its cksum, takes
// it by renaming it and prints it, a decision hq wrote whole, as its
// output. It ends with no output once the state file is no longer the
// dialog's (answered at the terminal, the session ended, a dialog's report
// never coming), never taking an answer after that, at Claude's SIGTERM
// (its timeout, the session's end), and at once for a subagent's dialog
// (agent_id), which hq does not answer.
//
// It needs only sh, git, cat, mv, cp, mkdir, rm, grep, awk, sleep and
// cksum, sends its own errors nowhere and always exits 0; it holds Claude
// back only while a message is unread (design §3.4, §7.1): the hook of
// kind hold waits while its dialog is open, which Claude still draws and
// takes an answer at.
const hookSource = `case $HQ_ID in '' | *[!0123456789abcdef]*) exit 0 ;; esac
exec 2>/dev/null
q=
b=$(git branch --show-current)
l=${b:-${PWD##*/}}
cd "${CLAUDE_PROJECT_DIR:-.}"
g=$(git rev-parse --git-common-dir) || exit 0
d=$g/hq/agents
i=$g/hq/inbox/$HQ_ID
mkdir -p "$d" || exit 0
f=$d/$HQ_ID
t=$f.$$
{ printf '%s %s\n' ` + branchHeader + ` "$b" && cat; } >"$t" || { rm -f "$t"; exit 0; }
A='` + hookAwk + `'
works() { grep -Eq '` + workingEvent + `' "$f"; }
ask() { grep -Eq '` + dialogEvent + `' "$f"; }
form() { grep -q '` + elicitEvent + `' "$f"; }
theirs() { ask && grep -q '"agent_id"' "$f"; }
owes() { grep -q '^[^=]' "$f` + owedSuffix + `"; }
owe() { [ -s "$t.o" ] && mv -f "$t.o" "$f` + owedSuffix + `" || rm -f "$t.o" "$f` + owedSuffix + `"; }
put() { cp -f "${2:-$t}" "$t.c" && mv -f "$t.c" "$1"; rm -f "$t.c"; }
get() { K=get N=$1 awk "$A" "$2"; }
turn() { get prompt_id "$1"; }
queued() { n=$(turn "$f` + queuedSuffix + `") && [ -n "$n" ]; }
taken() { queued && [ "$n" = "$(turn "$f.stop")" ]; }
tool() { awk 'match($0, /"tool_name"[[:blank:]]*:[[:blank:]]*"[^"]*"/) { print substr($0, RSTART, RLENGTH); exit }' "$1"; }
mcp() { form && v=$(get mcp_server_name "$f") && case $(tool "$t") in *\"mcp__"$v"__*) ;; *) false ;; esac; }
soon() { for m in "$i"/[0-9]*` + nowSuffix + `; do [ -f "$m" ] && return 0; done; return 1; }
take() {
    u=; c=$i/.taken.$$
    [ -d "$i" ] && rm -rf "$c" && mkdir "$c" || return 1
    for m in "$i"/[0-9]*; do [ -f "$m" ] && mv "$m" "$c/"; done
    r=1
    for m in "$c"/*; do [ -f "$m" ] && r=0 && case $m in *` + supervisorSuffix + ` | *` + supervisorSuffix + nowSuffix + `) ;; *) u=1 ;; esac; done
    [ $r = 0 ] || rm -rf "$c"; return $r
}
say() {
    [ "$u" ] && rm -f "$f` + supervisedSuffix + `"
    printf '%s' "$1"; s=
    for m in "$c"/*; do [ -f "$m" ] && printf '%s%s' "$s" '` + MessageLabel + `' && cat "$m" && s='\n\n'; done
    rm -rf "$c"; printf '%s\n' "$2"
}
nap() { sleep .2 || sleep 1; }
mine() { awk -v RS='\001' 'NR==FNR{p=$0;next}{s=$0==p}END{exit !s}' "$t" "$f"; }
case $1 in
hold) grep -q '"agent_id"' "$t" || { k=$(cksum <"$t") && a=$i/` + answerPrefix + `${k% *}.${k#* } && trap 'rm -f "$t"; exit 0' HUP TERM &&
    { w=0; until mine || [ $((w+=1)) -gt 50 ]; do nap; done
    while mine; do mv -f "$a" "$t" && cat "$t" && break; nap; done; }; }
    rm -f "$t"; exit 0 ;;
stop) take && { rm -f "$t"; say '{"decision":"block","reason":"' '"}'; exit 0; }
    K=keep O=$f` + owedSuffix + ` awk "$A" "$t" >"$t.o"; k=$?; owe
    queued && ! taken && [ "$n" = "$(turn "$t")" ] && {
        put "$f.stop"; rm -f "$f` + keptSuffix + `" "$f` + actedSuffix + `"; put "$f" "$f` + queuedSuffix + `"; rm -f "$t"; exit 0; }
    rm -f "$f` + queuedSuffix + `"
    [ $k = 0 ] && { rm -f "$f` + actedSuffix + `"; put "$f.stop"; put "$f` + keptSuffix + `"; works || theirs || mv -f "$t" "$f"; rm -f "$t"; exit 0; } ;;
prompt) take && say '{"hookSpecificOutput":{"hookEventName":"UserPromptSubmit","additionalContext":"' '"}}'
    if K=wake O=$f` + owedSuffix + ` awk "$A" "$t" >"$t.o"; then
        owe
        works && { mv -f "$t" "$f` + keptSuffix + `"; rm -f "$t"; exit 0; }
    else
        if mv -f "$f` + announcedSuffix + `" "$t.n"; then rm -f "$t.n"; [ "$u" ] || [ -f "$f` + supervisedSuffix + `" ] || owes || works || : >"$f` + supervisedSuffix + `"
        else rm -f "$f` + supervisedSuffix + `"; fi
        grep -v '^[!+]' "$f` + owedSuffix + `" >"$t.o"; owe
        p=$(turn "$t") && [ -n "$p" ] && works && { [ "$p" = "$(turn "$f")" ] || [ "$p" = "$(cat "$f` + actedSuffix + `")" ]; } && q=1 && put "$f` + queuedSuffix + `"
    fi ;;
answer) o=; grep -q '"agent_id"' "$t" || { o=1; p=$(turn "$t")
        queued && ! taken && [ "$p" != "$n" ] && [ ! -f "$f` + keptSuffix + `" ] && put "$f" "$f` + queuedSuffix + `"
        printf '%s\n' "$p" >"$f` + actedSuffix + `"; rm -f "$f` + queuedSuffix + `"; }
    grep -Eq '` + startedEvent + `' "$t" && w=$(K=tool awk "$A" "$t") && [ -n "$w" ] && printf '%s\n' "$w" >>"$f` + owedSuffix + `"
    grep -q '` + toolEndEvent + `' "$t" && [ "$o" ] && soon && take && say '{"hookSpecificOutput":{"hookEventName":"PostToolUse","additionalContext":"' '"}}'
    ask && [ "$(tool "$f")" = "$(tool "$t")" ] || mcp || { rm -f "$t"; exit 0; } ;;
input) ask && { rm -f "$t"; exit 0; } ;;
result) form || { rm -f "$t"; exit 0; } ;;
end) taken && grep -Eq '` + clearedSession + `' "$t" && { rm -f "$t"; exit 0; } ;;
start) taken && grep -Eq '` + clearedSession + `' "$t" && sid=$(get session_id "$t") &&
    S=$sid K=session awk "$A" "$f` + queuedSuffix + `" >"$t.c" && mv -f "$t.c" "$t"; rm -f "$t.c" ;;
resume) { grep -q '` + startEvent + `' "$f" && grep -q '` + resumed + `' "$f"; } || { cp -f "$f" "$t.p" && mv -f "$t.p" "$f.prev"; }; rm -f "$t.p" ;;
esac
case $1 in dialog | answer | input | result) ;; start | resume | end) rm -f "$f` + keptSuffix + `" "$f` + owedSuffix + `" "$f` + supervisedSuffix + `" "$f` + actedSuffix + `" "$f` + queuedSuffix + `" ;; *) rm -f "$f` + keptSuffix + `"; [ "$q" ] || rm -f "$f` + actedSuffix + `" "$f` + queuedSuffix + `" ;; esac
if mv -f "$t" "$f" && [ "$1" = stop ]; then
    cp -f "$f" "$t" && mv -f "$t" "$f.stop"
fi
rm -f "$t"
[ "$1" = stop ] && [ -f "$f` + supervisedSuffix + `" ] && { owes || rm -f "$f` + supervisedSuffix + `"; exit 0; }
case $1 in stop | dialog | input) [ -n "$2" ] && K=$1 T=$2 L=$l awk "$A" "$f" ;; esac
exit 0`

// hookScript is hookSource as it travels in the settings: without the
// indentation, which is a few hundred bytes of an argument tmux limits
// (design §3.4). No line of the script or its awk depends on it.
var hookScript = func() string {
	lines := strings.Split(hookSource, "\n")
	for i := range lines {
		lines[i] = strings.TrimLeft(lines[i], " \t")
	}
	return strings.Join(lines, "\n")
}()

// dialogEvent is what the hook's grep -E finds in a state file whose
// latest event opened a dialog: a PermissionRequest, or an MCP
// elicitation's form (elicitEvent), with or without spaces.
const dialogEvent = `"hook_event_name"[[:blank:]]*:[[:blank:]]*"(PermissionRequest|Elicitation)"`

// elicitEvent is what the hook's grep finds in a state file whose latest
// event is an MCP elicitation's form, drawn and not yet answered.
const elicitEvent = `"hook_event_name"[[:blank:]]*:[[:blank:]]*"Elicitation"`

// toolEndEvent is what the hook's grep finds in the payload of a tool that
// ended successfully, whose result can carry messages (PostToolUse).
const toolEndEvent = `"hook_event_name"[[:blank:]]*:[[:blank:]]*"PostToolUse"`

// startEvent and resumed are what the hook's grep finds in a state file
// whose latest event started a resumed session.
const (
	startEvent = `"hook_event_name"[[:blank:]]*:[[:blank:]]*"SessionStart"`
	resumed    = `"source"[[:blank:]]*:[[:blank:]]*"` + resumeSource + `"`
)

// clearedSession is what the hook's grep -E finds in the payload of the
// SessionEnd and the SessionStart of /clear.
const clearedSession = `"(reason|source)"[[:blank:]]*:[[:blank:]]*"clear"`

// workingEvent is what the hook's grep -E finds in a state file that says
// the agent works: its latest event is a prompt, or the end of the tool of
// a dialog (PostToolUse or PostToolUseFailure).
const workingEvent = `"hook_event_name"[[:blank:]]*:[[:blank:]]*"(UserPromptSubmit|PostToolUse)`

// startedEvent is what the hook's grep -E finds in the payload of a tool
// that started background work: a subagent or a workflow (async_launched),
// or a shell command or a watch a subagent started (its task id); and of
// a watch the agent itself keeps with the Monitor tool (its taskId).
const startedEvent = `async_launched|"(backgroundTaskId|taskId)"`

// wakeEvent is what the hook's awk finds in the payload of the prompt
// Claude gives itself when background work has finished (design §3.4).
const wakeEvent = `"prompt"[ \t\r\n]*:[ \t\r\n]*"` + wakePrefix

// hookAwk is the awk program of the hook. It reads the state file or the
// payload as one record.
//
// With K=keep it succeeds for the payload of a Stop that leaves the agent
// working (spec §5): background work still runs, or has finished without
// Claude having taken its turn for it yet, and the turn did not end with a
// question. Background work is a task of the payload's background_tasks
// with the status running that is neither a shell command nor a watch (a
// monitor task) the agent itself started: an agent that left a dev server
// running would never be done, and neither would one that published an
// artifact, which arms a watch that runs to the session's end and wakes
// the agent only for a comment (#62). A shell command or a watch one of
// its subagents started (K=tool, kept in O as ~ID) counts while a Stop
// lists it running, and is dropped at the first Stop that does not: Claude
// wakes the subagent for it, not the agent, so no turn is owed for it, and
// the subagent, woken, is listed until it reports. That a subagent which
// left a dev server running keeps the agent working is the price (design
// §3.4). A watch the agent itself keeps with the Monitor tool (K=tool, kept
// in O as =ID) never counts: the mark only lets hq show it as a watch, as
// Claude Code 2.1.302 lists it as a shell (#99); it is kept while a Stop
// lists it running as the agent's own, as ~ID is. A task of any other type
// counts, seen or not: a new kind of Claude's that is real work ending done
// early is the worse fault. busy walks the list to its end, reading the
// id, the type and the status of each task; text inside the strings of a
// task, its description or command, is only ever data. Claude
// takes a turn for every task that ends (wakeEvent, naming its id), and
// tasks that end together are gone from the list before the turns for the
// later ones: so the ids of the tasks that run, less those Claude woke for,
// are kept in the file O (one a line, printed anew on each Stop), and a
// Stop with any of them left is not the agent's end. An id that no longer
// runs is printed with a ! before it: it has held back a turn end, and
// holds back another only when Claude has woken for some task since (the
// wake-ups for tasks that ended together follow one another): a line + says
// so, and lasts until the next Stop. So a wake-up that never comes holds
// back one turn end.
//
// With K=wake it succeeds for a prompt Claude gave itself, and prints O
// without the task it names, and with the + line when that task was
// listed; it prints nothing when it names no task, as nothing can be told
// paid. The task is the one named first, before any other tag: the rest
// of the prompt is the subagent's report, which is only ever data.
//
// With K=tool it prints, for the payload of a tool that started background
// work, the id of the subagent or the workflow the agent started (the
// tool's response says async_launched), so the work is known before any
// Stop lists it, as when the user interrupts that turn. What a subagent
// starts (its events carry agent_id) is the subagent's: Claude wakes the
// subagent for it, not the agent; of that, it prints the task id in the
// tool's response, of a shell command (backgroundTaskId) or a watch (the
// Monitor tool's taskId), as ~ID. Of the agent's own Monitor tool, told by
// the tool's name (the Workflow tool's response has a taskId too), it
// prints the watch's taskId as =ID, which owes nothing.
//
// Otherwise it prints the notification for the event in the state file: on
// dialog and input "Needs input: <branch>", on stop "Question: <branch>"
// when the last assistant message ends with "?" (the rule of IsQuestion,
// for which question walks the message's JSON string to its end), else
// "Done: <branch>", placed at the %s of the sequence T, as Claude's hook
// output {"terminalSequence": ...}; the branch is only ever data.
const hookAwk = `function question(s,    i, n, c, m) {
    i = index(s, "\"last_assistant_message\":")
    if (!i) return 0
    s = substr(s, i + 25)
    sub(/^[ \t\r\n]*"/, "", s)
    n = length(s)
    m = ""
    for (i = 1; i <= n; i++) {
        c = substr(s, i, 1)
        if (c == "\\") { m = m substr(s, i, 2); i++; continue }
        if (c == "\"") break
        m = m c
    }
    for (;;) {
        if (m ~ /[ \t\r\n]$/) m = substr(m, 1, length(m) - 1)
        else if (m ~ /\\[nrt]$/) m = substr(m, 1, length(m) - 2)
        else break
    }
    return m ~ /\?$/
}
function busy(s,    i, c, d, str, key, val, ty, st, id) {
    if (!match(s, /"background_tasks"[ \t\r\n]*:[ \t\r\n]*\[/)) return
    s = substr(s, RSTART + RLENGTH)
    for (i = 1; (c = substr(s, i, 1)) != ""; i++) {
        if (c == "\"") {
            for (str = ""; (c = substr(s, ++i, 1)) != "\"" && c != ""; str = str c) if (c == "\\") i++
            if (d != 1) continue
            if (!val) key = str
            else if (key == "type") ty = str
            else if (key == "status") st = str
            else if (key == "id") id = str
        } else if (c == "{" || c == "[") {
            if (!d++) ty = st = val = id = ""
        } else if (c == "}" || c == "]") {
            if (!d--) return
            if (!d && st == "running") { if (ty != "shell" && ty != "monitor") owed[id] = 1; else if (id in theirs) mine[id] = 1; else if (id in watch) watched[id] = 1 }
        } else if (d == 1 && (c == ":" || c == ",")) val = c == ":"
    }
}
function get(s, key) {
    if (!match(s, "\"" key "\"[ \t\r\n]*:[ \t\r\n]*\"[^\"\\\\]+")) return ""
    s = substr(s, RSTART, RLENGTH)
    sub(/.*"/, "", s)
    return s
}
function load(    o) { return (getline o < ENVIRON["O"]) > 0 ? split(o, L, "\n") : 0 }
BEGIN { RS = "\001"; ends = 1; K = ENVIRON["K"] }
NR == 1 && K == "keep" {
    for (n = load(); n; n--) {
        x = L[n]
        if (x == "+") paid = 1
        else if (sub(/^~/, "", x)) theirs[x]
        else if (sub(/^=/, "", x)) watch[x]
        else if (sub(/^!/, "", x)) held[x]
        else was[x]
    }
    busy($0)
    for (i in owed) { if (i != "") print i; ends = 0 }
    for (i in mine) { if (i != "") print "~" i; ends = 0 }
    for (i in watched) if (i != "") print "=" i
    for (i in was) if (i != "" && !(i in owed)) { print "!" i; ends = 0 }
    if (paid) for (i in held) if (i != "" && !(i in owed) && !(i in was)) { print "!" i; ends = 0 }
    if (question($0)) ends = 1
    next
}
NR == 1 && K == "wake" {
    if (!match($0, /` + wakeEvent + `/)) next
    ends = 0
    o = substr($0, RSTART)
    if (!match(o, /^[^<]*<task-notification>[^<]*<task-id>[^<"\\]+/)) next
    o = substr(o, 1, RLENGTH)
    sub(/.*>/, "", o)
    for (n = load(); n; n--) {
        x = L[n]
        if (x == "+" || x == o || x == "!" o) paid = 1
        else if (x != "") print x
    }
    if (paid) print "+"
    next
}
NR == 1 && K == "get" { print get($0, ENVIRON["N"]); next }
NR == 1 && K == "session" {
    if (ENVIRON["S"] ~ /^[0-9A-Za-z-]+$/) sub(/"session_id"[ \t\r\n]*:[ \t\r\n]*"[^"]*"/, "\"session_id\":\"" ENVIRON["S"] "\"")
    printf "%s", $0
    next
}
NR == 1 && K == "tool" {
    if (!match($0, /"tool_response"[ \t\r\n]*:/)) next
    o = substr($0, RSTART)
    if (get($0, "agent_id") != "") { if ((x = get(o, "(backgroundTaskId|taskId)")) != "") print "~" x }
    else if (o ~ /"status"[ \t\r\n]*:[ \t\r\n]*"async_launched"/ && (x = get(o, "(agentId|taskId)")) != "") print x
    else if (get($0, "tool_name") == "Monitor" && (x = get(o, "taskId")) != "") print "=" x
    next
}
NR == 1 {
    k = "Done"
    if (K != "stop") k = "Needs input"
    else if (question($0)) k = "Question"
    l = ENVIRON["L"]
    gsub(/[[:cntrl:]\\"]/, "", l)
    t = ENVIRON["T"]
    i = index(t, "%s")
    if (i) t = substr(t, 1, i - 1) k ": " l substr(t, i + 2)
    printf "{\"terminalSequence\":\"%s\"}\n", t
}
END { if (K == "keep" || K == "wake") exit ends }`

// branchHeader starts the line the hook writes before the payload.
const branchHeader = "branch"

// attentionNotifications are the Notification kinds that mean the agent
// waits for the user (design §3.4). Claude sends them seconds after the
// dialog appears; a dialog that fired PermissionRequest is reported by then.
// An MCP elicitation's (elicitation_dialog, elicitation_url_dialog) is left
// out: its own hook reports it as it is drawn (#100).
const attentionNotifications = "permission_prompt|agent_needs_input"

// newSessions are the SessionStart sources after which Claude waits at its
// prompt for the user: a new session and one started over by /clear. A
// compaction (compact), which may come in the middle of a turn, is left out;
// a resumed session (resumeSource) has a hook of its own (design §3.4).
const newSessions = "startup|clear"

type hookCommand struct {
	Type    string   `json:"type"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
	Timeout int      `json:"timeout,omitempty"` // seconds; Claude's default when 0
}

type hookMatcher struct {
	Matcher string        `json:"matcher,omitempty"`
	Hooks   []hookCommand `json:"hooks"`
}

// hookEnv is the environment variable that carries the script: every hook
// runs the one copy of it, which keeps the settings, an argument of sbx run,
// within what Windows takes for a command line on WSL.
const hookEnv = "HQ_HOOK"

// hook runs the script for an event of kind; notify, when set, is the
// notification sequence the event sends.
func hook(kind, notify string) hookCommand {
	args := []string{"-c", `eval "$` + hookEnv + `"`, "hq-hook", kind}
	if notify != "" {
		args = append(args, notify)
	}
	return hookCommand{Type: "command", Command: "sh", Args: args}
}

// holdTimeout is the timeout of the hook that holds on a question dialog
// for hq's answer, in seconds: a day, which Claude honours (#89), so it
// never ends the hold while a user may still answer through hq; at the
// timeout Claude ends the hook and the dialog stays open.
const holdTimeout = 86400

// holding is h with the timeout of a hook that holds.
func holding(h hookCommand) hookCommand {
	h.Timeout = holdTimeout
	return h
}

// inTmux tells Claude its terminal is tmux, which it is: sbx does not pass
// TMUX into the sandbox. Claude then wraps a hook's notification sequence for
// tmux passthrough; tmux drops the sequence unwrapped (design §3.5).
const inTmux = "hq"

// Settings is the --settings hq gives Claude: the agent's identity and the
// hook script as environment, and the hook set that reports its state (ADR
// 0009) and sends notifications with the platform's sequence (ADR 0010).
func Settings(name, id, notify string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false) // the script's redirections stay readable
	_ = enc.Encode(map[string]any{
		"env": map[string]string{"HQ_AGENT": name, "HQ_ID": id, "TMUX": inTmux, hookEnv: hookScript},
		"hooks": map[string][]hookMatcher{
			"UserPromptSubmit": {{Hooks: []hookCommand{hook("prompt", "")}}},
			"PermissionRequest": {
				{Hooks: []hookCommand{hook("dialog", notify)}},
				{Matcher: AskTool, Hooks: []hookCommand{holding(hook("hold", ""))}},
			},
			"PostToolUse":        {{Hooks: []hookCommand{hook("answer", "")}}},
			"PostToolUseFailure": {{Hooks: []hookCommand{hook("answer", "")}}},
			"Stop":               {{Hooks: []hookCommand{hook("stop", notify)}}},
			"Notification":       {{Matcher: attentionNotifications, Hooks: []hookCommand{hook("input", notify)}}},
			"SessionStart": {
				{Matcher: newSessions, Hooks: []hookCommand{hook("start", "")}},
				{Matcher: resumeSource, Hooks: []hookCommand{hook("resume", "")}},
			},
			"SessionEnd":        {{Hooks: []hookCommand{hook("end", "")}}},
			"Elicitation":       {{Hooks: []hookCommand{hook("dialog", notify)}}},
			"ElicitationResult": {{Hooks: []hookCommand{hook("result", "")}}},
		},
	})
	return strings.TrimSpace(b.String())
}
