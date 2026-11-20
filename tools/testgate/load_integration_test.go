//go:build integration

package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The bounds of the waits of these tests: for a load run to have started its
// test (a build of the fixture included), and for it and its processes to
// end once asked.
const (
	loadStartBound = 60 * time.Second
	loadEndBound   = 30 * time.Second
)

const loadFixture = "./testdata/loadfixture"

// A loadCommand is testgate load started as this package's test binary, with
// one busy worker (loadHelper), and the files of its stdout and stderr.
type loadCommand struct {
	cmd                  *exec.Cmd
	stdout, stderr       string
	started              string // the file the fixture's TestWaits writes when it runs
	done                 chan struct{}
	waitErr              error
	stdoutText, errLines string
}

// startLoadCommand starts testgate load with args, through sh when viaShell
// (as `sh -c 'load & wait'`, whose end is the end of the process that
// started load).
func startLoadCommand(t *testing.T, viaShell bool, args ...string) *loadCommand {
	t.Helper()
	self := testBinary(t)
	dir := t.TempDir()
	c := &loadCommand{stdout: filepath.Join(dir, "stdout"), stderr: filepath.Join(dir, "stderr"),
		started: filepath.Join(dir, "started"), done: make(chan struct{})}
	if viaShell {
		script := `"$0" "$@" >"$LOAD_OUT" 2>"$LOAD_ERR" & echo $! >"$LOAD_PID"; wait`
		c.cmd = exec.Command("sh", append([]string{"-c", script, self, "load"}, args...)...)
	} else {
		c.cmd = exec.Command(self, append([]string{"load"}, args...)...)
		out, err := os.Create(c.stdout)
		if err != nil {
			t.Fatal(err)
		}
		errf, err := os.Create(c.stderr)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { out.Close(); errf.Close() })
		c.cmd.Stdout, c.cmd.Stderr = out, errf
	}
	// go test's temporary files, and those a killed one leaves, go to a
	// directory of the test's own.
	tmp := t.TempDir()
	c.cmd.Env = append(os.Environ(), "TMPDIR="+tmp, envLoadHelper+"=1", envRealGo+"=", "LOAD_FIXTURE_STARTED="+c.started,
		"LOAD_OUT="+c.stdout, "LOAD_ERR="+c.stderr, "LOAD_PID="+filepath.Join(dir, "pid"))
	if err := c.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { c.waitErr = c.cmd.Wait(); close(c.done) }()
	t.Cleanup(func() {
		c.cmd.Process.Kill()
		c.ended(t)
		for _, g := range c.groups(t) { // the test processes a SIGKILL leaves behind
			syscall.Kill(-g, syscall.SIGKILL)
		}
	})
	return c
}

// waitFor waits, bounded, until cond holds, and fails the test past the
// bound, saying what did not happen.
func waitFor(t *testing.T, bound time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(bound)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("%s: not within %s", what, bound)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func readFile(path string) string {
	b, _ := os.ReadFile(path)
	return string(b)
}

// ended waits, bounded, for the process the test started to end.
func (c *loadCommand) ended(t *testing.T) {
	t.Helper()
	select {
	case <-c.done:
	case <-time.After(loadEndBound):
		t.Fatalf("the process started for testgate load did not end within %s; stderr:\n%s", loadEndBound, readFile(c.stderr))
	}
}

// exit waits, bounded, for the command to end and returns its exit code,
// with what it printed.
func (c *loadCommand) exit(t *testing.T) int {
	t.Helper()
	c.ended(t)
	c.stdoutText, c.errLines = readFile(c.stdout), readFile(c.stderr)
	var exit *exec.ExitError
	if errors.As(c.waitErr, &exit) {
		return exit.ExitCode()
	}
	if c.waitErr != nil {
		t.Fatalf("testgate load: %v", c.waitErr)
	}
	return 0
}

var processGroupLine = regexp.MustCompile(`(?m)^load: process \d+ started, process group (\d+)$`)

// groups are the process groups of the test processes the command said it
// started.
func (c *loadCommand) groups(t *testing.T) []int {
	var gs []int
	for _, m := range processGroupLine.FindAllStringSubmatch(readFile(c.stderr), -1) {
		g, err := strconv.Atoi(m[1])
		if err != nil {
			t.Fatal(err)
		}
		gs = append(gs, g)
	}
	return gs
}

// running are the processes of the process group g that still run (not
// those that ended and wait to be reaped).
func running(t *testing.T, g int) []string {
	t.Helper()
	out, err := exec.Command("ps", "-A", "-o", "pgid=,stat=,pid=,command=").Output()
	if err != nil {
		t.Fatalf("ps: %v", err)
	}
	var left []string
	for _, l := range strings.Split(string(out), "\n") {
		f := strings.Fields(l)
		if len(f) >= 3 && f[0] == strconv.Itoa(g) && !strings.HasPrefix(f[1], "Z") {
			left = append(left, strings.TrimSpace(l))
		}
	}
	return left
}

// testBinary is this package's test binary, which is testgate load with
// $TESTGATE_LOAD_HELPER set (TestMain).
func testBinary(t *testing.T) string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return self
}

