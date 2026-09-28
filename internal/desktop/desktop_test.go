package desktop

import (
	"encoding/json"
	"strings"
	"testing"
)

var hq = Server{Command: "/Users/dev/.local/bin/hq", Args: []string{"mcp"}, Env: map[string]string{"PATH": "/opt/homebrew/bin:/usr/bin"}}

// A configuration as Claude Desktop keeps it: preferences, another MCP
// server with a secret in its environment.
const theirs = `{
  "mcpServers": {
    "github": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-github"],
      "env": {"GITHUB_TOKEN": "secret-123"}
    }
  },
  "preferences": {"quickEntryShortcut": "off", "zoom": 1.25}
}`

func TestMergeAddsHqAndKeepsEverythingElseInItsPlace(t *testing.T) {
	out, outcome, err := Merge([]byte(theirs), hq)
	if err != nil || outcome != Added {
		t.Fatalf("%v %v", outcome, err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	servers := got["mcpServers"].(map[string]any)
	gh := servers["github"].(map[string]any)
	if gh["env"].(map[string]any)["GITHUB_TOKEN"] != "secret-123" || gh["command"] != "npx" {
		t.Errorf("the other server changed: %v", gh)
	}
	if got["preferences"].(map[string]any)["zoom"] != 1.25 {
		t.Errorf("preferences changed: %v", got["preferences"])
	}
	h := servers["hq"].(map[string]any)
	if h["command"] != hq.Command || h["args"].([]any)[0] != "mcp" || h["env"].(map[string]any)["PATH"] != hq.Env["PATH"] {
		t.Errorf("hq %v", h)
	}
	s := string(out)
	if !(strings.Index(s, `"mcpServers"`) < strings.Index(s, `"preferences"`) && strings.Index(s, `"github"`) < strings.Index(s, `"hq"`)) {
		t.Errorf("order changed:\n%s", s)
	}
	if !strings.HasSuffix(s, "}\n") || !strings.Contains(s, "\n  \"preferences\"") {
		t.Errorf("not indented as Claude Desktop writes it:\n%s", s)
	}
}

func TestMergeTwiceChangesNothing(t *testing.T) {
	once, _, _ := Merge([]byte(theirs), hq)
	twice, outcome, err := Merge(once, hq)
	if err != nil || outcome != Unchanged || string(twice) != string(once) {
		t.Fatalf("%v %v\n%s", outcome, err, twice)
	}
}

func TestMergeUpdatesHqInItsPlace(t *testing.T) {
	old := `{"mcpServers":{"hq":{"command":"/old/hq","args":["mcp"]},"github":{"command":"npx"}}}`
	out, outcome, err := Merge([]byte(old), hq)
	if err != nil || outcome != Updated {
		t.Fatalf("%v %v", outcome, err)
	}
	s := string(out)
	if strings.Contains(s, "/old/hq") || strings.Index(s, `"hq"`) > strings.Index(s, `"github"`) {
		t.Fatalf("%s", s)
	}
}

func TestMergeStartsFromNothing(t *testing.T) {
	for _, config := range []string{"", " \n", "{}", `{"mcpServers": null}`, `{"mcpServers": {}}`} {
		out, outcome, err := Merge([]byte(config), hq)
		var got struct {
			MCPServers map[string]Server `json:"mcpServers"`
		}
		if err != nil || outcome != Added || json.Unmarshal(out, &got) != nil || got.MCPServers["hq"].Command != hq.Command || len(got.MCPServers) != 1 {
			t.Errorf("%q: %v %v\n%s", config, outcome, err, out)
		}
	}
}

func TestMergeRefusesWhatItCannotEditSafely(t *testing.T) {
	for _, config := range []string{
		`{"mcpServers": {`,                     // cut off
		`// comment` + "\n{}",                  // not JSON
		`[]`,                                   // not an object
		`{"a": 1} {"b": 2}`,                    // two values
		`{"mcpServers": {}, "mcpServers": {}}`, // which one counts?
		`{"mcpServers": {"hq": {}, "hq": {}}}`, // the same inside
		`{"mcpServers": ["hq"]}`,               // not an object
		`{"mcpServers": "hq"}`,                 // not an object
	} {
		if out, _, err := Merge([]byte(config), hq); err == nil {
			t.Errorf("%q: no error\n%s", config, out)
		}
	}
}

func TestMergeLeavesEscapesAndNumbersAsTheyAre(t *testing.T) {
	config := `{"a": "é\n", "n": 12345678901234567890, "mcpServers": {}}`
	out, _, err := Merge([]byte(config), hq)
	if err != nil || !strings.Contains(string(out), `"é\n"`) || !strings.Contains(string(out), "12345678901234567890") {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestSnippetIsHqsEntryAlone(t *testing.T) {
	s := Snippet(hq)
	var got map[string]map[string]Server
	if err := json.Unmarshal([]byte(s), &got); err != nil || got["mcpServers"]["hq"].Command != hq.Command || len(got) != 1 || len(got["mcpServers"]) != 1 {
		t.Fatalf("%v\n%s", err, s)
	}
	if noEnv := Snippet(Server{Command: "wsl.exe", Args: []string{"--exec", "/hq", "mcp"}}); strings.Contains(noEnv, "env") {
		t.Errorf("an empty environment is written:\n%s", noEnv)
	}
}
