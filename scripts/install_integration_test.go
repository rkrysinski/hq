//go:build integration

package scripts_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/rkrysinski/hq/internal/testutil"
	"github.com/rkrysinski/hq/internal/update"
	"github.com/rkrysinski/hq/internal/version"
)

// newHq stands in for an installed hq that knows generations: it prints its
// generation, logs what install.sh asks it to $STUB_DIR/asked and, with
// $STUB_DIR/refusal there, refuses with that line as hq update does.
const newHq = `#!/bin/sh
case $1 in
__generation) echo "$STUB_GEN" ;;
__may-update)
	echo "$*" >> "$STUB_DIR/asked"
	[ ! -e "$STUB_DIR/refusal" ] || { cat "$STUB_DIR/refusal" >&2; exit 1; }
	;;
*) echo "stub hq: unexpected $*" >&2; exit 2 ;;
esac
`

// oldHq stands in for an installed hq from before generations: it knows no
// __generation, and hq ls --json prints $STUB_DIR/ls.json, or fails when
// it is not there.
const oldHq = `#!/bin/sh
case "$*" in
"ls --json")
	echo "ls" >> "$STUB_DIR/asked"
	cat "$STUB_DIR/ls.json" 2>/dev/null || { echo "hq: tmux: lost server" >&2; exit 3; } ;;
*) echo "hq: unknown command '$1' (see hq help)" >&2; exit 1 ;;
esac
`

// installer runs scripts/install.sh against a stub release server whose
// latest release, v0.2.0, publishes the generation gen (none when empty),
// with the prerequisites stubbed on PATH, in a home of its own.
type installer struct {
	t                 *testing.T
	home, stub, bin   string
	relDir            string
	env               []string
	installedHqBinary string
}

func newInstaller(t *testing.T, gen string) *installer {
	base, relDir := testutil.ReleaseServer(t)
	in := &installer{t: t, home: t.TempDir(), stub: t.TempDir(), bin: t.TempDir(), relDir: relDir}
	for name, script := range map[string]string{
		"sbx":  "#!/bin/sh\n",
		"code": "#!/bin/sh\n",
		"tmux": "#!/bin/sh\necho 'tmux 3.4'\n",
	} {
		write(t, filepath.Join(in.bin, name), script, 0o755)
	}
	in.installedHqBinary = filepath.Join(in.home, ".local", "bin", "hq")
	in.env = append(os.Environ(),
		"HOME="+in.home, "HQ_RELEASES_URL="+base, "STUB_DIR="+in.stub,
		"STUB_GEN="+strconv.Itoa(version.Generation),
		"PATH="+in.bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	in.release(gen)
	return in
}

// release publishes v0.2.0 with a binary that says its version, the
// generation gen (none when empty) and SHA256SUMS over them.
func (in *installer) release(gen string) {
	dir := filepath.Join(in.relDir, "v0.2.0")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		in.t.Fatal(err)
	}
	files := map[string]string{
		"hq-" + runtime.GOOS + "-" + runtime.GOARCH: "#!/bin/sh\n[ \"$1\" != --version ] || echo 'hq v0.2.0'\n",
	}
	if gen != "" {
		files[update.GenerationFile] = gen + "\n"
	}
	var sums strings.Builder
	for name, content := range files {
		write(in.t, filepath.Join(dir, name), content, 0o755)
		sum := sha256.Sum256([]byte(content))
		sums.WriteString(hex.EncodeToString(sum[:]) + "  " + name + "\n")
	}
	write(in.t, filepath.Join(dir, update.SumsFile), sums.String(), 0o644)
	write(in.t, filepath.Join(in.relDir, "latest"), "v0.2.0\n", 0o644)
}

// installed puts script in place as the hq installed already.
func (in *installer) installed(script string) {
	if err := os.MkdirAll(filepath.Dir(in.installedHqBinary), 0o755); err != nil {
		in.t.Fatal(err)
	}
	write(in.t, in.installedHqBinary, script, 0o755)
}

// run runs install.sh and returns its exit code and output.
func (in *installer) run() (int, string) {
	in.t.Helper()
	cmd := exec.Command("sh", "install.sh")
	cmd.Env = in.env
	out, err := cmd.CombinedOutput()
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode(), string(out)
	} else if err != nil {
		in.t.Fatal(err)
	}
	return 0, string(out)
}

// asked is what install.sh asked the installed hq, one line per question.
func (in *installer) asked() string {
	b, _ := os.ReadFile(filepath.Join(in.stub, "asked"))
	return string(b)
}

// replaced reports whether install.sh put the release's hq in place.
func (in *installer) replaced() bool {
	b, _ := os.ReadFile(in.installedHqBinary)
	return strings.Contains(string(b), "hq v0.2.0")
}

