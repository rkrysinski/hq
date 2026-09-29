package prefs

import (
	"os"
	"path/filepath"
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

func TestDashboardModesAreKept(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preferences.json")
	if err := Save(path, Prefs{Sort: "repo", View: "all", UpdateChecked: 7}); err != nil {
		t.Fatal(err)
	}
	if p := Load(path); p.Sort != "repo" || p.View != "all" || p.UpdateChecked != 7 {
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
