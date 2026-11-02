package dialog

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// sandboxes answers the dialog's sandbox note: hq-<setup>-<base of dir>,
// claude-<base> for the default; those in exist exist; a dir under /w/plain
// is no repository.
type sandboxes struct {
	exist map[string]bool
	asked []string
}

func (s *sandboxes) sandbox(setup, dir string) (string, bool) {
	s.asked = append(s.asked, setup+"|"+dir)
	if strings.HasPrefix(dir, "/w/plain") {
		return "", false
	}
	base := dir[strings.LastIndex(dir, "/")+1:]
	name := "claude-" + base
	if setup != "default" {
		name = "hq-" + setup + "-" + base
	}
	return name, s.exist[name]
}

// withSetups is the dialog for dir with the setups default and local, its
// first note in.
func withSetups(t *testing.T, dir string, s *starts, sb *sandboxes) NewAgent {
	t.Helper()
	m := NewAgentDialog(dir, s.start).WithSetups([]string{"default", "local"}, sb.sandbox)
	tm, _ := m.Update(result(m.Init()))
	return tm.(NewAgent)
}

func TestWithoutSetupsBesidesTheDefaultTheDialogIsUnchanged(t *testing.T) {
	s := &starts{}
	want := NewAgentDialog("~/w/app", s.start).View()
	for _, setups := range [][]string{nil, {}, {"default"}} {
		m := NewAgentDialog("~/w/app", s.start).WithSetups(setups, (&sandboxes{}).sandbox)
		if got := m.View(); got != want {
			t.Errorf("%q:\n%s\nwant\n%s", setups, ansi.Strip(got), ansi.Strip(want))
		}
		// Started without a setup: hq new's default.
		if m, quit := press(m, "a", "enter"); !quit || !m.Started || strings.Join(s.calls, ",") != "a|~/w/app|" {
			t.Fatalf("%q: closed %v, calls %q", setups, quit, s.calls)
		}
		s.calls = nil
	}
}

func TestTheSetupFieldSitsBetweenDirAndPrompt(t *testing.T) {
	m := withSetups(t, "/w/app", &starts{}, &sandboxes{})
	v := view(m)
	ls := strings.Split(v, "\n")
	_, dirY := find(t, v, "dir     │")
	_, setupY := find(t, v, "setup   │")
	_, promptY := find(t, v, "prompt  │")
	if setupY != dirY+3 || promptY != setupY+3 {
		t.Fatalf("dir %d, setup %d, prompt %d:\n%s", dirY, setupY, promptY, v)
	}
	// The setups in one line, default chosen; the sandbox at the right of
	// the box.
	if !strings.Contains(ls[setupY], "│ ● default  ○ local") || !strings.HasSuffix(strings.TrimRight(ls[setupY], " "), "sandbox claude-app (new) │") {
		t.Fatalf("setup line %q", ls[setupY])
	}
	if !strings.Contains(v, "dir defaults to the cursor row's repo · ↑↓ setup · Tab next · Esc cancel") {
		t.Fatalf("hint:\n%s", v)
	}
	if len(ls) > HeightWithSetups-2 {
		t.Errorf("%d lines, the popup holds %d:\n%s", len(ls), HeightWithSetups-2, v)
	}
	for _, l := range ls {
		if w := ansi.StringWidth(l); w > Width-2 {
			t.Errorf("line %d wide: %q", w, l)
		}
	}
}

func TestUpAndDownPickTheSetupTheAgentStartsOn(t *testing.T) {
	s := &starts{}
	m := withSetups(t, "/w/app", s, &sandboxes{})
	m, _ = press(m, "a", "tab", "tab")
	if m.focus != Setup {
		t.Fatalf("focus %d after name, dir", m.focus)
	}
	m, _ = press(m, "down")
	if !strings.Contains(view(m), "○ default  ● local") || m.focus != Setup {
		t.Fatalf("focus %d:\n%s", m.focus, view(m))
	}
	// Round: down from the last is the first, up from the first the last.
	if m, _ = press(m, "down"); !strings.Contains(view(m), "● default  ○ local") {
		t.Fatalf("down from local:\n%s", view(m))
	}
	if m, _ = press(m, "up"); !strings.Contains(view(m), "○ default  ● local") {
		t.Fatalf("up from default:\n%s", view(m))
	}
	if m, quit := press(m, "enter"); !quit || !m.Started || strings.Join(s.calls, ",") != "a|/w/app||local" {
		t.Fatalf("closed %v, calls %q", quit, s.calls)
	}
	// Left alone, the default.
	s.calls = nil
	if _, quit := press(withSetups(t, "/w/app", s, &sandboxes{}), "b", "enter"); !quit || strings.Join(s.calls, ",") != "b|/w/app||default" {
		t.Fatalf("closed %v, calls %q", quit, s.calls)
	}
}