func write(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

// A first install asks nothing.
func TestInstallWithNoHqAsksNothing(t *testing.T) {
	in := newInstaller(t, "99")
	if code, out := in.run(); code != 0 || !strings.Contains(out, "installed hq v0.2.0") || in.asked() != "" || !in.replaced() {
		t.Fatalf("exit %d, asked %q\n%s", code, in.asked(), out)
	}
}

// An installed hq is asked whether it may be replaced by the release, of
// the generation the release publishes or of none, and its refusal stops
// the install with exit code 1 and its line, replacing nothing (ADR 0020).
func TestInstallAsksTheInstalledHq(t *testing.T) {
	for _, gen := range []string{"99", ""} {
		in := newInstaller(t, gen)
		in.installed(newHq)
		if code, out := in.run(); code != 0 || in.asked() != "__may-update v0.2.0 "+gen+"\n" || !in.replaced() {
			t.Fatalf("generation %q allowed: exit %d, asked %q\n%s", gen, code, in.asked(), out)
		}
		in.installed(newHq)
		line := "not updated to v0.2.0: it is of another generation (99, this hq 3) and agents exist; end them first with hq stop or hq kill NAME: a (working)"
		write(t, filepath.Join(in.stub, "refusal"), "hq: "+line+"\n", 0o644)
		code, out := in.run()
		if code != 1 || out != "hq install: "+line+"\n" || in.replaced() {
			t.Fatalf("generation %q refused: exit %d, replaced %v\n%q", gen, code, in.replaced(), out)
		}
	}
}

// An installed hq from before generations cannot answer: its list of
// agents decides, and any agent in it refuses the install, as does a list
// it cannot give (ADR 0020).
func TestInstallOverAnHqFromBeforeGenerations(t *testing.T) {
	in := newInstaller(t, strconv.Itoa(version.Generation))
	in.installed(oldHq)
	agents := `{
  "agents": [
    {
      "name": "a",
      "repo": "app",
      "state": "working",
      "asks": {
        "questions": [
          {
            "name": "not-an-agent",
            "options": []
          }
        ]
      }
    },
    {
      "name": "b",
      "repo": "lib",
      "state": "ended"
    }
  ],
  "next_since": "2026-11-15T10:00:00Z"
}
`
	write(t, filepath.Join(in.stub, "ls.json"), agents, 0o644)
	code, out := in.run()
	if want := "hq install: not updated to v0.2.0: the installed hq is from before generations and agents exist; end them first with hq stop or hq kill NAME: a, b\n"; code != 1 || out != want || in.replaced() {
		t.Fatalf("agents: exit %d, replaced %v\n%q\nwant\n%q", code, in.replaced(), out, want)
	}
	// The earliest releases printed the rows alone.
	write(t, filepath.Join(in.stub, "ls.json"), "[\n  {\n    \"name\": \"a\",\n    \"repo\": \"app\"\n  }\n]\n", 0o644)
	if code, out := in.run(); code != 1 || !strings.HasSuffix(out, "hq kill NAME: a\n") {
		t.Fatalf("rows alone: exit %d\n%s", code, out)
	}
	os.Remove(filepath.Join(in.stub, "ls.json"))
	if code, out := in.run(); code != 1 || out != "hq install: not updated to v0.2.0: the installed hq, from before generations, could not list its agents: tmux: lost server\n" || in.replaced() {
		t.Fatalf("no list: exit %d\n%q", code, out)
	}
	for _, none := range []string{"{\n  \"agents\": [],\n  \"next_since\": \"2026-11-15T10:00:00Z\"\n}\n", "[]\n"} {
		in.installed(oldHq)
		write(t, filepath.Join(in.stub, "ls.json"), none, 0o644)
		if code, out := in.run(); code != 0 || !in.replaced() {
			t.Fatalf("no agent %q: exit %d\n%s", none, code, out)
		}
	}
}

// A generation its checksums do not vouch for stops the install.
func TestInstallRefusesATamperedGeneration(t *testing.T) {
	in := newInstaller(t, "4")
	write(t, filepath.Join(in.relDir, "v0.2.0", update.GenerationFile), "5\n", 0o644)
	if code, out := in.run(); code != 1 || !strings.Contains(out, "checksum mismatch for GENERATION; nothing was installed") || in.replaced() {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

// A release whose SHA256SUMS lists a GENERATION not attached yet stops the
// install, to try again: it is not a release from before generations
// (#200).
func TestInstallStopsWhileTheGenerationIsOnItsWay(t *testing.T) {
	in := newInstaller(t, "4")
	if err := os.Remove(filepath.Join(in.relDir, "v0.2.0", update.GenerationFile)); err != nil {
		t.Fatal(err)
	}
	if code, out := in.run(); code != 1 || !strings.Contains(out, "could not download GENERATION of v0.2.0 from ") || !strings.Contains(out, "which SHA256SUMS lists; try again in a minute") || in.replaced() {
		t.Fatalf("exit %d\n%s", code, out)
	}
}
