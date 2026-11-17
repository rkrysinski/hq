//go:build integration

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/testutil"
	"github.com/rkrysinski/hq/internal/update"
	"github.com/rkrysinski/hq/internal/version"
)

// buildRelease builds hq as release tag of generation gen would be, into a
// directory of its own: the version set as a release build sets it, the
// generation through an overlay of internal/version, so the test needs no
// second checkout.
func buildRelease(t *testing.T, tag string, gen int) string {
	t.Helper()
	dir := t.TempDir()
	src, err := filepath.Abs("../../internal/version/version.go")
	if err != nil {
		t.Fatal(err)
	}
	code := filepath.Join(dir, "version.go")
	if err := os.WriteFile(code, []byte(fmt.Sprintf("package version\n\nvar Version = \"dev\"\n\nconst Generation = %d\n", gen)), 0o644); err != nil {
		t.Fatal(err)
	}
	overlay, _ := json.Marshal(map[string]map[string]string{"Replace": {src: code}})
	if err := os.WriteFile(filepath.Join(dir, "overlay.json"), overlay, 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "hq")
	cmd := exec.Command("go", "build", "-overlay", filepath.Join(dir, "overlay.json"),
		"-ldflags", "-X github.com/rkrysinski/hq/internal/version.Version="+tag, "-o", bin, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", tag, err, out)
	}
	return bin
}

// updateTo puts the hq at bin in place of the one at path, as hq update
// does.
func updateTo(t *testing.T, path, bin string) {
	t.Helper()
	data, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	if err := update.Replace(path, data); err != nil {
		t.Fatal(err)
	}
}

// The dashboard left open across updates: an update of hq's own generation
// leaves its list running; one to another generation has the list run
// from the new hq within 5 s, the docked session, the cursor and the
// layout as they were.
func TestTheListStepsAsideOnlyForAnHqOfAnotherGeneration(t *testing.T) {
	same := buildRelease(t, "v0.98.0", version.Generation)
	other := buildRelease(t, "v0.99.0", version.Generation+1)
	s := supervisorEnv(t)
	repo := testutil.GitRepo(t, "app")
	overrides := append(append([]string{}, s.overrides...), "SHELL=/bin/sh")
	env := append(os.Environ(), overrides...)
	hq := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(s.bin, args...)
		cmd.Dir, cmd.Env = repo, env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("hq %q: %v\n%s", args, err, out)
		}
		return string(out)
	}
	tmux := func(args ...string) string {
		out, _ := exec.Command("tmux", append([]string{"-u", "-L", s.socket}, args...)...).Output()
		return strings.TrimSpace(string(out))
	}
	hq("new", "a", "say hi")

	// The terminal runs hq, which docks a as it opens the dashboard. Its
	// new server has the test's environment, so the command line names only
	// the overrides: tmux refuses a command of more than 16 KiB, which the
	// whole environment can exceed (#226).
	term := testutil.TmuxSocket(t)
	args := append([]string{"-u", "-L", term, "new-session", "-d", "-x", "120", "-y", "30", "-c", repo, "env"}, overrides...)
	if out, err := exec.Command("tmux", append(args, "sh", "-c", `"$@"; exec sleep 600`, "sh", s.bin)...).CombinedOutput(); err != nil {
		t.Fatalf("terminal: %v %s", err, out)
	}
	screen := func() string {
		out, _ := exec.Command("tmux", "-u", "-L", term, "capture-pane", "-p").Output()
		return string(out)
	}
	within := func(what string, d time.Duration, check func(string) bool) {
		t.Helper()
		for end := time.Now().Add(d); time.Now().Before(end); time.Sleep(100 * time.Millisecond) {
			if check(screen()) {
				return
			}
		}
		t.Fatalf("not within %s: %s\n%s", d, what, screen())
	}
	within("a docked in the dashboard of this hq", 10*time.Second, func(sc string) bool {
		return strings.Contains(sc, "▸ a · ") && strings.Contains(sc, "fake claude: ready") && strings.Contains(sc, "· dev")
	})
	// What stays: the list's process, a's pane in the slot, the cursor and
	// the layout, once the list has laid the window out.
	kept := func() string {
		return strings.Join([]string{
			tmux("list-panes", "-t", "hq:", "-F", "#{@hq_role} #{pane_id} #{@hq_list_pid} #{pane_height} #{@hq_id}"),
			tmux("show-options", "-v", "-t", "hq", "@hq_cursor"),
			tmux("display-message", "-p", "-t", "hq:", "#{window_layout}"),
		}, "\n")
	}
	time.Sleep(2 * time.Second)
	before := kept()

	// An update of the same generation: the list goes on as it was.
	updateTo(t, s.bin, same)
	time.Sleep(5 * time.Second)
	if sc := screen(); !strings.Contains(sc, "· dev") || strings.Contains(sc, "v0.98.0") || kept() != before {
		t.Fatalf("the list was restarted for an hq of its own generation:\n%s\n%s\nwas\n%s", sc, kept(), before)
	}

	// An update to another generation: the list runs from the new hq
	// within 5 s, which shows a as outdated, in the same process.
	updateTo(t, s.bin, other)
	within("the list of the new hq", 5*time.Second, func(sc string) bool {
		return strings.Contains(sc, "v0.99.0") && strings.Contains(sc, "outdated")
	})
	for end := time.Now().Add(2 * time.Second); kept() != before && time.Now().Before(end); time.Sleep(100 * time.Millisecond) {
	}
	if after := kept(); after != before {
		t.Fatalf("the dashboard changed:\n%s\nwas\n%s", after, before)
	}
	if sc := screen(); !strings.Contains(sc, "▸ a · ") || !strings.Contains(sc, "fake claude: ready") {
		t.Fatalf("a is no longer docked:\n%s", sc)
	}
}

// hq mcp left running across updates: an update to another generation
// fails every tool call, starting nothing; one of hq's own generation
// leaves the tools as they were.
func TestHqMCPStepsAsideOnlyForAnHqOfAnotherGeneration(t *testing.T) {
	same := buildRelease(t, "v0.98.0", version.Generation)
	other := buildRelease(t, "v0.99.0", version.Generation+1)
	s := newSupervisor(t)
	repo := testutil.GitRepo(t, "app")
	s.ok("list", nil)

	updateTo(t, s.bin, same)
	if out := s.ok("new", map[string]any{"name": "a", "dir": repo, "prompt": "say hi"}); !strings.Contains(out, "started a in app") {
		t.Fatalf("new after an update of the same generation: %q", out)
	}
	s.ok("list", nil)

	updateTo(t, s.bin, other)
	for _, c := range []struct {
		tool string
		args map[string]any
	}{
		{"list", nil},
		{"read", map[string]any{"name": "a"}},
		{"new", map[string]any{"name": "b", "dir": repo, "prompt": "say hi"}},
		{"kill", map[string]any{"name": "a", "confirmed": true}},
	} {
		if out, isErr := s.call(c.tool, c.args); !isErr || out != "hq: hq was updated: restart hq in this client\n" {
			t.Errorf("%s: %v %q", c.tool, isErr, out)
		}
	}
	// b was not started, and a not killed.
	out, _ := exec.Command("tmux", "-u", "-L", s.socket, "list-windows", "-a", "-F", "#{@hq_name}").Output()
	if names := strings.Fields(string(out)); strings.Join(names, " ") != "a" {
		t.Fatalf("agents after the refused calls: %q", names)
	}
}
