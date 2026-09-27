package state

import (
	"bytes"
	"encoding/json"
	"strings"
)

// hookScript runs inside the sandbox on each lifecycle event, with Claude's
// event payload on stdin and the event's kind in $1. It copies the payload
// to the main repository's .git/hq/agents/$HQ_ID, replacing it in one step,
// and keeps the latest Stop event beside it (.stop) for the last message. It
// needs only sh, git, cat, mv and cp, never blocks Claude and always exits 0
// (design §3.4, §7.1).
const hookScript = `case $HQ_ID in '' | *[!0123456789abcdef]*) exit 0 ;; esac
cd "${CLAUDE_PROJECT_DIR:-.}" 2>/dev/null
d=$(git rev-parse --git-common-dir 2>/dev/null) || exit 0
d=$d/hq/agents
mkdir -p "$d" 2>/dev/null || exit 0
f=$d/$HQ_ID
if cat >"$f.$$" 2>/dev/null && mv -f "$f.$$" "$f" 2>/dev/null && [ "$1" = stop ]; then
    cp -f "$f" "$f.$$" 2>/dev/null && mv -f "$f.$$" "$f.stop" 2>/dev/null
fi
rm -f "$f.$$" 2>/dev/null
exit 0`

// attentionNotifications are the Notification kinds that mean the agent
// waits for the user (design §3.4).
const attentionNotifications = "permission_prompt|agent_needs_input|elicitation_dialog"

type hookCommand struct {
	Type    string   `json:"type"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

type hookMatcher struct {
	Matcher string        `json:"matcher,omitempty"`
	Hooks   []hookCommand `json:"hooks"`
}

func hook(kind string) hookCommand {
	return hookCommand{Type: "command", Command: "sh", Args: []string{"-c", hookScript, "hq-hook", kind}}
}

// Settings is the --settings hq gives Claude: the agent's identity as
// environment, and the hook set that reports its state (ADR 0009).
func Settings(name, id string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false) // the script's redirections stay readable
	_ = enc.Encode(map[string]any{
		"env": map[string]string{"HQ_AGENT": name, "HQ_ID": id},
		"hooks": map[string][]hookMatcher{
			"UserPromptSubmit": {{Hooks: []hookCommand{hook("prompt")}}},
			"Stop":             {{Hooks: []hookCommand{hook("stop")}}},
			"Notification":     {{Matcher: attentionNotifications, Hooks: []hookCommand{hook("input")}}},
			"SessionEnd":       {{Hooks: []hookCommand{hook("end")}}},
		},
	})
	return strings.TrimSpace(b.String())
}
