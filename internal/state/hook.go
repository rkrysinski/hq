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
// open dialog (the latest event is a PermissionRequest) decides two kinds:
// answer (a tool ran) is written only when it closes that dialog, the same
// tool having run, and input (Claude's Notification) only when no dialog is
// open, so a dialog is reported once, when it opens (design §3.4). A resumed
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
// that names no task, or a session's start or end, forgets them all. On answer it first adds to .owe the background work
// the tool started, if any (hookAwk).
//
// It needs only sh, git, cat, mv, cp, mkdir, rm, grep and awk and always
// exits 0; it holds Claude back only while a message is unread (design
// §3.4, §7.1).
const hookSource = `case $HQ_ID in '' | *[!0123456789abcdef]*) exit 0 ;; esac
b=$(git branch --show-current 2>/dev/null)
l=${b:-${PWD##*/}}
cd "${CLAUDE_PROJECT_DIR:-.}" 2>/dev/null
g=$(git rev-parse --git-common-dir 2>/dev/null) || exit 0
d=$g/hq/agents
i=$g/hq/inbox/$HQ_ID
mkdir -p "$d" 2>/dev/null || exit 0
f=$d/$HQ_ID
t=$f.$$
{ printf '%s %s\n' ` + branchHeader + ` "$b" && cat; } >"$t" 2>/dev/null || { rm -f "$t"; exit 0; }
A='` + hookAwk + `'
works() { grep -Eq '` + workingEvent + `' "$f" 2>/dev/null; }
theirs() { grep -q '` + dialogEvent + `' "$f" 2>/dev/null && grep -q '"agent_id"' "$f" 2>/dev/null; }
owe() { [ -s "$t.o" ] && mv -f "$t.o" "$f` + owedSuffix + `" 2>/dev/null || rm -f "$t.o" "$f` + owedSuffix + `" 2>/dev/null; }
put() { cp -f "$t" "$t.c" 2>/dev/null && mv -f "$t.c" "$1" 2>/dev/null; rm -f "$t.c" 2>/dev/null; }
tool() { awk 'match($0, /"tool_name"[[:blank:]]*:[[:blank:]]*"[^"]*"/) { print substr($0, RSTART, RLENGTH); exit }' "$1" 2>/dev/null; }
soon() { for m in "$i"/[0-9]*` + nowSuffix + `; do [ -f "$m" ] && return 0; done; return 1; }
take() {
    c=$i/.taken.$$
    [ -d "$i" ] && rm -rf "$c" 2>/dev/null && mkdir "$c" 2>/dev/null || return 1
    for m in "$i"/[0-9]*; do [ -f "$m" ] && mv "$m" "$c/" 2>/dev/null; done
    for m in "$c"/*; do [ -f "$m" ] && return 0; done
    rm -rf "$c" 2>/dev/null; return 1
}
say() {
    printf '%s' "$1"; s=
    for m in "$c"/*; do [ -f "$m" ] && printf '%s%s' "$s" '` + MessageLabel + `' && cat "$m" 2>/dev/null && s='\n\n'; done
    rm -rf "$c" 2>/dev/null; printf '%s\n' "$2"
}
case $1 in
stop) take && { rm -f "$t"; say '{"decision":"block","reason":"' '"}'; exit 0; }
    K=keep O=$f` + owedSuffix + ` awk "$A" "$t" >"$t.o" 2>/dev/null; k=$?; owe
    [ $k = 0 ] && { put "$f.stop"; put "$f` + keptSuffix + `"; works || theirs || mv -f "$t" "$f" 2>/dev/null; rm -f "$t"; exit 0; } ;;
prompt) take && say '{"hookSpecificOutput":{"hookEventName":"UserPromptSubmit","additionalContext":"' '"}}'
    if K=wake O=$f` + owedSuffix + ` awk "$A" "$t" >"$t.o" 2>/dev/null; then
        owe
        works && { mv -f "$t" "$f` + keptSuffix + `" 2>/dev/null; rm -f "$t"; exit 0; }
    else grep -v '^[!+]' "$f` + owedSuffix + `" >"$t.o" 2>/dev/null; owe; fi ;;
answer) grep -Eq '` + startedEvent + `' "$t" 2>/dev/null && w=$(K=tool awk "$A" "$t" 2>/dev/null) && [ -n "$w" ] && printf '%s\n' "$w" >>"$f` + owedSuffix + `"
    grep -q '` + toolEndEvent + `' "$t" 2>/dev/null && ! grep -q '"agent_id"' "$t" 2>/dev/null && soon && take && say '{"hookSpecificOutput":{"hookEventName":"PostToolUse","additionalContext":"' '"}}'
    grep -q '` + dialogEvent + `' "$f" 2>/dev/null && [ "$(tool "$f")" = "$(tool "$t")" ] || { rm -f "$t"; exit 0; } ;;
input) grep -q '` + dialogEvent + `' "$f" 2>/dev/null && { rm -f "$t"; exit 0; } ;;
resume) { grep -q '` + startEvent + `' "$f" && grep -q '` + resumed + `' "$f"; } 2>/dev/null || { cp -f "$f" "$t.p" && mv -f "$t.p" "$f.prev"; } 2>/dev/null; rm -f "$t.p" ;;
esac
case $1 in dialog | answer | input) ;; start | resume | end) rm -f "$f` + keptSuffix + `" "$f` + owedSuffix + `" 2>/dev/null ;; *) rm -f "$f` + keptSuffix + `" 2>/dev/null ;; esac
if mv -f "$t" "$f" 2>/dev/null && [ "$1" = stop ]; then
    cp -f "$f" "$t" 2>/dev/null && mv -f "$t" "$f.stop" 2>/dev/null
fi
rm -f "$t" 2>/dev/null
case $1 in stop | dialog | input) [ -n "$2" ] && K=$1 T=$2 L=$l awk "$A" "$f" 2>/dev/null ;; esac
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

// dialogEvent is what the hook's grep finds in a state file whose latest
// event opened a dialog: a PermissionRequest, with or without spaces.
const dialogEvent = `"hook_event_name"[[:blank:]]*:[[:blank:]]*"PermissionRequest"`

// toolEndEvent is what the hook's grep finds in the payload of a tool that
// ended successfully, whose result can carry messages (PostToolUse).
const toolEndEvent = `"hook_event_name"[[:blank:]]*:[[:blank:]]*"PostToolUse"`

// startEvent and resumed are what the hook's grep finds in a state file
// whose latest event started a resumed session.
const (
	startEvent = `"hook_event_name"[[:blank:]]*:[[:blank:]]*"SessionStart"`
	resumed    = `"source"[[:blank:]]*:[[:blank:]]*"` + resumeSource + `"`
)

// workingEvent is what the hook's grep -E finds in a state file that says
// the agent works: its latest event is a prompt, or the end of the tool of
// a dialog (PostToolUse or PostToolUseFailure).
const workingEvent = `"hook_event_name"[[:blank:]]*:[[:blank:]]*"(UserPromptSubmit|PostToolUse)`

