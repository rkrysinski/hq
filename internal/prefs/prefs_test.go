package prefs

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestPath(t *testing.T) {
	env := map[string]string{"HOME": "/home/u"}
	if got := Path(func(k string) string { return env[k] }); got != "/home/u/.config/hq/preferences.json" {
		t.Fatal(got)
	}
	env["XDG_CONFIG_HOME"] = "/cfg"
	if got := Path(func(k string) string { return env[k] }); got != "/cfg/hq/preferences.json" {
		t.Fatal(got)
	}
}

func TestSaveLoadKeepsUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hq", "preferences.json")
	if p := Load(path); p.UpdateChecked != 0 || p.LatestRelease != "" {
		t.Fatalf("missing file: %+v", p)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"group":"repo","update_checked":5}`), 0o644); err != nil {
		t.Fatal(err)
	}
	p := Load(path)
	p.UpdateChecked = 100
	p.LatestRelease = "v0.2.0"
	if err := Save(path, p); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), `"group": "repo"`) {
		t.Fatalf("unknown field lost: %s", data)
	}
	if q := Load(path); q.UpdateChecked != 100 || q.LatestRelease != "v0.2.0" {
		t.Fatalf("%+v", q)
	}
}

func TestDashboardSortIsKept(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preferences.json")
	if err := Save(path, Prefs{Sort: "repo", UpdateChecked: 7}); err != nil {
		t.Fatal(err)
	}
	if p := Load(path); p.Sort != "repo" || p.UpdateChecked != 7 {
		t.Fatalf("%+v", p)
	}
	if err := Save(path, Prefs{}); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); strings.TrimSpace(string(data)) != "{}" {
		t.Fatalf("defaults written: %s", data)
	}
}

func TestWritingWhereNoFileCanBeFails(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	under := filepath.Join(file, "hq", "preferences.json")
	if err := Save(under, Prefs{Sort: "repo"}); err == nil {
		t.Fatal("Save under a file")
	}
	if err := Update(under, func(*Prefs) bool { return true }); err == nil {
		t.Fatal("Update under a file")
	}
	path := filepath.Join(dir, "preferences.json")
	if err := os.Mkdir(path+".lock", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Update(path, func(*Prefs) bool { return true }); err == nil {
		t.Fatal("Update without its lock")
	}
}

// The sandbox settings are read as the user wrote them, and hq writing the
// file for its own reasons keeps them as they are (#58).
func TestSandboxSettingsAreReadAndKept(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preferences.json")
	if err := os.WriteFile(path, []byte(`{"sandbox":{"template":"sbx-image:local","staticMcp":["pencil","docs"],"later":1},"sort":"repo"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	p := Load(path)
	if p.Sandbox.Template != "sbx-image:local" || strings.Join(p.Sandbox.StaticMCP, ",") != "pencil,docs" {
		t.Fatalf("%+v", p.Sandbox)
	}
	p.Sort = "state"
	if err := Save(path, p); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), `"later": 1`) || !strings.Contains(string(data), `"sort": "state"`) {
		t.Fatalf("%s", data)
	}
	if q := Load(path); q.Sandbox.Template != "sbx-image:local" || len(q.Sandbox.StaticMCP) != 2 {
		t.Fatalf("%+v", q)
	}
}

