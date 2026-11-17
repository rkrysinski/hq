package version_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/state"
	"github.com/rkrysinski/hq/internal/version"
)

// pinned is the generation the forms of what agents report and of how hq
// keeps it were last pinned at, with a fingerprint of each (ADR 0020). It
// keeps the update rule from depending on memory: a change to one of the
// forms fails TestTheGenerationFollowsTheForms until it is pinned again.
//
// To pin again, set generation and the changed fingerprints to what the
// test prints. Raise version.Generation with them when what agents report,
// or the form hq keeps it in, changed; an hq of one generation cannot read
// an agent of another, and hq update refuses to cross generations while
// agents exist. A renewal that keeps the generation says in the pull
// request why what agents report is unchanged.
var pinned = struct {
	generation int
	forms      map[string]string
}{
	generation: 5,
	forms: map[string]string{
		"the hook's script": "cd9a650cb874da7b",
		"the record":        "7d2c1be547e68487",
		"the journal":       "acb23b5dfe1fdd87",
		"the events taken":  "2806c785dc8d84a4",
	},
}

// forms are the forms the generation stands for, each as text whose
// fingerprint changes with it: the hook set and script hq injects into
// every agent it starts (state.Settings, ADR 0009), hq's record of an
// agent for hq restore (state.Record), the journal (ADR 0018): an event,
// one a line, and the reply and the dialog kept beside it, and what the
// host takes of the hook's event files into the journal (state.ParseEvent,
// on probes), so that a change of which facts it takes from a payload
// demands a pin too.
func forms() map[string]string {
	return map[string]string{
		"the hook's script": state.Settings("NAME", "ID", "NOTIFY"),
		"the record":        form(reflect.TypeOf(state.Record{})),
		"the journal":       form(reflect.TypeOf(state.Event{})) + "\n" + form(reflect.TypeOf(state.Reply{})) + "\n" + form(reflect.TypeOf(state.Asked{})),
		"the events taken":  taken(),
	}
}

// probe is a payload of hook event kind with every field Claude may send
// that the host could take, so that what it takes shows in its event.
func probe(kind, prompt string) []byte {
	p := map[string]any{
		"hook_event_name": kind, "session_id": "s1", "prompt_id": "p1", "cwd": "/w/app", "agent_id": "", "tool_name": "AskUserQuestion",
		"mcp_server_name": "srv", "source": "resume", "reason": "other", "notification_type": "permission_prompt", "message": "Allow?",
		"prompt": prompt, "last_assistant_message": "Red or blue?", "error": "authentication_failed",
		"background_tasks": []map[string]string{{"id": "t1", "type": "local_agent", "status": "running", "description": "work", "command": "make"}},
		"tool_input":       map[string]any{"questions": []map[string]string{{"question": "Red?"}}, "description": "d", "command": "c", "file_path": "f", "url": "u"},
		"tool_response":    map[string]string{"status": "async_launched", "agentId": "a1", "taskId": "k1", "backgroundTaskId": "b1"},
	}
	data, _ := json.Marshal(p)
	return append([]byte("branch main\n"), data...)
}

