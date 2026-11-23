package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/journal/journaltest"
	"github.com/rkrysinski/hq/internal/state"
)

// The supervisor's list takes names, as hq ls takes them: those agents,
// in attention order; a name no agent has is the command's error; without
// names, or with none, every agent (#291).
func TestMCPListListsTheAgentsNamed(t *testing.T) {
	cs := mcpSession(t, threeAgents())
	for _, c := range []struct {
		args map[string]any
		want string
	}{
		{nil, "c b a"},
		{map[string]any{"names": []string{}}, "c b a"},
		{map[string]any{"names": []string{"a"}}, "a"},
		{map[string]any{"names": []string{"a", "c", "a"}}, "c a"},
	} {
		out, isErr := call(t, cs, "list", c.args)
		if isErr {
			t.Fatalf("%v: %s", c.args, out)
		}
		if got := lsNames(t, out); got != c.want {
			t.Errorf("%v: %q, want %q", c.args, got, c.want)
		}
	}
	if out, isErr := call(t, cs, "list", map[string]any{"names": []string{"a", "nobody"}}); !isErr || out != noAgent("nobody") {
		t.Fatalf("unknown agent %v %q", isErr, out)
	}
}

// The rows of the supervisor's list and wait carry last cut to 120
// characters, and read gives it whole (#291).
func TestMCPListAndWaitCutLastAndReadGivesItWhole(t *testing.T) {
	f := sendFakes(state.Done)
	long := strings.Repeat("ą", 300)
	f.states["id-a"] = journaltest.HookState{State: state.Done, Since: f.now.Add(-time.Second), Last: long}
	f.details["id-a"] = state.Detail{Reply: long}
	cs := mcpSession(t, f)
	cut := strings.Repeat("ą", 119) + "…"
	for tool, args := range map[string]map[string]any{
		"list": {"names": []string{"a"}},
		"wait": {"since": "c0", "timeout": 1},
	} {
		out, isErr := call(t, cs, tool, args)
		if isErr || lastOf(t, []string{tool}, out) != cut {
			t.Errorf("%s: %s", tool, out)
		}
	}
	var r struct {
		Last string `json:"last"`
	}
	if out, isErr := call(t, cs, "read", map[string]any{"name": "a"}); isErr || json.Unmarshal([]byte(out), &r) != nil || r.Last != long {
		t.Fatalf("read: %s", out)
	}
}

// The descriptions of list and wait and the server's instructions name
// list's names, the cursor of a list with names (for a wait that names the
// same agents; a watch over every agent keeps its own) and where the whole
// last is read (#291).
func TestMCPTextsNameTheNamesTheirCursorAndTheWholeLast(t *testing.T) {
	cs := mcpSession(t, newLsFakes())
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	tools := map[string]string{}
	schemas := map[string]string{}
	for _, tl := range res.Tools {
		tools[tl.Name] = tl.Description
		s, _ := json.Marshal(tl.InputSchema)
		schemas[tl.Name] = string(s)
	}
	instructions := cs.InitializeResult().Instructions
	for where, wants := range map[string][]string{
		"list": {
			"List the agents named in names, every agent when names is empty or absent",
			"last (its last message, one line, cut to 120 characters: read gives it whole)",
			"With names, next_since is for a wait that names the same agents: a watch over every agent keeps the next_since of its own wait or of a list without names",
			"A name no agent has is refused, and nothing is listed",
		},
		"wait":  {"last cut as list cuts it: read gives it whole"},
		"read":  {"last whole, where list and wait cut it to 120 characters"},
		"send":  {"the agent as wait gives it"},
		"names": {"the agents to list; every agent when empty"},
		"instructions": {
			"list for every agent or those you name",
			"its whole last message",
			"A list that names agents gives a next_since for a wait that names the same agents; a watch over every agent keeps the next_since of its own wait or of a list without names.",
		},
	} {
		text := tools[where]
		switch where {
		case "names":
			text = schemas["list"]
		case "instructions":
			text = instructions
		}
		for _, want := range wants {
			if !strings.Contains(text, want) {
				t.Errorf("%s: no %q in %q", where, want, text)
			}
		}
	}
}
