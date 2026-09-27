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
	if err := os.WriteFile(path, []byte(`{"sort":"age","update_checked":5}`), 0o644); err != nil {
		t.Fatal(err)
	}
	p := Load(path)
	p.UpdateChecked = 100
	p.LatestRelease = "v0.2.0"
	if err := Save(path, p); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), `"sort": "age"`) {
		t.Fatalf("unknown field lost: %s", data)
	}
	if q := Load(path); q.UpdateChecked != 100 || q.LatestRelease != "v0.2.0" {
		t.Fatalf("%+v", q)
	}
}
