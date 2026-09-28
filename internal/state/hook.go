package state

import (
	"bytes"
	"encoding/json"
	"strings"
)

// hookScript runs inside the sandbox on each lifecycle event, in Claude's
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
// write to its terminal (design §3.5). It needs only sh, git, cat, mv, cp,
// mkdir, rm, grep and awk, never blocks Claude and always exits 0 (design
// §3.4, §7.1).
const hookScript = `case $HQ_ID in '' | *[!0123456789abcdef]*) exit 0 ;; esac
b=$(git branch --show-current 2>/dev/null)
l=${b:-${PWD##*/}}
cd "${CLAUDE_PROJECT_DIR:-.}" 2>/dev/null
d=$(git rev-parse --git-common-dir 2>/dev/null) || exit 0
d=$d/hq/agents
mkdir -p "$d" 2>/dev/null || exit 0
f=$d/$HQ_ID
t=$f.$$
{ printf '%s %s\n' ` + branchHeader + ` "$b" && cat; } >"$t" 2>/dev/null || { rm -f "$t"; exit 0; }
tool() { awk 'match($0, /"tool_name"[[:blank:]]*:[[:blank:]]*"[^"]*"/) { print substr($0, RSTART, RLENGTH); exit }' "$1" 2>/dev/null; }
case $1 in
answer) grep -q '` + dialogEvent + `' "$f" 2>/dev/null && [ "$(tool "$f")" = "$(tool "$t")" ] || { rm -f "$t"; exit 0; } ;;
input) grep -q '` + dialogEvent + `' "$f" 2>/dev/null && { rm -f "$t"; exit 0; } ;;
resume) { grep -q '` + startEvent + `' "$f" && grep -q '` + resumed + `' "$f"; } 2>/dev/null || { cp -f "$f" "$t.p" && mv -f "$t.p" "$f.prev"; } 2>/dev/null; rm -f "$t.p" ;;
esac
if mv -f "$t" "$f" 2>/dev/null && [ "$1" = stop ]; then
    cp -f "$f" "$t" 2>/dev/null && mv -f "$t" "$f.stop" 2>/dev/null
fi
rm -f "$t" 2>/dev/null
case $1 in stop | dialog | input) [ -n "$2" ] && K=$1 T=$2 L=$l awk '` + notifyAwk + `' "$f" 2>/dev/null ;; esac
exit 0`

// dialogEvent is what the hook's grep finds in a state file whose latest
// event opened a dialog: a PermissionRequest, with or without spaces.
const dialogEvent = `"hook_event_name"[[:blank:]]*:[[:blank:]]*"PermissionRequest"`

// startEvent and resumed are what the hook's grep finds in a state file
// whose latest event started a resumed session.
const (
	startEvent = `"hook_event_name"[[:blank:]]*:[[:blank:]]*"SessionStart"`
	resumed    = `"source"[[:blank:]]*:[[:blank:]]*"` + resumeSource + `"`
)

// notifyAwk prints the notification for the event in the state file: on
// dialog and input "Needs input: <branch>", on stop "Question: <branch>"
// when the last assistant message ends with "?" (the rule of IsQuestion),
// else "Done: <branch>", placed at the %s of the sequence T, as Claude's hook output
// {"terminalSequence": ...}. It reads the file as one record and walks the
// message's JSON string to its end; the branch is only ever data.
const notifyAwk = `function question(s,    i, n, c, m) {
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
BEGIN { RS = "\001" }
NR == 1 {
    k = "Done"
    if (ENVIRON["K"] != "stop") k = "Needs input"
    else if (question($0)) k = "Question"
    l = ENVIRON["L"]
    gsub(/[[:cntrl:]\\"]/, "", l)
    t = ENVIRON["T"]
    i = index(t, "%s")
    if (i) t = substr(t, 1, i - 1) k ": " l substr(t, i + 2)
    printf "{\"terminalSequence\":\"%s\"}\n", t
}`

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
