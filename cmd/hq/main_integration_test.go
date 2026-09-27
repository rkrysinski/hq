//go:build integration

package main

import (
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The built binary keeps the exit-code and stderr contract of spec §4.2.
func TestBinaryReportsErrorsOnStderrWithExitCode(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "hq")
	if out, err := exec.Command("go", "build", "-ldflags", "-X github.com/rkrysinski/hq/internal/version.Version=v9.9.9", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

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
