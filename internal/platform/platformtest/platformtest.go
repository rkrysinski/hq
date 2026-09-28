// Package platformtest holds the contract every platform adapter keeps, and
// a fake adapter that keeps it (design §3.10).
package platformtest

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/rkrysinski/hq/internal/platform"
)

// Contract checks what hq relies on from any platform: an sbx command,
// paths that survive the trip to sbx and back, a notification sequence
// that is the content of a JSON string with at most one %s, and commands
// for the editor, the browser and raising the window.
func Contract(t *testing.T, p platform.Platform) {
	t.Helper()
	if p.SbxCommand() == "" {
		t.Error("no sbx command")
	}
	seq := p.NotifySequence()
	var s string
	if seq == "" || json.Unmarshal([]byte(`"`+seq+`"`), &s) != nil || strings.Count(seq, "%s") > 1 {
		t.Errorf("notification sequence %q", seq)
	}
	if e := p.Editor("/w/app"); len(e) < 2 || e[len(e)-1] != "/w/app" {
		t.Errorf("editor %q", e)
	}
	if b := p.Browser("https://github.com/o/r/pull/7"); len(b) < 2 || b[len(b)-1] != "https://github.com/o/r/pull/7" {
		t.Errorf("browser %q", b)
	}
	if r := p.Raise("/dev/ttys004"); len(r) == 0 || r[0] == "" {
		t.Errorf("raise %q", r)
	}
	if c, err := p.DesktopConfig("/home/dev"); err != nil || !strings.HasSuffix(c, "/Claude/claude_desktop_config.json") {
		t.Errorf("Claude Desktop's configuration %q, %v", c, err)
	}
	cmd, args, env := p.DesktopServer("/opt/hq", []string{"PATH=/bin"})
	all := strings.Join(append(append([]string{cmd}, args...), env["PATH"]), " ")
	if cmd == "" || len(args) == 0 || args[len(args)-1] != "mcp" || !strings.Contains(all, "/opt/hq") || !strings.Contains(all, "/bin") {
		t.Errorf("Claude Desktop starts %q %q with %q", cmd, args, env)
	}
	for _, path := range []string{"/home/dev/app", "/Users/dev/work/hq", "/w/repo with space"} {
		s, err := p.ToSbx(path)
		if err != nil || s == "" {
			t.Errorf("ToSbx(%q) = %q, %v", path, s, err)
			continue
		}
		back, err := p.FromSbx(s)
		if err != nil || back != path {
			t.Errorf("FromSbx(ToSbx(%q)) = %q, %v", path, back, err)
		}
	}
}

// Fake is a platform whose sbx sees Windows-like paths (F:\home\dev\app for
// /home/dev/app), so a test shows which paths went through the adapter.
type Fake struct{ Sbx string }

func (f Fake) SbxCommand() string {
	if f.Sbx == "" {
		return "sbx"
	}
	return f.Sbx
}

func (Fake) ToSbx(path string) (string, error) {
	return `F:` + strings.ReplaceAll(path, "/", `\`), nil
}

func (Fake) FromSbx(path string) (string, error) {
	return strings.ReplaceAll(strings.TrimPrefix(path, "F:"), `\`, "/"), nil
}

// NotifySequence is plain text, readable in a test's output.
func (Fake) NotifySequence() string { return "[notify %s]" }

// Editor names the fake editor.
func (Fake) Editor(dir string) []string { return []string{"editor", dir} }

// Browser names the fake browser.
func (Fake) Browser(url string) []string { return []string{"browser", url} }

// Raise names the fake raise.
func (Fake) Raise(tty string) []string { return []string{"raise", tty} }

// DesktopConfig is under home, as on macOS.
func (Fake) DesktopConfig(home string) (string, error) {
	return home + "/Claude/claude_desktop_config.json", nil
}

// DesktopServer starts hq directly with the environment given.
func (Fake) DesktopServer(hq string, env []string) (string, []string, map[string]string) {
	m := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		m[k] = v
	}
	return hq, []string{"mcp"}, m
}

var _ platform.Platform = Fake{}