// closedPipe is the writing end of a pipe whose reading end is closed
// already: every write to it fails (EPIPE, and SIGPIPE to the writer).
func closedPipe(t *testing.T) *os.File {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
	t.Cleanup(func() { w.Close() })
	return w
}

// groupOf is the process group of the process pid.
func groupOf(t *testing.T, pid string) int {
	t.Helper()
	out, err := exec.Command("ps", "-o", "pgid=", "-p", pid).Output()
	if err != nil {
		t.Fatalf("ps -o pgid= -p %s: %v", pid, err)
	}
	g, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatalf("the process group of %s: %q", pid, out)
	}
	return g
}

// runs reports whether the process pid runs (not ended and waiting to be
// reaped by whoever took it over).
func runs(t *testing.T, pid int) bool {
	t.Helper()
	out, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) { // ps exits 1 when there is no such process
		t.Fatalf("ps: %v", err)
	}
	stat := strings.TrimSpace(string(out))
	return stat != "" && !strings.HasPrefix(stat, "Z")
}

// noTestProcessLeft waits, bounded, until no process of the command's test
// processes' groups runs.
func (c *loadCommand) noTestProcessLeft(t *testing.T) {
	t.Helper()
	gs := c.groups(t)
	if len(gs) == 0 {
		t.Fatalf("testgate load named no process group it started; stderr:\n%s", readFile(c.stderr))
	}
	for _, g := range gs {
		waitFor(t, loadEndBound, "the test processes of group "+strconv.Itoa(g)+" ended", func() bool { return len(running(t, g)) == 0 })
	}
}

// waitForTheTest waits, bounded, until the fixture's TestWaits runs under the
// command, its worker started.
func (c *loadCommand) waitForTheTest(t *testing.T) {
	t.Helper()
	waitFor(t, loadStartBound, "TestWaits started under testgate load", func() bool { return readFile(c.started) != "" })
	if !strings.Contains(readFile(c.stderr), "load: 1 busy workers started\n") {
		t.Fatalf("no busy worker started; stderr:\n%s", readFile(c.stderr))
	}
}

