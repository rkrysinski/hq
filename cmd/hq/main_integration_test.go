//go:build integration

package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/testutil"
)

// The built binary keeps the exit-code and stderr contract of spec §4.2.
func TestBinaryReportsErrorsOnStderrWithExitCode(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "hq")
	if out, err := exec.Command("go", "build", "-ldflags", "-X github.com/rkrysinski/hq/internal/version.Version=v9.9.9", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	// Never the user's preferences, nor GitHub: nothing answers on port 1.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HQ_RELEASES_URL", "http://127.0.0.1:1/releases")
	out, err := exec.Command(bin, "--version").Output()
	if err != nil || string(out) != "hq v9.9.9\n" {
		t.Fatalf("--version: %q %v", out, err)
	}

	cmd := exec.Command(bin, "nope")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	err = cmd.Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 {
		t.Fatalf("want exit 1, got %v", err)
	}
	if stderr.String() != "hq: unknown command 'nope' (see hq help)\n" {
		t.Fatalf("stderr %q", stderr.String())
	}
}

// A command that finds the daily check due starts it in a detached hq and
// returns without waiting; that hq keeps the answer for the next command.
func TestTheUpdateCheckRunsInADetachedHq(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "hq")
	if out, err := exec.Command("go", "build", "-ldflags", "-X github.com/rkrysinski/hq/internal/version.Version=v0.1.0", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	base, dir := testutil.ReleaseServer(t)
	if err := os.WriteFile(filepath.Join(dir, "latest"), []byte("v0.2.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("HQ_RELEASES_URL", base)
	if out, err := exec.Command(bin, "--version").Output(); err != nil || string(out) != "hq v0.1.0\n" {
		t.Fatalf("--version before the check: %q %v", out, err)
	}
	prefs := filepath.Join(cfg, "hq", "preferences.json")
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if data, _ := os.ReadFile(prefs); strings.Contains(string(data), `"latest_release": "v0.2.0"`) {
			break
		}
		if time.Now().After(deadline) {
			data, _ := os.ReadFile(prefs)
			t.Fatalf("the detached check kept nothing: %s", data)
		}
	}
	if out, err := exec.Command(bin, "--version").Output(); err != nil || string(out) != "hq v0.1.0\nv0.2.0 available - hq update\n" {
		t.Fatalf("--version after the check: %q %v", out, err)
	}
}
