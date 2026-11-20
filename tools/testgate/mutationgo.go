package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// The go that a mutation run puts in front of the real one for the tool
// (#234). The tool counts every failing run of the unit tests as a kill, also
// one that fails for the machine (a full disk), not for its mutant; it never
// sees why. This go is the gate's own binary, linked as go in a directory of
// the run first in the tool's PATH, and it reads where things are from the
// environment the run gives the tool.
const (
	envRealGo  = "HQ_MUTATION_GO"      // the real go
	envSource  = "HQ_MUTATION_SRC"     // the staged tree, without a mutant
	envMachine = "HQ_MUTATION_MACHINE" // the directory where this go tells the gate the machine failed (tellMachineFailed)
)

// gremlinsTimeoutSlack is what the tool adds to a mutant's time limit in the
// -timeout it gives go test, so that its own limit ends the run first: an
// internal of go-gremlins v0.6.0, as -failfast is (isMutantRun).
const gremlinsTimeoutSlack = 2 * time.Second

// rerunMargin is how long before the end of the mutant's time limit the
// run again without the mutant has to be over, so the tool still gets this
// go's exit code in time; rerunWaitDelay is how long a run again stopped at
// that point may keep its output open (a test binary the stopped go left
// running), within the margin.
const (
	rerunMargin    = 2 * time.Second
	rerunWaitDelay = 500 * time.Millisecond
)

// isMutationGo tells whether a process started as arg0, with realGo the
// value of envRealGo, is the go a mutation run put in front of the real one.
func isMutationGo(arg0, realGo string) bool {
	return filepath.Base(arg0) == "go" && realGo != ""
}

// mutationGo is go for the tool: it runs the real go with args and returns
// its exit code. A mutant's run of the unit tests that fails is then checked:
// what failed runs again in the staged tree, without the mutant, and has to
// be over a margin before the mutant's time limit ends, so the tool's verdict
// on a healthy machine stays what it was. If it fails there too, the mutant's
// tests did not fail for the mutant, and the gate is told by removing
// machine/ok, with a record of it when one can be written
// (tellMachineFailed); the gate then judges no mutant. If it is not over in
// time, nothing is told.
func mutationGo(args []string) int {
	start := time.Now()
	realGo := os.Getenv(envRealGo)
	if dir := os.Getenv(envCover); dir != "" && !isMutantRun(args) {
		if runArgs, profile, ok := toolCoverageRun(args, coverDir(dir), os.Getenv(envCoverGo)); ok {
			return coverageRun(realGo, runArgs, profile, coverDir(dir))
		}
	}
	cmd := exec.Command(realGo, args...)
	cmd.Stdin = os.Stdin
	if !isMutantRun(args) {
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		return exitCode(cmd.Run())
	}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	code := exitCode(cmd.Run())
	if code != 1 {
		return code
	}
	ctx := context.Background()
	if deadline, ok := rerunDeadline(args, start); ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, deadline)
		defer cancel()
	}
	for _, again := range rerunArgs(out.String()) {
		rerun := exec.CommandContext(ctx, realGo, again...)
		rerun.Dir = os.Getenv(envSource)
		rerun.WaitDelay = rerunWaitDelay
		got, err := rerun.CombinedOutput()
		if err == nil {
			continue
		}
		if ctx.Err() != nil {
			break // not over in time, nothing told: the mutant counts as the tool says
		}
		tellMachineFailed(os.Getenv(envMachine), fmt.Sprintf("go %s: %v\n%s", strings.Join(again, " "), err, got))
		break
	}
	return code
}

// toolCoverageRun reads args, the tool's arguments of a go command, as its
// first run of the unit tests, the only one with -coverprofile, and returns
// them with each test binary run through this binary with GOCOVERDIR set to
// d's counters and the run's go, in bin, first in PATH (testExecFlag), and
// the file of its -coverprofile; ok is false for any other command.
func toolCoverageRun(args []string, d coverDir, bin string) (runArgs []string, profile string, ok bool) {
	i := slices.Index(args, "-coverprofile")
	if len(args) == 0 || args[0] != "test" || i < 1 || i+1 >= len(args) {
		return nil, "", false
	}
	self, err := os.Executable()
	if err != nil {
		return nil, "", false
	}
	return slices.Concat(args[:1], []string{"-exec", testExecFlag(self, d.counters(), bin)}, args[1:]), args[i+1], true
}

