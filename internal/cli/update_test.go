package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/journal/journaltest"
	"github.com/rkrysinski/hq/internal/prefs"
	"github.com/rkrysinski/hq/internal/proc"
	"github.com/rkrysinski/hq/internal/sbx"
	"github.com/rkrysinski/hq/internal/state"
	"github.com/rkrysinski/hq/internal/tmux"
	"github.com/rkrysinski/hq/internal/update"
	"github.com/rkrysinski/hq/internal/version"
)

func withVersion(t *testing.T, v string) {
	old := version.Version
	version.Version = v
	t.Cleanup(func() { version.Version = old })
}

// publish adds a release of this hq's generation whose binary for the fake
// platform is content.
func (f *fakes) publish(tag, content string) {
	f.publishOf(tag, content, strconv.Itoa(version.Generation))
}

// publishOf adds a release whose binary for the fake platform is content,
// publishing the generation gen, or none when gen is empty (a release from
// before generations).
func (f *fakes) publishOf(tag, content, gen string) {
	files := map[string][]byte{"hq-testos-testarch": []byte(content)}
	if gen != "" {
		files[update.GenerationFile] = []byte(gen + "\n")
	}
	var sums strings.Builder
	for _, name := range []string{"hq-testos-testarch", update.GenerationFile} {
		if data, ok := files[name]; ok {
			sum := sha256.Sum256(data)
			sums.WriteString(hex.EncodeToString(sum[:]) + "  " + name + "\n")
		}
	}
	files[update.SumsFile] = []byte(sums.String())
	f.releases.files[tag] = files
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
	// No option updates anyway (ADR 0020).
	for _, arg := range []string{"now", "--force", "-f", "-y", "--yes"} {
		if code, _, errOut := f.run("update", arg); code != ExitUsage || errOut != "hq: usage: hq update\n" {
			t.Fatalf("%s: exit %d %q", arg, code, errOut)
		}
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
		{[]string{mayUpdateCommand, "v1.0.0", "4"}, false, false},
		{[]string{generationCommand}, false, false},
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

// other is a generation that is not this hq's.
var other = strconv.Itoa(version.Generation + 1)

// inTheWayFakes has hq v0.1.0 installed as "old" and, on this tmux server,
// the agent a working and b ended (its pane dead), in app's running
// sandbox claude-x, and c lost in lib, whose sandbox claude-lib is stopped.
func inTheWayFakes(t *testing.T) *fakes {
	f := newLsFakes()
	installed(t, f, "old")
	f.sbx.sandboxes = []sbx.Sandbox{
		{Name: "claude-x", Status: "running", Workspaces: []string{"/w/app"}},
		{Name: "claude-lib", Status: "stopped", Workspaces: []string{"/w/lib"}},
	}
	f.tmux.windows = []tmux.Window{
		{ID: "@0", Name: "hq", Options: map[string]string{}},
		agentWindow("@1", "a", "/w/app", f.now.Add(-time.Hour), false),
		agentWindow("@2", "b", "/w/app", f.now.Add(-time.Hour), true),
	}
	f.states["id-a"] = journaltest.HookState{State: state.Working, Since: f.now.Add(-time.Minute), Last: "on it"}
	f.records = map[string][]state.Record{"/w/lib": {{ID: "id-c", Name: "c", Sandbox: "claude-lib", Generation: version.Generation}}}
	return f
}

// replaced fails the test unless hq's binary holds want.
func replaced(t *testing.T, f *fakes, want string) {
	t.Helper()
	if data, _ := os.ReadFile(f.exe); string(data) != want {
		t.Fatalf("binary %q, want %q", data, want)
	}
}

// An update within hq's generation goes through with agents running and
// leaves them as they were (ADR 0020).
func TestUpdateWithinTheGenerationLeavesTheAgents(t *testing.T) {
	withVersion(t, "v0.1.0")
	f := inTheWayFakes(t)
	before := f.names()
	f.publish("v0.2.0", "new")
	if code, out, errOut := f.run("update"); code != 0 || out != "updated hq v0.1.0 -> v0.2.0\n" || errOut != "" {
		t.Fatalf("exit %d %q %q", code, out, errOut)
	}
	replaced(t, f, "new")
	if f.names() != before || len(f.sbx.calls) != 0 || len(f.records["/w/lib"]) != 1 {
		t.Fatalf("agents touched: %q, sbx %q, records %v", f.names(), f.sbx.calls, f.records)
	}
}

// An update across generations with no agent anywhere replaces hq.
func TestUpdateAcrossGenerationsWithNoAgent(t *testing.T) {
	for _, gen := range []string{other, ""} {
		withVersion(t, "v0.1.0")
		f := newLsFakes()
		installed(t, f, "old")
		f.publishOf("v0.2.0", "new", gen)
		if code, out, errOut := f.run("update"); code != 0 || out != "updated hq v0.1.0 -> v0.2.0\n" {
			t.Fatalf("generation %q: exit %d %q %q", gen, code, out, errOut)
		}
		replaced(t, f, "new")
	}
}

// An update across generations is refused while any agent hq knows exists,
// running, ended or lost: nothing is replaced, the exit code is 1, and the
// line names the release, the reason, the remedy and each agent with its
// state. A release that publishes no generation counts as another; a dev
// build compares the same way (ADR 0020).
func TestUpdateAcrossGenerationsIsRefusedWhileAgentsExist(t *testing.T) {
	for _, tc := range []struct{ version, gen, theirs string }{
		{"v0.1.0", other, other},
		{"v0.1.0", "", "it names none"},
		{"dev", other, other},
	} {
		withVersion(t, tc.version)
		f := inTheWayFakes(t)
		f.publishOf("v0.2.0", "new", tc.gen)
		code, out, errOut := f.run("update")
		want := "hq: not updated to v0.2.0: it is of another generation (" + tc.theirs + ", this hq " + generation +
			") and agents exist; end them first with hq stop or hq kill NAME: a (working), b (ended), c (lost)\n"
		if code != ExitUsage || out != "" || errOut != want {
			t.Fatalf("%s, generation %q: exit %d %q\n%q\nwant\n%q", tc.version, tc.gen, code, out, errOut, want)
		}
		replaced(t, f, "old")
	}
	// Each kind of agent alone is in the way.
	for name, only := range map[string]func(f *fakes){
		"running": func(f *fakes) { f.tmux.windows = f.tmux.windows[:2]; f.records = nil },
		"ended":   func(f *fakes) { f.tmux.windows = append(f.tmux.windows[:1], f.tmux.windows[2]); f.records = nil },
		"lost":    func(f *fakes) { f.tmux.windows = nil },
	} {
		withVersion(t, "v0.1.0")
		f := inTheWayFakes(t)
		only(f)
		f.publishOf("v0.2.0", "new", other)
		if code, _, errOut := f.run("update"); code != ExitUsage || strings.Count(errOut, " (") != 2 {
			t.Fatalf("%s: exit %d %q", name, code, errOut)
		}
		replaced(t, f, "old")
	}
}

// An agent hq started on another tmux server is in the way too, running or
// lost there: this hq cannot see its window, but its repository holds its
// record (story 22 of #165). It counts once, and an agent this server
// knows is not counted again for a record of another server.
func TestUpdateAcrossGenerationsIsRefusedForAnAgentOfAnotherTmuxServer(t *testing.T) {
	for _, tc := range []struct{ socket, server, want string }{
		{"", "qa", "c (on tmux server qa), d (on tmux server qa)"},
		{"qa", "", "c (on the default tmux server), d (on the default tmux server)"},
	} {
		withVersion(t, "v0.1.0")
		f := inTheWayFakes(t)
		f.env["HQ_TMUX_SOCKET"] = tc.socket
		f.tmux.windows = nil
		f.records = map[string][]state.Record{
			"/w/lib": {
				{ID: "id-d", Name: "d", Sandbox: "claude-lib", Server: tc.server, Generation: version.Generation},
				{ID: "id-c", Name: "c", Sandbox: "claude-lib", Server: tc.server, Generation: version.Generation},
				{ID: "id-x", Name: "../x", Sandbox: "claude-lib", Server: tc.server},
			},
			"/w/app": {{ID: "id-c", Name: "c", Sandbox: "claude-x", Server: tc.server}},
		}
		f.publishOf("v0.2.0", "new", other)
		code, _, errOut := f.run("update")
		if code != ExitUsage || !strings.HasSuffix(errOut, "hq kill NAME: "+tc.want+"\n") {
			t.Fatalf("socket %q: exit %d %q", tc.socket, code, errOut)
		}
		replaced(t, f, "old")
	}
}

// When hq cannot tell whether agents exist, because sbx does not answer, is
// too old or is not there, or tmux fails, an update across generations is
// refused with the reason, exit code 1, and nothing is replaced; an update
// within the generation does not ask (ADR 0020).
func TestUpdateAcrossGenerationsIsRefusedWhenHqCannotTell(t *testing.T) {
	refused := "hq: not updated to v0.2.0: it is of another generation (" + other + ", this hq " + generation + ") and hq cannot tell whether agents exist: "
	for _, tc := range []struct {
		sbxErr, tmuxErr error
		why             string
	}{
		{sbxErr: &proc.Error{Name: "sbx", Msg: "no answer within 5s"}, why: "sbx: no answer within 5s"},
		{sbxErr: &sbx.TooOldError{Version: "0.47.0"}, why: "sbx 0.47.0 is too old, 0.48.0 or newer needed; update Docker Sandboxes"},
		{sbxErr: &proc.Error{Name: "sbx", Msg: "sbx not found", NotFound: true}, why: "sbx not found; install Docker Sandboxes"},
		{tmuxErr: errors.New("tmux: lost server"), why: "tmux: lost server"},
	} {
		withVersion(t, "v0.1.0")
		f := newLsFakes()
		installed(t, f, "old")
		f.sbx.err, f.tmux.windowsErr = tc.sbxErr, tc.tmuxErr
		f.publishOf("v0.2.0", "new", other)
		if code, _, errOut := f.run("update"); code != ExitUsage || errOut != refused+tc.why+"\n" {
			t.Fatalf("exit %d %q, want %q", code, errOut, refused+tc.why)
		}
		replaced(t, f, "old")
		f.publish("v0.2.0", "new")
		if code, _, errOut := f.run("update"); code != 0 {
			t.Fatalf("same generation: exit %d %q", code, errOut)
		}
		replaced(t, f, "new")
	}
}

// A generation its checksums do not vouch for stops the update before
// anything is replaced, refused with exit code 1 as the binary's mismatch
// is (#165, story 27).
func TestUpdateStopsOnAGenerationItCannotRead(t *testing.T) {
	withVersion(t, "v0.1.0")
	f := newLsFakes()
	installed(t, f, "old")
	f.publish("v0.2.0", "new")
	f.releases.files["v0.2.0"][update.GenerationFile] = []byte(other + "\n")
	if code, _, errOut := f.run("update"); code != ExitUsage || errOut != "hq: could not read the generation of hq v0.2.0: checksum mismatch for GENERATION; nothing was replaced\n" {
		t.Fatalf("exit %d %q", code, errOut)
	}
	replaced(t, f, "old")
}

// install.sh asks the installed hq its generation, and then whether it may
// replace it; the answer is hq update's (ADR 0020).
func TestTheInstalledHqAnswersTheInstallScript(t *testing.T) {
	f := inTheWayFakes(t)
	if code, out, _ := f.run(generationCommand); code != 0 || out != generation+"\n" {
		t.Fatalf("generation: exit %d %q", code, out)
	}
	if code, out, errOut := f.run(mayUpdateCommand, "v0.2.0", generation); code != 0 || out != "" || errOut != "" {
		t.Fatalf("same generation: exit %d %q %q", code, out, errOut)
	}
	for _, gen := range []string{other, ""} {
		code, out, errOut := f.run(mayUpdateCommand, "v0.2.0", gen)
		if code != ExitUsage || out != "" || !strings.HasPrefix(errOut, "hq: not updated to v0.2.0: it is of another generation") ||
			!strings.HasSuffix(errOut, ": a (working), b (ended), c (lost)\n") {
			t.Fatalf("generation %q: exit %d %q %q", gen, code, out, errOut)
		}
	}
	f = newLsFakes()
	// Generation 1 is the first there can be.
	for _, gen := range []string{other, "1"} {
		if code, _, errOut := f.run(mayUpdateCommand, "v0.2.0", gen); code != 0 || errOut != "" {
			t.Fatalf("no agent, generation %s: exit %d %q", gen, code, errOut)
		}
	}
	for _, args := range [][]string{{}, {"v0.2.0"}, {"", other}, {"v0.2.0", "x"}, {"v0.2.0", "0"}, {"v0.2.0", other, "--force"}} {
		if code, _, errOut := f.run(append([]string{mayUpdateCommand}, args...)...); code != ExitUsage || errOut != "hq: usage: hq __may-update TAG GENERATION\n" {
			t.Fatalf("%q: exit %d %q", args, code, errOut)
		}
	}
	if code, _, errOut := f.run(generationCommand, "x"); code != ExitUsage || errOut != "hq: usage: hq __generation\n" {
		t.Fatalf("generation x: exit %d %q", code, errOut)
	}
}
