package cli

import (
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/rkrysinski/hq/internal/dialog"
)

func TestNOpensTheNewAgentDialogInAPopup(t *testing.T) {
	f := newFakes()
	f.exe = "/opt/hq"
	src := listSource(f.deps(), "%1")
	if err := src.NewAgent("/w/lib"); err != nil {
		t.Fatal(err)
	}
	if err := src.NewAgent(""); err != nil {
		t.Fatal(err)
	}
	want := []string{"%1 /w/app 76x20 /opt/hq __new-dialog /w/lib", "%1 /w/app 76x20 /opt/hq __new-dialog /w/app"}
	if strings.Join(f.tmux.popups, "\n") != strings.Join(want, "\n") {
		t.Fatalf("popups %q", f.tmux.popups)
	}
}

func TestTheDialogCommandShowsTheDirUnderHomeWithATilde(t *testing.T) {
	f := newFakes()
	f.env["HOME"] = "/w"
	if code, _, errOut := f.run("__new-dialog", "/w/app"); code != 0 {
		t.Fatalf("exit %d %q", code, errOut)
	}
	m, ok := f.dialog.(dialog.NewAgent)
	if !ok || !strings.Contains(ansi.Strip(m.View()), "~/app ") {
		t.Fatalf("dialog %T:\n%s", f.dialog, ansi.Strip(m.View()))
	}
	if code, _, _ := f.run("__new-dialog"); code != ExitUsage {
		t.Fatalf("no dir: exit %d", code)
	}
	if _, out, _ := runCLI("help"); strings.Contains(out, "__new-dialog") {
		t.Fatal("help shows __new-dialog")
	}
}

func TestTheDialogStartsAgentsAsHqNewDoes(t *testing.T) {
	f := newFakes()
	f.env["HOME"] = "/w"
	f.sbx.sandboxes = sandboxesFor("/w/app")
	start := dialogStart(f.deps(), "/w/app")
	for _, dir := range []string{"~/app", "sub", "/w/app/"} {
		name := "a" + string(rune('0'+len(f.tmux.windows)))
		if err := start(name, dir, "say hi"); err != nil {
			t.Fatalf("%s: %v", dir, err)
		}
		w := f.tmux.windows[len(f.tmux.windows)-1]
		if w.Name != name || w.Options["repo"] != "/w/app" || w.Options["new"] == "" || !f.tmux.started[w.ID] {
			t.Fatalf("%s: window %+v", dir, w)
		}
		if argv := f.tmux.argv[w.ID]; argv[len(argv)-1] != "say hi" {
			t.Fatalf("argv %q", argv)
		}
	}
}

func TestTheDialogsErrorsBelongToTheirFields(t *testing.T) {
	f := newFakes()
	f.sbx.sandboxes = sandboxesFor("/w/app")
	start := dialogStart(f.deps(), "/w/app")
	if err := start("a", "/w/app", ""); err != nil {
		t.Fatal(err)
	}
	before := len(f.tmux.windows)
	for _, tc := range []struct {
		name, dir string
		field     int
		msg       string
	}{
		{"a", "/w/app", dialog.Name, "agent 'a' already exists (see hq ls; hq kill a frees the name)"},
		{"bad name", "/w/app", dialog.Name, "invalid name 'bad name': use letters, digits, - and _"},
		{"b", "/w/plain", dialog.Dir, "/w/plain is not in a git repository"},
	} {
		var fe dialog.FieldError
		err := start(tc.name, tc.dir, "")
		if !errors.As(err, &fe) || fe.Field != tc.field || err.Error() != tc.msg {
			t.Errorf("%s in %s: %v", tc.name, tc.dir, err)
		}
	}
	if len(f.tmux.windows) != before {
		t.Fatal("a window was created")
	}
	f.sbx.createErr = errors.New("daemon not running")
	var fe dialog.FieldError
	if err := start("b", "/w/lib", ""); err == nil || errors.As(err, &fe) {
		t.Fatalf("sbx failure: %v", err)
	}
}

func TestExpandDir(t *testing.T) {
	for in, want := range map[string]string{"~": "/h", "~/x": "/h/x", "": "/c", "x/y": "/c/x/y", "/a/b/": "/a/b"} {
		if got := expandDir(in, "/h", "/c"); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
	if tildeDir("/hx", "/h") != "/hx" || tildeDir("/h", "/h") != "~" {
		t.Error("tildeDir")
	}
}