// coverageRun runs the tool's first run of the unit tests, runArgs, and
// returns its exit code; when it passes, its profile is copied to d
// (coverDir), where the gate reads it once the tool is done.
func coverageRun(realGo string, runArgs []string, profile string, d coverDir) int {
	cmd := exec.Command(realGo, runArgs...)
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if name != envCover && name != envCoverGo {
			env = append(env, kv)
		}
	}
	cmd.Env = env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	code := exitCode(cmd.Run())
	if code == 0 {
		if err := copyEntry(profile, d.profile()); err != nil {
			fmt.Fprintln(os.Stderr, "go:", err)
			return 2
		}
	}
	return code
}

// tellMachineFailed tells the gate, through dir, that a run again without
// the mutant failed: it leaves record in dir/records when it can, and
// removes dir/ok, which needs no free disk, so a disk too full even for an
// empty file still ends the run (machineFailed).
func tellMachineFailed(dir, record string) {
	if f, err := os.CreateTemp(filepath.Join(dir, "records"), "machine-*"); err == nil {
		f.WriteString(record)
		f.Close()
	}
	os.Remove(filepath.Join(dir, "ok"))
}

// exitCode is the exit code of a finished command: -1 for one ended by a
// signal, and 2, which the tool takes for a mutant that does not build, for
// one that could not be started.
func exitCode(err error) int {
	var exit *exec.ExitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &exit):
		return exit.ExitCode()
	}
	fmt.Fprintln(os.Stderr, "go:", err)
	return 2
}

// isMutantRun tells a mutant's run of the unit tests from the tool's other
// calls of go: of go-gremlins' runs of go test, only a mutant's has
// -failfast (its first run, which times the unit tests, has -cover).
func isMutantRun(args []string) bool {
	return len(args) > 0 && args[0] == "test" && slices.Contains(args, "-failfast")
}

// rerunDeadline is when the run again without the mutant has to be over: a
// margin before the mutant's time limit ends, which is the -timeout the tool
// gives go test less its slack, from the start of the mutant's run.
func rerunDeadline(args []string, start time.Time) (time.Time, bool) {
	i := slices.Index(args, "-timeout")
	if i < 0 || i+1 >= len(args) {
		return time.Time{}, false
	}
	d, err := time.ParseDuration(args[i+1])
	if err != nil {
		return time.Time{}, false
	}
	return start.Add(d - gremlinsTimeoutSlack - rerunMargin), true
}

// failedPackage is a line of go test's output for a package that failed:
// FAIL, a tab, the package and either its time or why it failed.
var failedPackage = regexp.MustCompile(`^FAIL\t(\S+)(?:\s+(\[[a-z ]+\]))?`)

// rerunArgs are the go commands that run again, without the mutant, what
// failed in out, the output of a mutant's run of go test: for each package
// that failed, its top-level tests that failed, its build when it did not
// build, or its whole tests when out names none; the whole unit suite when
// out names no package.
func rerunArgs(out string) [][]string {
	var reruns [][]string
	var tests []string
	for _, line := range strings.Split(out, "\n") {
		if name, ok := strings.CutPrefix(line, "--- FAIL: "); ok {
			if f := strings.Fields(name); len(f) > 0 && !slices.Contains(tests, f[0]) {
				tests = append(tests, f[0])
			}
			continue
		}
		m := failedPackage.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		switch {
		case m[2] != "":
			reruns = append(reruns, []string{"test", "-count=1", "-run", "^$", m[1]})
		case len(tests) > 0:
			reruns = append(reruns, []string{"test", "-count=1", "-run", "^(" + strings.Join(tests, "|") + ")$", m[1]})
		default:
			reruns = append(reruns, []string{"test", "-count=1", m[1]})
		}
		tests = nil
	}
	if len(reruns) == 0 {
		return [][]string{{"test", "-count=1", "./..."}}
	}
	return reruns
}