// taken is what state.ParseEvent takes of probes of every hook event the
// hook reports, a wake-up, a subagent's, a delivery and a file it cannot
// read: each event and reply as the journal keeps them.
func taken() string {
	var files [][]byte
	for _, kind := range []string{
		state.KindPrompt, state.KindToolEnd, state.KindToolFail, state.KindTurnEnd, state.KindTurnFailed, state.KindPermission, state.KindElicitation,
		state.KindElicitationResult, state.KindNotification, state.KindSessionStart, state.KindSessionEnd,
	} {
		files = append(files, probe(kind, "Run the tests"))
	}
	wake := probe(state.KindPrompt, "<task-notification><task-id>t1</task-id>")
	sub := bytes.Replace(probe(state.KindToolEnd, ""), []byte(`"agent_id":""`), []byte(`"agent_id":"x"`), 1)
	watch := bytes.Replace(bytes.Replace(probe(state.KindToolEnd, ""), []byte(`"AskUserQuestion"`), []byte(`"Monitor"`), 1), []byte(`"async_launched"`), []byte(`"running"`), 1)
	files = append(files, wake, sub, watch, []byte("delivery stop\n0001-ab.sup\n../x\n"), []byte("not an event"))
	var b strings.Builder
	for _, data := range files {
		e, r := state.ParseEvent("000000001.case.1", data, time.Unix(1, 0).UTC())
		line, _ := json.Marshal(struct {
			E state.Event
			R *state.Reply
		}{e, r})
		b.Write(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// form describes a type as it is kept: each field of a struct with its
// type and its tag, nested types included (a struct once, by name after).
func form(t reflect.Type) string { return formOf(t, map[reflect.Type]bool{}) }

func formOf(t reflect.Type, seen map[reflect.Type]bool) string {
	switch t.Kind() {
	case reflect.Pointer:
		return "*" + formOf(t.Elem(), seen)
	case reflect.Slice:
		return "[]" + formOf(t.Elem(), seen)
	case reflect.Map:
		return "map[" + formOf(t.Key(), seen) + "]" + formOf(t.Elem(), seen)
	case reflect.Struct:
		if seen[t] {
			return t.Name()
		}
		seen[t] = true
		var b strings.Builder
		b.WriteString(t.Name() + "{")
		for i := range t.NumField() {
			f := t.Field(i)
			fmt.Fprintf(&b, "%s %s %q;", f.Name, formOf(f.Type, seen), f.Tag)
		}
		return b.String() + "}"
	}
	return t.String()
}

func fingerprint(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}

// The generation follows the forms: the test fails until a change to one
// of them is pinned, with the generation raised or the renewal explained
// (story 50 of #165).
func TestTheGenerationFollowsTheForms(t *testing.T) {
	var changed []string
	now := forms()
	names := make([]string, 0, len(now))
	for name := range now {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if got := fingerprint(now[name]); got != pinned.forms[name] {
			changed = append(changed, fmt.Sprintf("%s (now %q)", name, got))
		}
	}
	if len(pinned.forms) != len(now) {
		t.Errorf("pinned %d forms, there are %d: pin every form of forms()", len(pinned.forms), len(now))
	}
	if version.Generation != pinned.generation {
		t.Errorf("version.Generation is %d, the forms are pinned at %d: pin them at %d", version.Generation, pinned.generation, version.Generation)
	}
	if len(changed) > 0 {
		t.Errorf("changed since generation %d was pinned: %s; raise version.Generation and pin the forms again, or pin them again and say in the pull request why what agents report is unchanged",
			pinned.generation, strings.Join(changed, ", "))
	}
}

// The fingerprint changes with any change to a form: a byte of the hook's
// script, a field of a kept type, its type or its tag.
func TestAFormChangesItsFingerprint(t *testing.T) {
	type kept struct {
		ID   string `json:"id"`
		Size int    `json:"size,omitempty"`
		Next *kept
	}
	type renamed struct {
		ID   string `json:"id"`
		Size int    `json:"bytes,omitempty"`
		Next *kept
	}
	type retyped struct {
		ID   string `json:"id"`
		Size int64  `json:"size,omitempty"`
		Next *kept
	}
	base := fingerprint(form(reflect.TypeOf(kept{})))
	for name, other := range map[string]string{
		"tag":    form(reflect.TypeOf(renamed{})),
		"type":   form(reflect.TypeOf(retyped{})),
		"nested": form(reflect.TypeOf([]map[string]*kept{})),
	} {
		if fingerprint(other) == base {
			t.Errorf("%s: same fingerprint", name)
		}
	}
	script := state.Settings("NAME", "ID", "NOTIFY")
	if fingerprint(script) == fingerprint(script+" ") {
		t.Error("a byte more of the script keeps its fingerprint")
	}
}

// The probes show every fact the host takes of a payload, so that taking
// one fact more or less changes the fingerprint of the events taken.
func TestTheProbesShowEveryFactTheHostTakes(t *testing.T) {
	got := taken()
	for _, want := range []string{
		`"branch":"main"`, `"session":"s1"`, `"cwd":"/w/app"`, `"turn":"p1"`, `"subagent":true`, `"tool":"AskUserQuestion"`,
		`"server":"srv"`, `"start":"resume"`, `"reason":"other"`, `"notice":"permission_prompt"`, `"question":true`,
		`"tasks":[{"id":"t1","type":"local_agent","status":"running"}]`, `"started":"a1"`, `"started":"~b1"`, `"started":"=k1"`,
		`"wake":true`, `"woke":"t1"`, `"prompt":"Run the tests"`, `"text":"Red?"`, `"text":"Allow?"`, `"messages":["0001-ab.sup"]`,
		`"hook":"stop"`, `"hook":"case"`, `"size":12`, `"text":"Red or blue?"`, `"description":"work","command":"make"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("no %s in what the host takes of the probes", want)
		}
	}
}
