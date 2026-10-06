//go:build e2e

package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/testutil"
)

// release builds hq at version tag for this platform, as the release workflow
// does, into the stub release server's directory, and makes it the latest.
func release(t *testing.T, relDir, tag string) {
	t.Helper()
	out := filepath.Join(relDir, tag)
	cmd := exec.Command("sh", "../scripts/build-release.sh", tag, out, runtime.GOOS+"/"+runtime.GOARCH)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", tag, err, b)
	}
	if err := os.WriteFile(filepath.Join(relDir, "latest"), []byte(tag+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// run runs a command with the journey's environment and returns its exit
// code and combined output.
func run(t *testing.T, name string, args ...string) (int, string) {
	t.Helper()
	out, err := exec.Command(name, args...).CombinedOutput()
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode(), string(out)
	} else if err != nil {
		t.Fatal(err)
	}
	return 0, string(out)
}

// waitFor polls until ok, for up to 10 s.
func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); !ok(); time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

// Install and update read the public release URLs over HTTPS, with no gh: a
// gh on PATH fails the journey if anything calls it.
func TestInstallThenUpdate(t *testing.T) {
	base, relDir := testutil.ReleaseServer(t)
	t.Setenv("HQ_RELEASES_URL", base)
	sbx, _ := testutil.SbxStub(t)
	// The prerequisites on PATH: the stub sbx, a stand-in for VS Code's
	// code, and the real git, curl and tmux.
	bin := t.TempDir()
	if err := os.Symlink(sbx, filepath.Join(bin, "sbx")); err != nil {
		t.Fatal(err)
	}
	ghCalled := filepath.Join(t.TempDir(), "gh-called")
	for name, script := range map[string]string{
		"code": "#!/bin/sh\n",
		"gh":   "#!/bin/sh\necho \"$@\" >> " + ghCalled + "\nexit 1\n",
	} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		if data, err := os.ReadFile(ghCalled); err == nil {
			t.Errorf("gh was called: %s", data)
		}
	})
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	// Building the releases keeps using the real Go caches under the fake HOME.
	for _, v := range []string{"GOMODCACHE", "GOCACHE", "GOPATH"} {
		out, err := exec.Command("go", "env", v).Output()
		if err != nil {
			t.Fatal(err)
		}
		t.Setenv(v, strings.TrimSpace(string(out)))
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	hq := filepath.Join(home, ".local", "bin", "hq")
	prefs := filepath.Join(home, ".config", "hq", "preferences.json")
	knows := func(tag string) func() bool {
		return func() bool {
			data, _ := os.ReadFile(prefs)
			return strings.Contains(string(data), `"latest_release": "`+tag+`"`)
		}
	}

	// iTerm2 in the user's Applications folder.
	if err := os.MkdirAll(filepath.Join(home, "Applications", "iTerm.app"), 0o755); err != nil {
		t.Fatal(err)
	}
	profiles := filepath.Join(home, "Library/Application Support/iTerm2/DynamicProfiles")

	// First install, as the one-liner does: the release's install.sh piped to
	// sh. Run twice, it leaves one profile and says so once.
	release(t, relDir, "v0.1.0")
	script, err := os.ReadFile(filepath.Join(relDir, "v0.1.0", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	install := func(env ...string) (string, error) {
		cmd := exec.Command("sh")
		cmd.Env = append(os.Environ(), env...)
		cmd.Stdin = strings.NewReader(string(script))
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	for i := range 2 {
		out, err := install()
		if err != nil || !strings.Contains(out, "installed hq v0.1.0 to ~/.local/bin/hq") ||
			!strings.Contains(out, "is not on PATH") {
			t.Fatalf("install: %v\n%s", err, out)
		}
		said := strings.Contains(out, "added the iTerm2 profile hq")
		if want := runtime.GOOS == "darwin" && i == 0; said != want {
			t.Fatalf("install %d: profile said %v, want %v\n%s", i+1, said, want, out)
		}
	}
	if runtime.GOOS == "darwin" {
		if es, err := os.ReadDir(profiles); err != nil || len(es) != 1 || es[0].Name() != "hq.json" {
			t.Fatalf("iTerm2 profiles: %v %v", es, err)
		}
	} else if _, err := os.Stat(filepath.Join(home, "Library")); !os.IsNotExist(err) {
		t.Fatalf("a profile off macOS: %v", err)
	}
	if code, out := run(t, hq, "--version"); code != 0 || out != "hq v0.1.0\n" {
		t.Fatalf("--version: %d %q", code, out)
	}
	// The install's own hq --version started the first daily check.
	waitFor(t, "the first check", knows("v0.1.0"))

	// A newer release: the daily check is due again only after a day, so the
	// cached answer is cleared first. --version answers at once from what the
	// last check found and starts the check in a detached hq; the next
	// --version says what it found.
	release(t, relDir, "v0.2.0")
	os.Remove(prefs)
	if _, out := run(t, hq, "--version"); out != "hq v0.1.0\n" {
		t.Fatalf("--version: %q", out)
	}
	waitFor(t, "the check in the background", knows("v0.2.0"))
	if _, out := run(t, hq, "--version"); out != "hq v0.1.0\nv0.2.0 available - hq update\n" {
		t.Fatalf("--version: %q", out)
	}

	// In a terminal, hq ls ends with the notice on stderr; not with --json,
	// nor when stderr is a pipe.
	socket := testutil.TmuxSocket(t)
	shell := hq + " ls; echo --; " + hq + " ls --json >/dev/null; echo --; " + hq + " ls 2>&1 | cat; echo END; sleep 60"
	if out, err := exec.Command("tmux", "-u", "-L", socket, "-f", "/dev/null", "new-session", "-d", "-x", "100", "-y", "12",
		"-e", "HQ_TMUX_SOCKET="+socket, "sh", "-c", shell).CombinedOutput(); err != nil {
		t.Fatalf("tmux: %v %s", err, out)
	}
	var screen string
	waitFor(t, "the commands in the terminal", func() bool {
		out, _ := exec.Command("tmux", "-u", "-L", socket, "capture-pane", "-p").Output()
		screen = string(out)
		return strings.Contains(screen, "END")
	})
	if want := "hq v0.2.0 is available - run hq update\n--\n--\nEND"; !strings.Contains(screen, want) || strings.Count(screen, "is available") != 1 {
		t.Fatalf("terminal:\n%s", screen)
	}

	if code, out := run(t, hq, "update"); code != 0 || out != "updated hq v0.1.0 -> v0.2.0\n" {
		t.Fatalf("update: %d %q", code, out)
	}
	if _, out := run(t, hq, "--version"); out != "hq v0.2.0\n" {
		t.Fatalf("--version after update: %q", out)
	}
	if _, out := run(t, hq, "update"); out != "hq v0.2.0 is up to date (latest release v0.2.0)\n" {
		t.Fatalf("update again: %q", out)
	}

	// A release whose binary does not match its checksum replaces nothing.
	release(t, relDir, "v0.3.0")
	asset := filepath.Join(relDir, "v0.3.0", "hq-"+runtime.GOOS+"-"+runtime.GOARCH)
	if err := os.WriteFile(asset, []byte("tampered"), 0o755); err != nil {
		t.Fatal(err)
	}
	if code, out := run(t, hq, "update"); code != 1 || !strings.Contains(out, "checksum mismatch") {
		t.Fatalf("tampered update: %d %q", code, out)
	}
	if _, out := run(t, hq, "--version"); !strings.HasPrefix(out, "hq v0.2.0\n") {
		t.Fatalf("--version after a refused update: %q", out)
	}
	if out, err := install(); err == nil || !strings.Contains(out, "checksum mismatch") {
		t.Fatalf("tampered install: %v\n%s", err, out)
	}
	// HQ_VERSION installs that release, not the latest.
	if out, err := install("HQ_VERSION=v0.1.0"); err != nil || !strings.Contains(out, "installed hq v0.1.0") {
		t.Fatalf("install v0.1.0: %v\n%s", err, out)
	}
	// A place with no release says so.
	if out, err := install("HQ_RELEASES_URL=" + base + "/elsewhere"); err == nil || !strings.Contains(out, "could not read the latest release") {
		t.Fatalf("no releases: %v\n%s", err, out)
	}
}