func TestTabAndLeftRightMoveBetweenTheFieldsAroundTheSetup(t *testing.T) {
	m := withSetups(t, "/w/app", &starts{}, &sandboxes{})
	for _, step := range []struct {
		key  string
		want int
	}{
		{"tab", Dir}, {"tab", Setup}, {"tab", Prompt}, {"shift+tab", Setup}, {"shift+tab", Dir},
		{"down", Setup}, {"right", Prompt}, {"up", Setup}, {"left", Dir},
		{"shift+tab", Name}, {"shift+tab", cancelButton}, {"left", startButton}, {"tab", cancelButton}, {"tab", Name},
	} {
		if m, _ = press(m, step.key); m.focus != step.want {
			t.Fatalf("%s: focus %d, want %d", step.key, m.focus, step.want)
		}
	}
	// ←/→ in a text field move its cursor, as before.
	m, _ = press(m, "tab", "left", "x")
	if m.focus != Dir || m.inputs[Dir].Value() != "/w/apxp" {
		t.Fatalf("focus %d, dir %q", m.focus, m.inputs[Dir].Value())
	}
	// Only the setup field of a dialog with setups takes ↑↓.
	if m, _ = press(m, "down"); m.focus != Setup || !strings.Contains(view(m), "● default") {
		t.Fatalf("focus %d:\n%s", m.focus, view(m))
	}
}

func TestTheSandboxNoteFollowsTheSetupAndTheDir(t *testing.T) {
	sb := &sandboxes{exist: map[string]bool{"claude-app": true, "hq-local-lib": true}}
	m := withSetups(t, "/w/app", &starts{}, sb)
	note := func(m NewAgent) string {
		_, y := find(t, m.View(), "setup   │")
		l := strings.Split(view(m), "\n")[y]
		if i := strings.Index(l, "sandbox "); i >= 0 {
			return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(l[i:]), "│"))
		}
		return ""
	}
	if got := note(m); got != "sandbox claude-app" {
		t.Fatalf("default in app: %q", got)
	}
	m, _ = press(m, "tab", "tab", "down")
	if got := note(m); got != "sandbox hq-local-app (new)" {
		t.Fatalf("local in app: %q", got)
	}
	// The dir typed: /w/lib has the local sandbox, /w/plain is no repository.
	m, _ = press(m, "shift+tab", "backspace", "backspace", "backspace", "l", "i", "b")
	if got := note(m); got != "sandbox hq-local-lib" {
		t.Fatalf("local in lib: %q", got)
	}
	if m, _ = press(m, "tab", "up"); note(m) != "sandbox claude-lib (new)" {
		t.Fatalf("default in lib: %q", note(m))
	}
	m.inputs[Dir].SetValue("/w/plain")
	m.inputs[Dir].CursorEnd()
	if m, _ = press(m, "shift+tab", "x"); note(m) != "" {
		t.Fatalf("no repository: %q", note(m))
	}
	// An answer for a setup or dir no longer shown is dropped.
	tm, _ := m.Update(noteMsg{setup: "local", dir: "/w/plainx", name: "hq-local-x"})
	if note(tm.(NewAgent)) != "" {
		t.Fatalf("stale answer shown: %q", note(tm.(NewAgent)))
	}
}

func TestAClickOnASetupPicksIt(t *testing.T) {
	s := &starts{}
	m := withSetups(t, "/w/app", s, &sandboxes{})
	x, y := find(t, m.View(), "○ local")
	if m, _ = clicked(m, x+3, y); m.focus != Setup || !strings.Contains(view(m), "○ default  ● local") || !strings.Contains(view(m), "hq-local-app (new)") {
		t.Fatalf("focus %d:\n%s", m.focus, view(m))
	}
	x, _ = find(t, m.View(), "○ default")
	if m, _ = clicked(m, x, y); !strings.Contains(view(m), "● default  ○ local") {
		t.Fatalf("default not picked:\n%s", view(m))
	}
	// Between the setups or on the box's border: the focus only.
	for _, at := range [][2]int{{x - 2, y}, {x + 10, y}, {x + 3, y - 1}} {
		if m, _ = clicked(m, at[0], at[1]); !strings.Contains(view(m), "● default  ○ local") {
			t.Fatalf("click at %v picked:\n%s", at, view(m))
		}
	}
}
