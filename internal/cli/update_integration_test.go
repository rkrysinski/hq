//go:build integration

package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/rkrysinski/hq/internal/state"
	"github.com/rkrysinski/hq/internal/testutil"
	"github.com/rkrysinski/hq/internal/update"
	"github.com/rkrysinski/hq/internal/version"
)

// updateHQ is hq v0.1.0, installed as a file that says "old", on a private
// tmux server with the stub sbx, reading its releases from the stub release
// server; publish adds the latest release.
type updateHQ struct {
	*realHQ
	exe, relDir string
}

func newUpdateHQ(t *testing.T) *updateHQ {
	testutil.FakeClaude(t)
	base, relDir := testutil.ReleaseServer(t)
	old := version.Version
	version.Version = "v0.1.0"
	t.Cleanup(func() { version.Version = old })
	h := &updateHQ{realHQ: newRealHQ(t), exe: filepath.Join(t.TempDir(), "hq"), relDir: relDir}
	h.d.releases = update.Releases{Base: base}
	h.d.executable = func() (string, error) { return h.exe, nil }
	if err := os.WriteFile(h.exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	return h
}

// publish makes tag, whose binary for this platform holds content and which
// publishes the generation gen (none when empty), the latest release.
func (h *updateHQ) publish(tag, content, gen string) {
	h.t.Helper()
	files := map[string]string{h.d.asset: content}
	if gen != "" {
		files[update.GenerationFile] = gen + "\n"
	}
	dir := filepath.Join(h.relDir, tag)
	if err := os.RemoveAll(dir); err != nil {
		h.t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		h.t.Fatal(err)
	}
	var sums strings.Builder
	for name, c := range files {
		sum := sha256.Sum256([]byte(c))
		sums.WriteString(hex.EncodeToString(sum[:]) + "  " + name + "\n")
		if err := os.WriteFile(filepath.Join(dir, name), []byte(c), 0o644); err != nil {
			h.t.Fatal(err)
		}
	}
	for name, c := range map[string]string{filepath.Join(tag, update.SumsFile): sums.String(), "latest": tag + "\n"} {
		if err := os.WriteFile(filepath.Join(h.relDir, name), []byte(c), 0o644); err != nil {
			h.t.Fatal(err)
		}
	}
}

func (h *updateHQ) binary() string {
	b, _ := os.ReadFile(h.exe)
	return string(b)
}

// Against the stub release server, in real tmux with the stub sbx: an
// update within the generation goes through with an agent running and
// leaves it as it was; one to another generation, or to a release that
// publishes none, is refused with a running, an ended and a lost agent and
// replaces nothing; with none left it goes through (ADR 0020).
func TestUpdateAcrossGenerationsAgainstTheReleaseServer(t *testing.T) {
	h := newUpdateHQ(t)
	app, lib := testutil.GitRepo(t, "app"), testutil.GitRepo(t, "lib")
	h.cwd = app
	if code, _, errOut := h.run("new", "a", "hello"); code != 0 {
		t.Fatalf("new a: exit %d: %s", code, errOut)
	}
	h.waitReport("a", "done", "Done: hello")
	h.cwd = lib
	if code, _, errOut := h.run("new", "b"); code != 0 {
		t.Fatalf("new b: exit %d: %s", code, errOut)
	}
	h.waitState("b", "done")
	if err := h.d.sbx.Stop("claude-lib"); err != nil {
		t.Fatal(err)
	}
	h.waitState("b", "ended")
	// c was lost with an earlier tmux server: hq's record of it is all
	// that is left, in app.
	if err := state.WriteRecord(app, "00000000000000c0", state.Record{Name: "c", Sandbox: "claude-app", Server: server(h.d), Generation: version.Generation}); err != nil {
		t.Fatal(err)
	}

	h.publish("v0.2.0", "same generation", strconv.Itoa(version.Generation))
	if code, out, errOut := h.run("update"); code != 0 || out != "updated hq v0.1.0 -> v0.2.0\n" {
		t.Fatalf("same generation: exit %d %q %q", code, out, errOut)
	}
	if h.binary() != "same generation" {
		t.Fatalf("binary %q", h.binary())
	}
	if rows := h.ls(); len(rows) != 2 || rows[0].Name != "a" || rows[0].State != "done" || rows[0].Last != "Done: hello" || rows[1].State != "ended" {
		t.Fatalf("after the update: %+v", rows)
	}

	other := strconv.Itoa(version.Generation + 1)
	for _, tc := range []struct{ gen, theirs string }{{other, other}, {"", "it names none"}} {
		h.publish("v0.3.0", "another generation", tc.gen)
		want := "hq: not updated to v0.3.0: it is of another generation (" + tc.theirs + ", this hq " + strconv.Itoa(version.Generation) +
			") and agents exist; end them first with hq stop or hq kill NAME: a (done), b (ended), c (lost)\n"
		if code, out, errOut := h.run("update"); code != ExitUsage || out != "" || errOut != want {
			t.Fatalf("generation %q: exit %d %q\n%q\nwant\n%q", tc.gen, code, out, errOut, want)
		}
		if h.binary() != "same generation" {
			t.Fatalf("generation %q: replaced with %q", tc.gen, h.binary())
		}
	}

	// The remedy the line names clears the way.
	if code, _, errOut := h.run("stop", "-y"); code != 0 {
		t.Fatalf("stop: exit %d %s", code, errOut)
	}
	if code, _, errOut := h.run("update"); code != ExitUsage || !strings.HasSuffix(errOut, "hq kill NAME: c (lost)\n") {
		t.Fatalf("after stop: exit %d %q", code, errOut)
	}
	if code, _, errOut := h.run("kill", "c", "-y"); code != 0 {
		t.Fatalf("kill c: exit %d %s", code, errOut)
	}
	if code, out, errOut := h.run("update"); code != 0 || out != "updated hq v0.1.0 -> v0.3.0\n" || h.binary() != "another generation" {
		t.Fatalf("with no agent: exit %d %q %q, binary %q", code, out, errOut, h.binary())
	}
}
