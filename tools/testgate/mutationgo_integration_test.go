//go:build integration

package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

// fakeGo points the run's go (mutationGo) at a fake real go and at a
// directory of the run's machine, as mutationGoEnv makes it, and returns
// that directory. The fake fails a mutant's run (with -failfast), naming a
// test of a package; a run again does what again, a shell command, does.
func fakeGo(t *testing.T, again string) (machine string) {
	t.Helper()
	dir := t.TempDir()
	write(t, dir, "go", "#!/bin/sh\ncase \" $* \" in\n"+
		"*\" -failfast \"*) printf -- '--- FAIL: TestX (0.00s)\\nFAIL\\texample.com/p\\t0.01s\\n'; exit 1 ;;\nesac\n"+
		again+"\n")
	if err := os.Chmod(filepath.Join(dir, "go"), 0o755); err != nil {
		t.Fatal(err)
	}
	machine = filepath.Join(dir, "machine")
	if err := os.MkdirAll(filepath.Join(machine, "records"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, machine, "ok", "")
	t.Setenv(envRealGo, filepath.Join(dir, "go"))
	t.Setenv(envSource, dir)
	t.Setenv(envMachine, machine)
	return machine
}

// A run again that is not over a margin before the mutant's time limit,
// with a process of its own still holding its output (as a test binary
// that a stopped go test leaves), tells nothing: the mutant's run exits as
// the real go did, and the tool's kill stands.
func TestMutationGoLeavesTheKillToTheToolWhenTheRunAgainIsNotOverInTime(t *testing.T) {
	pid := filepath.Join(t.TempDir(), "pid")
	machine := fakeGo(t, "sleep 3600 &\necho $! > "+strconv.Quote(pid)+"\nwait")
	t.Cleanup(func() {
		if b, err := os.ReadFile(pid); err == nil {
			if n, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
				syscall.Kill(n, syscall.SIGKILL)
			}
		}
	})
	// A limit of 3 s (5 s less the tool's 2): the run again has until 1 s.
	if code := mutationGo([]string{"test", "-timeout", "5s", "-failfast", "./..."}); code != 1 {
		t.Errorf("exit %d, want the real go's 1", code)
	}
	if err := machineFailed(machine); err != nil {
		t.Errorf("a run again not over in time told the gate: %v", err)
	}
}

// A run again that fails, on a disk too full even for an empty file (the
// records directory is a file here, so no record can be made), still ends
// the run: the gate is told by a file removed.
func TestMutationGoTellsTheGateWhenNoRecordCanBeWritten(t *testing.T) {
	machine := fakeGo(t, "echo 'no space left on device (simulated)'\nexit 1")
	if err := os.Remove(filepath.Join(machine, "records")); err != nil {
		t.Fatal(err)
	}
	write(t, machine, "records", "")
	if code := mutationGo([]string{"test", "-timeout", "600s", "-failfast", "./..."}); code != 1 {
		t.Errorf("exit %d, want the real go's 1", code)
	}
	err := machineFailed(machine)
	if err == nil || !strings.Contains(err.Error(), "fail without it too") || !strings.Contains(err.Error(), "no record of it could be written") {
		t.Errorf("err %v; want one saying the tests fail without the mutant and no record could be written", err)
	}
}