// Setups are read from "sandboxes" beside the default's "sandbox", with
// model and provider, and kept as the user wrote them (#135).
func TestSetupsAreReadAndKept(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preferences.json")
	if err := os.WriteFile(path, []byte(`{"sandbox":{"template":"img:claude"},"sandboxes":{"local":{"template":"img:local","staticMcp":["docs"],"model":"qwen-local","provider":"lmstudio","later":2},"plain":{}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	p := Load(path)
	local, err := p.Setup("local")
	if err != nil || local.Template != "img:local" || strings.Join(local.StaticMCP, ",") != "docs" || local.Model != "qwen-local" || local.Provider != "lmstudio" {
		t.Fatalf("%+v %v", local, err)
	}
	if def, err := p.Setup("default"); err != nil || def.Template != "img:claude" || def.Model != "" {
		t.Fatalf("default %+v %v", def, err)
	}
	if plain, err := p.Setup("plain"); err != nil || !reflect.DeepEqual(plain, Sandbox{}) {
		t.Fatalf("plain %+v %v", plain, err)
	}
	p.Sort = "state"
	if err := Save(path, p); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), `"later": 2`) || !strings.Contains(string(data), `"provider": "lmstudio"`) {
		t.Fatalf("%s", data)
	}
}

func TestSetupNamesListDefaultFirst(t *testing.T) {
	if got := (Prefs{}).SetupNames(); !reflect.DeepEqual(got, []string{"default"}) {
		t.Fatalf("%q", got)
	}
	p := Prefs{Sandboxes: map[string]Sandbox{"zeta": {}, "local": {}}}
	if got := p.SetupNames(); !reflect.DeepEqual(got, []string{"default", "local", "zeta"}) {
		t.Fatalf("%q", got)
	}
}

// An unknown setup is refused naming the setups there are; "default" under
// "sandboxes" is refused with the reason, whichever setup is asked for.
func TestSetupRefusals(t *testing.T) {
	p := Prefs{Sandboxes: map[string]Sandbox{"local": {}}}
	if _, err := p.Setup("locl"); err == nil || err.Error() != "unknown setup 'locl'; setups: default, local" {
		t.Fatalf("%v", err)
	}
	if _, err := (Prefs{}).Setup("local"); err == nil || err.Error() != "unknown setup 'local'; setups: default" {
		t.Fatalf("%v", err)
	}
	p.Sandboxes["default"] = Sandbox{Template: "img:x"}
	for _, name := range []string{"default", "local"} {
		_, err := p.Setup(name)
		if err == nil || err.Error() != `"default" under "sandboxes" is not a setup of its own: the default setup is the "sandbox" object; move its settings there or give it another name` {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

// Setups the file holds in a form hq cannot read are refused naming where
// and what is wrong, whichever setup is asked for, rather than read as no
// setups; a wrong form elsewhere in the file leaves the setups alone.
func TestSetupsInTheWrongFormAreRefused(t *testing.T) {
	for _, c := range []struct{ file, want string }{
		{`{"sandboxes":"local"}`, `"sandboxes" should be an object of setups by name, not a string`},
		{`{"sandboxes":[{"model":"qwen-local"}]}`, `"sandboxes" should be an object of setups by name, not a list`},
		{`{"sandboxes":{"local":"qwen-local"}}`, `"sandboxes.local" should be an object of settings, not a string`},
		{`{"sandboxes":{"local":{"model":5}}}`, `"sandboxes.local.model" should be a string, not a number`},
		{`{"sandbox":{"staticMcp":"docs"}}`, `"sandbox.staticMcp" should be a list of names, not a string`},
		{`{"sandbox":true}`, `"sandbox" should be an object of settings, not true or false`},
		{`{"sandboxes":{"local":{"staticMcp":["docs",5]}}}`, `"sandboxes.local.staticMcp[1]" should be a string, not a number`},
		{`{"sandboxes":{"a":{},"b":{"provider":[]},"c":{"model":1}}}`, `"sandboxes.b.provider" should be a string, not a list`},
	} {
		path := filepath.Join(t.TempDir(), "preferences.json")
		if err := os.WriteFile(path, []byte(c.file), 0o644); err != nil {
			t.Fatal(err)
		}
		p := Load(path)
		for _, name := range []string{"default", "local"} {
			_, err := p.Setup(name)
			if err == nil || err.Error() != c.want || !InFile(err) {
				t.Errorf("%s, %s: %v", c.file, name, err)
			}
		}
	}
	path := filepath.Join(t.TempDir(), "preferences.json")
	if err := os.WriteFile(path, []byte(`{"sort":5,"sandboxes":{"local":{"model":"qwen-local"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if s, err := Load(path).Setup("local"); err != nil || s.Model != "qwen-local" {
		t.Fatalf("%+v %v", s, err)
	}
	if !InFile(ErrDefaultUnderSandboxes) {
		t.Fatal(`"default" under "sandboxes" is the file's fault`)
	}
	if _, err := (Prefs{}).Setup("local"); InFile(err) {
		t.Fatalf("an unknown setup is no fault of the file: %v", err)
	}
}

// The sandboxes hq loaded itself into are kept, and none leaves no key.
func TestMCPLoadedIsKept(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preferences.json")
	if err := Save(path, Prefs{MCPLoaded: []string{"claude-app", "hq-local-app"}}); err != nil {
		t.Fatal(err)
	}
	if p := Load(path); !slices.Equal(p.MCPLoaded, []string{"claude-app", "hq-local-app"}) {
		t.Fatalf("%+v", p)
	}
	if err := Save(path, Prefs{}); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); strings.TrimSpace(string(data)) != "{}" {
		t.Fatalf("none written: %s", data)
	}
}

// The dashboard's view is gone (ADR 0016): an older hq's is dropped the next
// time the file is saved, without a word.
func TestAnOlderHqsViewIsDroppedOnSave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preferences.json")
	if err := os.WriteFile(path, []byte(`{"sort": "repo", "view": "attention", "group": "repo"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	p := Load(path)
	if err := Save(path, p); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), `"view"`) || !strings.Contains(string(data), `"sort": "repo"`) || !strings.Contains(string(data), `"group": "repo"`) {
		t.Fatalf("saved %s", data)
	}
}
