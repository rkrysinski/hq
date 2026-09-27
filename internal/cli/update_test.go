package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	f.releases.err = errors.New("HTTP 401")
	if code, _, errOut := f.run("update"); code != ExitEnvironment || errOut != "hq: could not read hq's releases: HTTP 401 (see gh auth status)\n" {
		t.Fatalf("gh fails: exit %d %q", code, errOut)
	}
	f.releases.err = sbxNotFound()
	if code, _, errOut := f.run("update"); code != ExitEnvironment || !strings.Contains(errOut, "gh not found") {
		t.Fatalf("gh missing: exit %d %q", code, errOut)
	}
	f.releases.err = nil
	f.releases.latest = "v0.2.0" // listed, but its files are missing
	if code, _, _ := f.run("update"); code != ExitEnvironment {
		t.Fatalf("download fails: exit %d", code)
	}
}

func TestVersionShowsANewerReleaseCheckedOnceADay(t *testing.T) {
	withVersion(t, "v0.1.0")
	f := newFakes()
	f.releases.latest = "v0.2.0"
	if _, out, _ := f.run("--version"); out != "hq v0.1.0\nv0.2.0 available - hq update\n" {
		t.Fatalf("%q", out)
	}
	f.releases.latest = "v0.3.0"
	f.run("--version")
	if f.releases.lookups != 1 {
		t.Fatalf("looked up %d times within a day", f.releases.lookups)
	}
	f.now = f.now.Add(updateCheckEvery)
	if _, out, _ := f.run("--version"); out != "hq v0.1.0\nv0.3.0 available - hq update\n" || f.releases.lookups != 2 {
		t.Fatalf("%q after %d lookups", out, f.releases.lookups)
	}
}

func TestVersionHintIsQuietWhenCurrentOffline(t *testing.T) {
	withVersion(t, "v0.2.0")
	f := newFakes()
	f.releases.latest = "v0.2.0"
	if _, out, _ := f.run("--version"); out != "hq v0.2.0\n" {
		t.Fatalf("%q", out)
	}
	f = newFakes()
	f.releases.err = errors.New("offline")
	if _, out, _ := f.run("-V"); out != "hq v0.2.0\n" || f.prefs.UpdateChecked == 0 {
		t.Fatalf("%q %+v", out, f.prefs)
	}
	withVersion(t, "dev")
	f = newFakes()
	f.releases.latest = "v9.0.0"
	if _, out, _ := f.run("--version"); out != "hq dev\n" || f.releases.lookups != 0 {
		t.Fatalf("dev build: %q", out)
	}
}