// startedEvent is what the hook's grep -E finds in the payload of a tool
// that started background work: a subagent or a workflow.
const startedEvent = `async_launched`

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
// with the status running that is no shell command (an agent that left a
// dev server running would never be done). busy walks the list to its end,
// reading the id, the type and the status of each task; text inside the
// strings of a task, its description or command, is only ever data. Claude
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
// without the tasks it names, and with the + line when it named one of
// them; it prints nothing when it names no task, as nothing can be told
// paid.
//
// With K=tool it prints, for the payload of a tool that started background
// work, the id of the subagent or the workflow the agent started (the
// tool's response says async_launched), so the work is known before any
// Stop lists it, as when the user interrupts that turn. What a subagent
// starts (its events carry agent_id) is the subagent's: Claude wakes the
// subagent for it, not the agent.
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
            if (!d && st == "running" && ty != "shell") owed[id] = 1
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
        else if (sub(/^!/, "", x)) held[x]
        else was[x]
    }
    busy($0)
    for (i in owed) { if (i != "") print i; ends = 0 }
    for (i in was) if (i != "" && !(i in owed)) { print "!" i; ends = 0 }
    if (paid) for (i in held) if (i != "" && !(i in owed) && !(i in was)) { print "!" i; ends = 0 }
    if (question($0)) ends = 1
    next
}
NR == 1 && K == "wake" {
    if (!match($0, /` + wakeEvent + `/)) next
    ends = 0
    o = substr($0, RSTART)
    for (n = 0; match(o, /<task-id>[^<"\\]+/); n++) {
        woke[substr(o, RSTART + 9, RLENGTH - 9)]
        o = substr(o, RSTART + RLENGTH)
    }
    if (!n) next
    for (n = load(); n; n--) {
        x = o = L[n]
        sub(/^!/, "", o)
        if (x == "+" || o in woke) paid = 1
        else if (x != "") print x
    }
    if (paid) print "+"
    next
}
NR == 1 && K == "tool" {
    if (get($0, "agent_id") == "" && match($0, /"tool_response"[ \t\r\n]*:/)) {
        o = substr($0, RSTART)
        if (o ~ /"status"[ \t\r\n]*:[ \t\r\n]*"async_launched"/ && (x = get(o, "(agentId|taskId)")) != "") print x
    }
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
const attentionNotifications = "permission_prompt|agent_needs_input|elicitation_dialog"

// newSessions are the SessionStart sources after which Claude waits at its
// prompt for the user: a new session and one started over by /clear. A
// compaction (compact), which may come in the middle of a turn, is left out;
// a resumed session (resumeSource) has a hook of its own (design §3.4).
const newSessions = "startup|clear"

type hookCommand struct {
	Type    string   `json:"type"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

type hookMatcher struct {
	Matcher string        `json:"matcher,omitempty"`
	Hooks   []hookCommand `json:"hooks"`
}

// hookEnv is the environment variable that carries the script: every hook
// runs the one copy of it, which keeps the settings, an argument of the
// agent's tmux window, within what tmux accepts for a command.
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
			"UserPromptSubmit":   {{Hooks: []hookCommand{hook("prompt", "")}}},
			"PermissionRequest":  {{Hooks: []hookCommand{hook("dialog", notify)}}},
			"PostToolUse":        {{Hooks: []hookCommand{hook("answer", "")}}},
			"PostToolUseFailure": {{Hooks: []hookCommand{hook("answer", "")}}},
			"Stop":               {{Hooks: []hookCommand{hook("stop", notify)}}},
			"Notification":       {{Matcher: attentionNotifications, Hooks: []hookCommand{hook("input", notify)}}},
			"SessionStart": {
				{Matcher: newSessions, Hooks: []hookCommand{hook("start", "")}},
				{Matcher: resumeSource, Hooks: []hookCommand{hook("resume", "")}},
			},
			"SessionEnd": {{Hooks: []hookCommand{hook("end", "")}}},
		},
	})
	return strings.TrimSpace(b.String())
}
