//go:build integration

package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The built gate refuses a command line outside the usage line before any
// gate runs: timebound with one argument too many would otherwise check the
// tree it is started in and exit 0.
func TestTheBuiltGateRefusesACommandLineTheUsageLineDoesNotName(t *testing.T) {
	gate := filepath.Join(t.TempDir(), "testgate")
	if out, err := exec.Command("go", "build", "-o", gate, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	tree := t.TempDir()
	write(t, tree, "a/a_test.go", "package a\nimport \"testing\"\nfunc TestOne(t *testing.T) {}\n")
	for _, args := range [][]string{nil, {"timebound", tree, "extra"}, {"prepost", "extra"}} {
		cmd := exec.Command(gate, args...)
		cmd.Dir = tree
		cmd.Env = append(os.Environ(), envRealGo+"=") // not the go of a mutation run
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 2 || stdout.Len() != 0 || stderr.String() != usage+"\n" {
			t.Errorf("%q: err %v, stdout %q, stderr %q; want exit 2, no stdout, the usage line on stderr", args, err, stdout.String(), stderr.String())
		}
	}
	// Started in a role of a mutation run, with its arguments missing.
	var stderr bytes.Buffer
	role := exec.Command(gate, roleCoverGo)
	role.Stderr = &stderr
	var exit *exec.ExitError
	if err := role.Run(); !errors.As(err, &exit) || exit.ExitCode() != 2 || !strings.Contains(stderr.String(), "the run's go needs") {
		t.Errorf("the run's go without its arguments: %v, %q; want exit 2 saying what it needs", err, stderr.String())
	}
}