// However testgate load ends, it leaves no busy worker and no test process
// it started, but for SIGKILL, after which its test process may run on to
// its own time limit (#276). A busy worker is a goroutine of the command: the
// command's end is the end of its workers.
func TestALoadRunLeavesNoLoadBehindHoweverItEnds(t *testing.T) {
	for _, c := range []struct {
		name string
		args []string
		end  func(t *testing.T, c *loadCommand)
		code int
		says string // on stderr
	}{
		{name: "the tests ended", args: []string{"-count", "2", "-run", "^TestPasses$", loadFixture}, code: 0},
		{name: "a test failed", args: []string{"-run", "^(TestPasses|TestFails)$", "-count", "1", loadFixture}, code: 1},
		// Whether TestWaits started before the limit or not, the limit stops
		// the tests the same way; the signals' cases show a test that runs
		// stopped.
		{name: "the time limit passed", args: []string{"-timeout", "2s", "-count", "1", "-run", "^TestWaits$", loadFixture},
			code: 2, says: "testgate: load: the time limit 2s passed: the tests were stopped\n"},
		{name: "SIGINT", args: []string{"-count", "1", "-run", "^TestWaits$", loadFixture},
			end:  func(t *testing.T, c *loadCommand) { c.waitForTheTest(t); c.cmd.Process.Signal(syscall.SIGINT) },
			code: 2, says: "testgate: load: interrupted by SIGINT: the tests were stopped\n"},
		{name: "SIGTERM", args: []string{"-count", "1", "-run", "^TestWaits$", loadFixture},
			end:  func(t *testing.T, c *loadCommand) { c.waitForTheTest(t); c.cmd.Process.Signal(syscall.SIGTERM) },
			code: 2, says: "testgate: load: interrupted by SIGTERM: the tests were stopped\n"},
		{name: "SIGHUP", args: []string{"-count", "1", "-run", "^TestWaits$", loadFixture},
			end:  func(t *testing.T, c *loadCommand) { c.waitForTheTest(t); c.cmd.Process.Signal(syscall.SIGHUP) },
			code: 2, says: "testgate: load: interrupted by SIGHUP: the tests were stopped\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			cmd := startLoadCommand(t, false, c.args...)
			if c.end != nil {
				c.end(t, cmd)
			}
			if code := cmd.exit(t); code != c.code || !strings.Contains(cmd.errLines, c.says) {
				t.Fatalf("exit %d, want %d with %q on stderr; stdout:\n%s\nstderr:\n%s", code, c.code, c.says, cmd.stdoutText, cmd.errLines)
			}
			if !strings.Contains(cmd.errLines, "load: 1 busy workers stopped\n") {
				t.Errorf("the busy worker was not stopped; stderr:\n%s", cmd.errLines)
			}
			if c.code == 2 && strings.Contains(cmd.stdoutText, "\nload: ") {
				t.Errorf("a line of counts on exit 2:\n%s", cmd.stdoutText)
			}
			cmd.noTestProcessLeft(t)
		})
	}

	// A write to a closed stdout or stderr does not end the command before
	// it stops its tests: it ends as its tests do. Their reading end is
	// closed before the command starts, so that every write it makes there
	// fails.
	t.Run("stdout and stderr closed", func(t *testing.T) {
		cmd := exec.Command(testBinary(t), "load", "-count", "1", "-run", "^TestPasses$", loadFixture)
		cmd.Env = append(os.Environ(), "TMPDIR="+t.TempDir(), envLoadHelper+"=1", envRealGo+"=")
		cmd.Stdout = closedPipe(t)
		cmd.Stderr = cmd.Stdout
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("got %v, want exit 0 once its test passed", err)
			}
		case <-time.After(loadStartBound):
			cmd.Process.Kill()
			<-done
			t.Fatalf("testgate load did not end within %s", loadStartBound)
		}
	})

	// The process that started the command ends, and with it the reading
	// end of the command's stdout and stderr: the command stops its tests as
	// on SIGTERM, though every write it makes there fails. Its test's process
	// group is found from the process id the test writes, since stderr
	// cannot be read.
	t.Run("the process that started it ended, its stdout and stderr closed", func(t *testing.T) {
		dir := t.TempDir()
		started, pidFile := filepath.Join(dir, "started"), filepath.Join(dir, "pid")
		sh := exec.Command("sh", "-c", `"$0" "$@" & echo $! >"$LOAD_PID"; wait`,
			testBinary(t), "load", "-count", "1", "-run", "^TestWaits$", loadFixture)
		sh.Env = append(os.Environ(), "TMPDIR="+t.TempDir(), envLoadHelper+"=1", envRealGo+"=",
			"LOAD_FIXTURE_STARTED="+started, "LOAD_PID="+pidFile)
		sh.Stdout = closedPipe(t)
		sh.Stderr = sh.Stdout
		if err := sh.Start(); err != nil {
			t.Fatal(err)
		}
		shDone := make(chan struct{})
		go func() { sh.Wait(); close(shDone) }()
		group := 0
		t.Cleanup(func() {
			sh.Process.Kill()
			<-shDone
			if group != 0 {
				syscall.Kill(-group, syscall.SIGKILL)
			}
		})
		waitFor(t, loadStartBound, "TestWaits started under testgate load", func() bool { return readFile(started) != "" })
		group = groupOf(t, strings.TrimSpace(readFile(started)))
		pid, err := strconv.Atoi(strings.TrimSpace(readFile(pidFile)))
		if err != nil {
			t.Fatal(err)
		}
		sh.Process.Kill() // sh passes no signal on
		select {
		case <-shDone:
		case <-time.After(loadEndBound):
			t.Fatalf("sh did not end within %s of SIGKILL", loadEndBound)
		}
		waitFor(t, loadEndBound, "testgate load ended after the process that started it", func() bool { return !runs(t, pid) })
		waitFor(t, loadEndBound, "the test processes of group "+strconv.Itoa(group)+" ended", func() bool { return len(running(t, group)) == 0 })
	})

	t.Run("SIGKILL", func(t *testing.T) {
		cmd := startLoadCommand(t, false, "-count", "1", "-run", "^TestWaits$", loadFixture)
		cmd.waitForTheTest(t)
		cmd.cmd.Process.Kill()
		if code := cmd.exit(t); code != -1 {
			t.Fatalf("exit %d, want killed", code)
		}
		// Its busy worker ended with it; its test process runs on, to be
		// killed by the test's cleanup.
		if gs := cmd.groups(t); len(gs) != 1 || len(running(t, gs[0])) == 0 {
			t.Errorf("groups %v: want the one test process still running", gs)
		}
	})

	t.Run("the process that started it ended", func(t *testing.T) {
		sh := startLoadCommand(t, true, "-count", "1", "-run", "^TestWaits$", loadFixture)
		sh.waitForTheTest(t)
		pid, err := strconv.Atoi(strings.TrimSpace(readFile(filepath.Join(filepath.Dir(sh.stdout), "pid"))))
		if err != nil {
			t.Fatal(err)
		}
		sh.cmd.Process.Kill() // sh passes no signal on
		sh.ended(t)
		waitFor(t, loadEndBound, "testgate load ended after the process that started it", func() bool {
			return !runs(t, pid)
		})
		errs := readFile(sh.stderr)
		if !strings.Contains(errs, "testgate: load: the process that started testgate ended: the tests were stopped\n") ||
			!strings.Contains(errs, "load: 1 busy workers stopped\n") {
			t.Fatalf("stderr:\n%s", errs)
		}
		sh.noTestProcessLeft(t)
	})
}

