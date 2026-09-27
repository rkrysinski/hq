//go:build e2e

package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/rkrysinski/hq/internal/testutil"
)

// release builds hq at version tag for this platform, as the release workflow
// does, into the stub gh's release directory, and makes it the latest.
func release(t *testing.T, ghDir, tag string) {
	t.Helper()
	out := filepath.Join(ghDir, tag)
	cmd := exec.Command("sh", "../scripts/build-release.sh", tag, out, runtime.GOOS+"/"+runtime.GOARCH)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", tag, err, b)
	}
	if err := os.WriteFile(filepath.Join(ghDir, "latest"), []byte(tag+"\n"), 0o644); err != nil {
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

func TestInstallThenUpdate(t *testing.T) {
	gh, ghDir := testutil.GhStub(t)
	sbx, _ := testutil.SbxStub(t)
	// The prerequisites on PATH: the stubs, a stand-in for VS Code's code,
	// and the real git and tmux.
	bin := t.TempDir()
	for name, target := range map[string]string{"gh": gh, "sbx": sbx} {
		if err := os.Symlink(target, filepath.Join(bin, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(bin, "code"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
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

	// First install, as the one-liner does: the release's install.sh piped to sh.
	release(t, ghDir, "v0.1.0")
	script, err := os.ReadFile(filepath.Join(ghDir, "v0.1.0", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh")
	cmd.Stdin = strings.NewReader(string(script))
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "installed hq v0.1.0 to ~/.local/bin/hq") ||
		!strings.Contains(string(out), "is not on PATH") {
		t.Fatalf("install: %v\n%s", err, out)
	}
	if runtime.GOOS == "darwin" {
		if _, err := os.Stat(filepath.Join(home, "Library/Application Support/iTerm2/DynamicProfiles/hq.json")); err != nil {
			t.Fatalf("iTerm2 profile: %v", err)
		}
	}
	if code, out := run(t, hq, "--version"); code != 0 || out != "hq v0.1.0\n" {
		t.Fatalf("--version: %d %q", code, out)
	}

	// A newer release: --version says so (its daily check is due again only
	// after a day, so the cached answer is cleared first), hq update moves to it.
	release(t, ghDir, "v0.2.0")
	os.Remove(filepath.Join(home, ".config", "hq", "preferences.json"))
	if _, out := run(t, hq, "--version"); out != "hq v0.1.0\nv0.2.0 available - hq update\n" {
		t.Fatalf("--version: %q", out)
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
	release(t, ghDir, "v0.3.0")
	asset := filepath.Join(ghDir, "v0.3.0", "hq-"+runtime.GOOS+"-"+runtime.GOARCH)
	if err := os.WriteFile(asset, []byte("tampered"), 0o755); err != nil {
		t.Fatal(err)
	}
	if code, out := run(t, hq, "update"); code != 1 || !strings.Contains(out, "checksum mismatch") {
		t.Fatalf("tampered update: %d %q", code, out)
	}
	if _, out := run(t, hq, "--version"); !strings.HasPrefix(out, "hq v0.2.0\n") {
		t.Fatalf("--version after a refused update: %q", out)
	}
	cmd = exec.Command("sh")
	cmd.Stdin = strings.NewReader(string(script))
	if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "checksum mismatch") {
		t.Fatalf("tampered install: %v\n%s", err, out)
	}
}
