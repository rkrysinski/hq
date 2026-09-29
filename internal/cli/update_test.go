package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/prefs"
	"github.com/rkrysinski/hq/internal/version"
)

func withVersion(t *testing.T, v string) {
	old := version.Version
	version.Version = v
	t.Cleanup(func() { version.Version = old })
}

// publish adds a release whose binary for the fake platform is content.
func (f *fakes) publish(tag, content string) {
	sum := sha256.Sum256([]byte(content))
	f.releases.files[tag] = map[string][]byte{
		"hq-testos-testarch": []byte(content),
		"SHA256SUMS":         []byte(hex.EncodeToString(sum[:]) + "  hq-testos-testarch\n"),
	}
	f.releases.latest = tag
}

func installed(t *testing.T, f *fakes, content string) {
	f.exe = filepath.Join(t.TempDir(), "hq")
	if err := os.WriteFile(f.exe, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateReplacesHqWithTheLatestRelease(t *testing.T) {
	withVersion(t, "v0.1.0")
	f := newFakes()
	installed(t, f, "old")
	f.publish("v0.2.0", "new")
	code, out, errOut := f.run("update")
	if code != 0 || out != "updated hq v0.1.0 -> v0.2.0\n" {
		t.Fatalf("exit %d %q %q", code, out, errOut)
	}
	if data, _ := os.ReadFile(f.exe); string(data) != "new" {
		t.Fatalf("binary %q", data)
	}
	if fi, _ := os.Stat(f.exe); fi.Mode().Perm() != 0o755 {
		t.Fatalf("mode %v", fi.Mode())
	}
	if f.prefs.LatestRelease != "v0.2.0" {
		t.Fatalf("prefs %+v", f.prefs)
	}
}

func TestUpdateWhenCurrent(t *testing.T) {
	withVersion(t, "v0.2.0")
	f := newFakes()
	f.publish("v0.2.0", "same")
	if code, out, _ := f.run("update"); code != 0 || out != "hq v0.2.0 is up to date (latest release v0.2.0)\n" {
		t.Fatalf("exit %d %q", code, out)
	}
}

func TestUpdateChecksumMismatchReplacesNothing(t *testing.T) {
	withVersion(t, "v0.1.0")
	f := newFakes()
	installed(t, f, "old")
	f.publish("v0.2.0", "new")
	f.releases.files["v0.2.0"]["hq-testos-testarch"] = []byte("tampered")
	code, _, errOut := f.run("update")
	if code != ExitUsage || errOut != "hq: checksum mismatch for hq-testos-testarch; nothing was replaced\n" {
		t.Fatalf("exit %d %q", code, errOut)
	}
	if data, _ := os.ReadFile(f.exe); string(data) != "old" {
		t.Fatalf("binary %q", data)
	}
}

func TestUpdateErrors(t *testing.T) {
	withVersion(t, "v0.1.0")
	f := newFakes()
	if code, _, _ := f.run("update", "now"); code != ExitUsage {
		t.Fatalf("args: exit %d", code)
	}
	f.releases.err = errors.New("no release at https://github.com/rkrysinski/hq/releases")
	if code, _, errOut := f.run("update"); code != ExitEnvironment || errOut != "hq: could not read hq's releases: no release at https://github.com/rkrysinski/hq/releases\n" {
		t.Fatalf("lookup fails: exit %d %q", code, errOut)
	}
	// hq in a directory that is gone: nothing to replace.
	f.releases.err = nil
	f.publish("v0.2.0", "new")
	f.exe = "/nonexistent/hq"
	if code, _, errOut := f.run("update"); code != ExitEnvironment || !strings.HasPrefix(errOut, "hq: could not replace /nonexistent/hq") {
		t.Fatalf("replace fails: exit %d %q", code, errOut)
	}
	delete(f.releases.files, "v0.2.0") // listed, but its files are missing
	if code, _, errOut := f.run("update"); code != ExitEnvironment || errOut != "hq: could not download hq v0.2.0: no asset hq-testos-testarch in v0.2.0\n" {
		t.Fatalf("download fails: exit %d %q", code, errOut)
	}
}

// hq --version reads the last check's answer and never waits on GitHub: a
// due check starts in the background, and its answer shows next time.
func TestVersionShowsTheNewerReleaseTheLastCheckFound(t *testing.T) {
	withVersion(t, "v0.1.0")
	f := newFakes()
	f.releases.latest = "v0.2.0"
	if _, out, _ := f.run("--version"); out != "hq v0.1.0\n" || len(f.detached) != 1 || f.releases.lookups != 0 {
		t.Fatalf("%q, %d checks started, %d lookups", out, len(f.detached), f.releases.lookups)
	}
	if f.prefs.UpdateChecked != f.now.Unix() {
		t.Fatalf("the check's moment is not taken: %+v", f.prefs)
	}
	if got := strings.Join(f.detached[0], " "); got != f.exe+" "+updateCheckCommand {
		t.Fatalf("started %q", got)
	}
	// The detached check.
	if code, out, errOut := f.run(updateCheckCommand); code != 0 || out != "" || errOut != "" {
		t.Fatalf("check: exit %d %q %q", code, out, errOut)
	}
	if _, out, errOut := f.run("--version"); out != "hq v0.1.0\nv0.2.0 available - hq update\n" || errOut != "" || len(f.detached) != 1 {
		t.Fatalf("%q %q after %d checks", out, errOut, len(f.detached))
	}
	// A day later the next check is due.
	f.now = f.now.Add(updateCheckEvery)
	f.run("-V")
	if len(f.detached) != 2 {
		t.Fatalf("%d checks after a day", len(f.detached))
	}
}

func TestVersionHintIsQuietWhenCurrentOffline(t *testing.T) {
	withVersion(t, "v0.2.0")
	f := newFakes()
	f.prefs.LatestRelease = "v0.2.0"
	if _, out, _ := f.run("--version"); out != "hq v0.2.0\n" {
		t.Fatalf("%q", out)
	}
	// A failed check is silent and stores nothing but its moment.
	f = newFakes()
	f.releases.err = errors.New("offline")
	f.run("-V")
	if code, out, errOut := f.run(updateCheckCommand); code != 0 || out != "" || errOut != "" || f.prefs.LatestRelease != "" || f.prefs.UpdateChecked == 0 {
		t.Fatalf("exit %d %q %q %+v", code, out, errOut, f.prefs)
	}
	if code, _, _ := f.run(updateCheckCommand, "x"); code != ExitUsage {
		t.Fatalf("args: exit %d", code)
	}
	withVersion(t, "dev")
	f = newFakes()
	f.prefs.LatestRelease = "v9.0.0"
	f.stderrTTY = true
	if _, out, errOut := f.run("--version"); out != "hq dev\n" || errOut != "" || len(f.detached) != 0 {
		t.Fatalf("dev build: %q %q, %d checks", out, errOut, len(f.detached))
	}
	if _, _, errOut := f.run("ls"); errOut != "" || len(f.detached) != 0 {
		t.Fatalf("dev build: %q, %d checks", errOut, len(f.detached))
	}
}

func TestUpdateDuties(t *testing.T) {
	for _, tc := range []struct {
		args          []string
		check, notice bool
	}{
		{nil, false, false}, // the dashboard: its list checks, its header shows the hint
		{[]string{"dash"}, false, false},
		{[]string{"ls"}, true, true},
		{[]string{"ls", "--json"}, true, false},
		{[]string{"read", "a", "--json"}, true, false},
		{[]string{"wait", "a"}, true, true},
		{[]string{"new", "a", "/w/app", "say --json"}, true, true},
		{[]string{"new", "a", "--json"}, true, false},
		{[]string{"send", "a", "hi", "--json"}, true, false},
		{[]string{"go", "a"}, true, true},
		{[]string{"code", "a"}, true, true},
		{[]string{"kill", "a", "-y"}, true, true},
		{[]string{"stop"}, true, true},
		{[]string{"sandbox", "rm", "app"}, true, true},
		{[]string{"help"}, true, true},
		{[]string{"-h"}, true, true},
		{[]string{"--version"}, true, false},
		{[]string{"-V"}, true, false},
		{[]string{"update"}, false, false},
		{[]string{"mcp"}, false, false},
		{[]string{"mcp", "install"}, false, false},
		{[]string{listCommand}, false, false},
		{[]string{newDialogCommand, "/w"}, false, false},
		{[]string{killDialogCommand, "a"}, false, false},
		{[]string{slotCommand}, false, false},
		{[]string{sessionCommand}, false, false},
		{[]string{chordCommand, "next"}, false, false},
		{[]string{itermProfileCommand}, false, false},
		{[]string{updateCheckCommand}, false, false},
		{[]string{"nope"}, false, false},
	} {
		if check, notice := updateDuties(tc.args); check != tc.check || notice != tc.notice {
			t.Errorf("%q: check %v notice %v, want %v %v", tc.args, check, notice, tc.check, tc.notice)
		}
	}
	for _, c := range commands() {
		if _, notice := updateDuties([]string{c.name}); notice != (c.name != "dash" && c.name != "mcp" && c.name != "update") {
			t.Errorf("%s: notice %v", c.name, notice)
		}
	}
}

// Every command a user types ends with the notice on stderr when the last
// check found a newer release, after its output, errors included.
func TestUserCommandsEndWithTheUpdateNotice(t *testing.T) {
	withVersion(t, "v0.1.0")
	f := newFakes()
	f.stderrTTY = true
	f.prefs = prefs.Prefs{UpdateChecked: f.now.Unix(), LatestRelease: "v0.2.0"}
	const notice = "hq v0.2.0 is available - run hq update\n"
	if code, _, errOut := f.run("ls"); code != 0 || errOut != notice {
		t.Fatalf("ls: exit %d %q", code, errOut)
	}
	if code, _, errOut := f.run("read", "nobody"); code != ExitNotFound || !strings.HasPrefix(errOut, "hq: no agent 'nobody'") || !strings.HasSuffix(errOut, "\n"+notice) {
		t.Fatalf("an error, then the notice: exit %d %q", code, errOut)
	}
	if _, out, errOut := f.run("help"); !strings.HasPrefix(out, "hq - one console") || errOut != notice {
		t.Fatalf("help: %q", errOut)
	}
	for _, args := range [][]string{{"ls", "--json"}, {"--version"}, {"nope"}, {itermProfileCommand}} {
		if _, _, errOut := f.run(args...); strings.Contains(errOut, "is available") {
			t.Errorf("%q: %q", args, errOut)
		}
	}
	if len(f.detached) != 0 {
		t.Fatalf("%d checks started within a day of the last", len(f.detached))
	}
	// Not when stderr is not a terminal, nor when this is the latest.
	f.stderrTTY = false
	if _, _, errOut := f.run("ls"); errOut != "" {
		t.Fatalf("stderr not a terminal: %q", errOut)
	}
	f.stderrTTY = true
	f.prefs.LatestRelease = "v0.1.0"
	if _, _, errOut := f.run("ls"); errOut != "" {
		t.Fatalf("up to date: %q", errOut)
	}
}

// A user command starts the due check in the background, once; commands run
// at once then find it taken.
func TestAUserCommandStartsTheDueCheckOnce(t *testing.T) {
	withVersion(t, "v0.1.0")
	f := newFakes()
	f.run("ls")
	f.run("ls", "--json")
	f.run("mcp", "install") // not a command that checks
	if len(f.detached) != 1 || f.releases.lookups != 0 {
		t.Fatalf("%d checks started, %d lookups in the command", len(f.detached), f.releases.lookups)
	}
	// The clock set back a day: the check dated after now is due again.
	f.now = f.now.Add(-updateCheckEvery)
	f.run("ls")
	if len(f.detached) != 2 {
		t.Fatalf("%d checks after the clock went back", len(f.detached))
	}
}

func TestDue(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	for checked, want := range map[int64]bool{0: true, now.Unix(): false, now.Unix() - 3600: false, now.Unix() - 86400: true, now.Unix() + 60: true} {
		if due(checked, now) != want {
			t.Errorf("due(%d) != %v", checked, want)
		}
	}
}