// A load run's evidence and verdict against the fixture package (#276).
func TestALoadRunsEvidenceAndVerdict(t *testing.T) {
	head, err := headCommit(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name    string
		args    []string
		code    int
		stdout  []string // lines stdout has, in this order
		stderr  string
		noCount bool
	}{
		{name: "every run passed, in each process, with the tags given", code: 0,
			args: []string{"-p", "2", "-tags", "loadtag", "-o", "OUT", "-count", "3", "-run", "^(TestPasses|TestTagged)$", loadFixture},
			stdout: []string{
				"commit " + head,
				"processes 2, each: go test -json -count 3 -run '^(TestPasses|TestTagged)$' -tags loadtag -timeout 11m0s " + loadFixture,
				"time limit 10m0s (the default)",
				"output in OUT",
				"process 1: exit status 0, output OUT/process-1.out, last lines:",
				"process 2: exit status 0, output OUT/process-2.out, last lines:",
				"load: 12 passed, 0 failed, 0 skipped of 12 runs expected on " + head,
			}},
		{name: "a run failed", code: 1, args: []string{"-count", "2", "-run", "^TestFails$", loadFixture},
			stdout: []string{"process 1, TestFails, run 1: failed", "        fixture_test.go:15: fails as asked",
				"process 1, TestFails, run 2: failed", "load: 0 passed, 2 failed, 0 skipped of 2 runs expected on " + head}},
		{name: "a subtest pattern that names no subtest", code: 1, args: []string{"-count", "2", "-run", "^TestPasses$/^none$", loadFixture},
			stdout: []string{"process 1, TestPasses, run 1: skipped: no subtest the pattern names ran",
				"process 1, TestPasses, run 2: skipped: no subtest the pattern names ran",
				"load: 0 passed, 0 failed, 2 skipped of 2 runs expected on " + head}},
		{name: "a subtest pattern whose subtests all skipped", code: 1, args: []string{"-count", "2", "-run", "^TestSub$/^skips$", loadFixture},
			stdout: []string{"process 1, TestSub, run 1: skipped: no subtest the pattern names passed", "    TestSub/skips: skips as asked",
				"process 1, TestSub, run 2: skipped: no subtest the pattern names passed",
				"load: 0 passed, 0 failed, 2 skipped of 2 runs expected on " + head}},
		{name: "a subtest pattern whose subtest passed", code: 0, args: []string{"-count", "2", "-run", "^TestSub$/^passes$|^TestPasses$", loadFixture},
			stdout: []string{"load: 4 passed, 0 failed, 0 skipped of 4 runs expected on " + head}},
		{name: "a run skipped", code: 1, args: []string{"-count", "1", "-run", "^TestSkips$", loadFixture},
			stdout: []string{"process 1, TestSkips, run 1: skipped: skips as asked", "load: 0 passed, 0 failed, 1 skipped of 1 runs expected on " + head}},
		{name: "a process ended without all its runs", code: 1, args: []string{"-count", "3", "-run", "^TestExits$", loadFixture},
			stdout: []string{"process 1: exit status 1, output ", "process 1, TestExits, run 1: never ended",
				"load: 0 passed, 1 failed, 0 skipped of 3 runs expected on " + head}},
		{name: "a pattern that names no test", code: 2, noCount: true, args: []string{"-count", "1", "-run", "^TestNone$", loadFixture},
			stderr: "testgate: load: -run '^TestNone$' names no test of " + loadFixture + "\n"},
		{name: "the tags left out", code: 2, noCount: true, args: []string{"-count", "1", "-run", "^TestTagged$", loadFixture},
			stderr: "names no test of"},
		{name: "a package that does not build", code: 2, noCount: true, args: []string{"-count", "1", "-run", "Test", "./testdata/loadbroken"},
			stderr: "cannot use \"not a number\""},
		{name: "wrong arguments", code: 2, noCount: true, args: []string{"-count", "0", "-run", "Test", loadFixture},
			stderr: "testgate: load: -count must be 1 or more\n" + usage + "\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "out")
			args := make([]string, len(c.args))
			for i, a := range c.args {
				args[i] = strings.ReplaceAll(a, "OUT", out)
			}
			cmd := startLoadCommand(t, false, args...)
			code := cmd.exit(t)
			stdout := strings.ReplaceAll(cmd.stdoutText, out, "OUT")
			if code != c.code || !strings.Contains(cmd.errLines, c.stderr) {
				t.Fatalf("exit %d, want %d with %q on stderr; stdout:\n%s\nstderr:\n%s", code, c.code, c.stderr, stdout, cmd.errLines)
			}
			rest := stdout
			for _, l := range c.stdout {
				i := strings.Index(rest, l)
				if i < 0 {
					t.Fatalf("stdout has no %q after the lines before it:\n%s", l, stdout)
				}
				rest = rest[i+len(l):]
			}
			if c.noCount && stdout != "" {
				t.Errorf("stdout on exit 2:\n%s", stdout)
			}
			if c.code != 2 {
				for _, f := range []string{"process-1.out"} {
					if m := regexp.MustCompile(`output (\S+/` + f + `)`).FindStringSubmatch(cmd.stdoutText); m == nil || readFile(m[1]) == "" {
						t.Errorf("no output in the file of %s:\n%s", f, cmd.stdoutText)
					}
				}
			}
		})
	}
}
