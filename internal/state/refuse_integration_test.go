//go:build integration

package state

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rkrysinski/hq/internal/testutil"
)

// refuse runs the PreToolUse hook as Claude runs it in the session of the
// agent named agent, and returns what it printed.
func refuse(t *testing.T, dir, agent string, payload []byte) string {
	t.Helper()
	h := hook("refuse", "")
	cmd := exec.Command(h.Command, h.Args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "HQ_AGENT="+agent, "HQ_ID=0123abcd", "CLAUDE_PROJECT_DIR="+dir, hookEnv+"="+hookScript)
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil || stderr.Len() != 0 {
		t.Fatalf("hook refuse: %v %q", err, stderr.String())
	}
	return string(out)
}

// call is a fixture's PreToolUse payload with its tool's name and input
// replaced.
func call(t *testing.T, fixtureName, tool, input string) []byte {
	t.Helper()
	var p map[string]json.RawMessage
	if err := json.Unmarshal(fixture(t, fixtureName), &p); err != nil {
		t.Fatal(err)
	}
	if tool != "" {
		p["tool_name"], _ = json.Marshal(tool)
	}
	if input != "" {
		p["tool_input"] = json.RawMessage(input)
	}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestKillAnswerAndSendRefuseTheCallingAgent(t *testing.T) {
	root := testutil.GitRepo(t, "app")
	for verb, reason := range map[string]string{
		"kill":   "hq: kill refuses sup, the calling agent: an agent does not end itself; the user ends it (hq kill sup)",
		"answer": "hq: answer refuses sup, the calling agent: an agent does not answer its own dialog",
		"send":   "hq: send refuses sup, the calling agent: an agent does not send to itself",
	} {
		// Through hq's own server, and through sbx's gateway (#141).
		for _, tool := range []string{"mcp__hq__" + verb, "mcp__mcp-gateway__" + verb} {
			out := refuse(t, root, "sup", call(t, "pre-tool-"+verb, tool, ""))
			var d struct {
				HookSpecificOutput struct {
					HookEventName, PermissionDecision, PermissionDecisionReason string
				}
			}
			if err := json.Unmarshal([]byte(out), &d); err != nil {
				t.Fatalf("%s: %q: %v", tool, out, err)
			}
			o := d.HookSpecificOutput
			if o.HookEventName != "PreToolUse" || o.PermissionDecision != "deny" || o.PermissionDecisionReason != reason {
				t.Errorf("%s: %+v", tool, o)
			}
		}
	}
	// The refusal writes nothing: the agent's state is its turn's.
	if _, err := os.Stat(filepath.Join(root, ".git", "hq")); !os.IsNotExist(err) {
		t.Errorf("the refusal wrote the agent's state: %v", err)
	}
}

func TestOnlyACallOfHqsShapeNamingTheCallerIsRefused(t *testing.T) {
	root := testutil.GitRepo(t, "app")
	for _, c := range []struct {
		why, agent, fixture, tool, input string
		refused                          bool
	}{
		{"another agent", "other", "pre-tool-kill", "", "", false},
		{"no agent: not hq's session", "", "pre-tool-kill", "", "", false},
		{"a kill not yet confirmed", "sup", "pre-tool-kill", "", `{"name":"sup"}`, true},
		{"a send at once", "sup", "pre-tool-send", "", `{"name":"sup","text":"x","now":true}`, true},
		{"an answer that cancels", "sup", "pre-tool-answer", "", `{"dialog":"d1","cancel":true,"name":"sup","confirmed":true}`, true},
		{"another server's kill with a key hq's has not", "sup", "pre-tool-kill", "", `{"name":"sup","signal":"KILL"}`, false},
		{"a send without its text", "sup", "pre-tool-send", "", `{"name":"sup"}`, false},
		{"an answer without its dialog", "sup", "pre-tool-answer", "", `{"name":"sup","confirmed":true}`, false},
		{"the name inside a message", "sup", "pre-tool-send", "", `{"name":"other","text":"{\"name\":\"sup\"}"}`, false},
		{"the caller's name only inside another key", "sup", "pre-tool-send", "", `{"name":"other","text":"sup"}`, false},
		{"a name with an escape", "sup", "pre-tool-kill", "", `{"name":"su` + `\` + `u0070","confirmed":true}`, false},
		{"another tool", "sup", "pre-tool-kill", "mcp__hq__killall", "", false},
		{"not an MCP tool", "sup", "pre-tool-kill", "kill", "", false},
		{"hq's read", "sup", "pre-tool-kill", "mcp__hq__read", `{"name":"sup"}`, false},
	} {
		out := refuse(t, root, c.agent, call(t, c.fixture, c.tool, c.input))
		if got := strings.Contains(out, `"permissionDecision":"deny"`); got != c.refused || (!got && out != "") {
			t.Errorf("%s: printed %q", c.why, out)
		}
	}
	// Claude's payload is compact, but JSON may have spaces anywhere.
	spaced := bytes.Replace(fixture(t, "pre-tool-kill"), []byte(`{"name":"sup","confirmed":true}`), []byte("{ \"name\" :\n \"sup\" , \"confirmed\" : true }"), 1)
	if out := refuse(t, root, "sup", spaced); !strings.Contains(out, `"permissionDecision":"deny"`) {
		t.Errorf("spaces between the tokens: printed %q", out)
	}
}
