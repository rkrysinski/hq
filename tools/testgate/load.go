package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// loadTimeout is the time limit of a load run when -timeout is not given.
const loadTimeout = 10 * time.Minute

// loadOptions are what the flags of load read (docs/hq/testing.md, *Load
// runs*).
type loadOptions struct {
	procs   int           // -p: the test processes started at once
	tags    string        // -tags: the build tags of go test
	timeout time.Duration // -timeout: the time limit of the whole run
	dir     string        // -o: the directory of the processes' output, "" for a new one
	count   int           // -count: the runs of each test in each process
	run     string        // -run: the tests to run
}

// loadFlags are the flags of load.
func loadFlags() options {
	var o loadOptions
	set := flag.NewFlagSet("load", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	set.IntVar(&o.procs, "p", 1, "")
	set.StringVar(&o.tags, "tags", "", "")
	set.DurationVar(&o.timeout, "timeout", loadTimeout, "")
	set.StringVar(&o.dir, "o", "", "")
	set.IntVar(&o.count, "count", 0, "")
	set.StringVar(&o.run, "run", "", "")
	return options{set: set, required: []string{"count", "run"}, read: func() (any, error) {
		switch {
		case o.count < 1:
			return nil, errors.New("-count must be 1 or more")
		case o.procs < 1:
			return nil, errors.New("-p must be 1 or more")
		case o.timeout <= 0:
			return nil, errors.New("-timeout must be longer than 0s")
		case o.run == "":
			return nil, errors.New("-run must name the tests to run")
		}
		return o, nil
	}}
}

// testTimeoutMargin is how much longer than the run's time limit each go
// test process may run: the run stops the tests at its own limit, and a test
// process left behind by a run killed with SIGKILL ends at its own.
const testTimeoutMargin = time.Minute

// lastLineCount is how many of a process's last lines the evidence shows.
const lastLineCount = 5

// stopGrace is how long a test process stopped with SIGINT has to end, with
// go test removing its temporary files, before its process group is killed.
const stopGrace = 5 * time.Second

// parentPoll is how often a load run looks whether the process that started
// it is still there.
const parentPoll = time.Second

// loadContext is the context main hands runLoad: SIGINT, SIGTERM and SIGHUP
// end it, and so does the end of the process that started testgate, which
// go run does not pass a signal on from when it is killed; the cause says
// which. A write to a closed stdout or stderr fails instead of killing the
// run before it stops its test processes (SIGPIPE). stop releases the
// signals.
func loadContext() (ctx context.Context, stop func()) {
	ctx, cancel := context.WithCancelCause(context.Background())
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	pipes := make(chan os.Signal, 1) // never read: a SIGPIPE that does not fit is dropped
	signal.Notify(pipes, syscall.SIGPIPE)
	parent := os.Getppid()
	go func() {
		tick := time.NewTicker(parentPoll)
		defer tick.Stop()
		for {
			select {
			case s := <-sigs:
				cancel(fmt.Errorf("interrupted by %s", unix.SignalName(s.(syscall.Signal))))
				return
			case <-tick.C:
				if os.Getppid() != parent {
					cancel(errors.New("the process that started testgate ended"))
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	return ctx, func() {
		signal.Stop(sigs)
		signal.Stop(pipes)
		cancel(nil)
	}
}

// runLoad makes a load run in the working directory, with that many busy
// workers (one per core; the tests set 1): it lists the tests before any
// load, starts the busy workers, runs the test processes, stops the workers
// once the last test process ended and what was left of its process group
// was killed, prints the evidence on stdout and its progress on stderr, and
// returns its exit code: 0 when every expected run passed, 1 when not, 2 on
// an error of its own, the end of ctx and the time limit included. The time
// limit counts from the start of the tests; the listing before them has a
// limit of the same length.
func runLoad(ctx context.Context, o loadOptions, pkg string, workers int, stdout, stderr io.Writer) int {
	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "testgate: load: "+format+"\n", a...)
		return 2
	}
	head, err := headCommit(".")
	if err != nil {
		return fail("%v", err)
	}
	names, err := listTests(ctx, o, pkg, stderr)
	if err != nil {
		return fail("%v", err)
	}
	dir := o.dir
	if dir == "" {
		dir, err = os.MkdirTemp("", "testgate-load-")
	} else {
		err = os.MkdirAll(dir, 0o755)
	}
	if err != nil {
		return fail("%v", err)
	}
	r := loadRun{head: head, cores: runtime.NumCPU(), workers: workers, command: append([]string{"go"}, goTestArgs(o, pkg)...),
		procs: o.procs, timeout: o.timeout, dir: dir, subtests: topLevelPattern(o.run) != o.run}

	ctx, cancel := context.WithTimeoutCause(ctx, o.timeout, fmt.Errorf("the time limit %s passed", o.timeout))
	defer cancel()
	stopWorkers := startWorkers(workers)
	fmt.Fprintf(stderr, "load: %d busy workers started\n", workers)
	r.start, r.loadStart = time.Now(), loadAverageText(loadAverage())
	for _, l := range r.headLines() {
		fmt.Fprintln(stdout, l)
	}
	processes := make([]loadProcess, o.procs)
	var wg sync.WaitGroup
	abort := func(err error) int { // a process that could not start: stop those that did
		cancel()
		wg.Wait()
		stopWorkers()
		return fail("%v", err)
	}
	for i := range processes {
		processes[i].file = filepath.Join(dir, fmt.Sprintf("process-%d.out", i+1))
		f, err := os.Create(processes[i].file)
		if err != nil {
			return abort(err)
		}
		cmd := testCommand(ctx, goTestArgs(o, pkg))
		cmd.Stdout, cmd.Stderr = f, f
		if err := cmd.Start(); err != nil {
			f.Close()
			return abort(err)
		}
		fmt.Fprintf(stderr, "load: process %d started, process group %d\n", i+1, cmd.Process.Pid)
		wg.Go(func() {
			cmd.Wait()
			killGroup(cmd)
			f.Close()
			processes[i].status = cmd.ProcessState.String()
			fmt.Fprintf(stderr, "load: process %d ended: %s\n", i+1, processes[i].status)
		})
	}
	wg.Wait()
	stopWorkers()
	fmt.Fprintf(stderr, "load: %d busy workers stopped\n", workers)
	r.end, r.loadEnd = time.Now(), loadAverageText(loadAverage())

	for i := range processes {
		f, err := os.Open(processes[i].file)
		if err != nil {
			return fail("%v", err)
		}
		processes[i].run, err = readTestRun(f)
		f.Close()
		if err != nil {
			return fail("%s: %v", processes[i].file, err)
		}
	}
	stopped := context.Cause(ctx)
	lines, ok := r.endLines(processes, len(names)*o.count*o.procs, stopped != nil)
	for _, l := range lines {
		fmt.Fprintln(stdout, l)
	}
	switch {
	case stopped != nil:
		return fail("%v: the tests were stopped", stopped)
	case ok:
		return 0
	}
	return 1
}

// listTests lists the top-level tests the run runs, before any load and
// within its time limit: it fails when the package does not build or no test
// is named.
func listTests(ctx context.Context, o loadOptions, pkg string, stderr io.Writer) ([]string, error) {
	ctx, cancel := context.WithTimeoutCause(ctx, o.timeout, fmt.Errorf("listing the tests took longer than the time limit %s", o.timeout))
	defer cancel()
	var listed bytes.Buffer
	list := testCommand(ctx, listArgs(o, pkg))
	list.Stdout, list.Stderr = &listed, &listed
	fmt.Fprintf(stderr, "load: %s\n", shellLine(list.Args))
	err := list.Run()
	killGroup(list)
	switch {
	case ctx.Err() != nil:
		return nil, fmt.Errorf("%w: no test ran", context.Cause(ctx))
	case err != nil:
		return nil, fmt.Errorf("%s failed (%v):\n%s", shellLine(list.Args), err, strings.TrimRight(listed.String(), "\n"))
	}
	names := listedTests(listed.String())
	if len(names) == 0 {
		return nil, fmt.Errorf("-run %s names no test of %s", shellLine([]string{o.run}), pkg)
	}
	fmt.Fprintf(stderr, "load: tests: %s\n", strings.Join(names, " "))
	return names, nil
}

// testCommand is a go test of a load run, in a process group of its own:
// the end of ctx sends the group SIGINT, on which go test removes its
// temporary files, and the group's leader is killed when it has not ended
// stopGrace later; killGroup kills the rest once it ended.
func testCommand(ctx context.Context, args []string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGINT) }
	cmd.WaitDelay = stopGrace
	return cmd
}

// killGroup kills what is left of the process group of a command that
// ended: a test binary whose go test ended first, a process a test started.
func killGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}

// startWorkers starts n busy workers, goroutines that keep a core busy each,
// and returns what stops them and returns once they all ended. Being of the
// load run's own process, no worker outlives it, however it ends.
func startWorkers(n int) (stop func()) {
	done := make(chan struct{})
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() {
			for {
				select {
				case <-done:
					return
				default:
				}
			}
		})
	}
	return func() {
		close(done)
		wg.Wait()
	}
}

// loadAverage is the machine's 1-minute load average.
func loadAverage() (float64, error) {
	switch runtime.GOOS {
	case "linux":
		b, err := os.ReadFile("/proc/loadavg")
		if err != nil {
			return 0, err
		}
		return procLoadavg(string(b))
	case "darwin":
		out, err := exec.Command("sysctl", "-n", "vm.loadavg").Output()
		if err != nil {
			return 0, fmt.Errorf("sysctl -n vm.loadavg: %v", err)
		}
		return sysctlLoadavg(string(out))
	}
	return 0, fmt.Errorf("not read on %s", runtime.GOOS)
}

// headCommit is HEAD's full sha of the repository dir is in, followed by
// " with uncommitted changes" when its tree is not clean.
func headCommit(dir string) (string, error) {
	sha, err := gitOutput(dir, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("%s is not in a git repository with a commit", dir)
	}
	status, err := gitOutput(dir, "status", "--porcelain")
	if err != nil {
		return "", err
	}
	return commitText(strings.TrimSpace(sha), status), nil
}

// commitText is how the evidence names the commit sha whose tree has the
// status git status --porcelain gives.
func commitText(sha, status string) string {
	if status != "" {
		return sha + " with uncommitted changes"
	}
	return sha
}

// goTestArgs are the arguments of go test in each process of a load run.
func goTestArgs(o loadOptions, pkg string) []string {
	args := withTags([]string{"test", "-json", "-count", strconv.Itoa(o.count), "-run", o.run}, o.tags)
	return append(args, "-timeout", (o.timeout + testTimeoutMargin).String(), pkg)
}

// listArgs are the arguments of the go test that lists the top-level tests
// a load run runs, before any load: it fails when the package does not build.
func listArgs(o loadOptions, pkg string) []string {
	return append(withTags([]string{"test", "-list", topLevelPattern(o.run)}, o.tags), pkg)
}

// withTags adds the build tags to go test's arguments, when there are any.
func withTags(args []string, tags string) []string {
	if tags == "" {
		return args
	}
	return append(args, "-tags", tags)
}

// topLevelPattern is the part of a -run pattern that names top-level tests:
// what comes before its first slash outside brackets and parentheses, as go
// test splits it (testing's splitRegexp).
func topLevelPattern(run string) string {
	class, parens := false, 0
	for i := 0; i < len(run); i++ {
		switch c := run[i]; {
		case c == '\\':
			i++
		case class:
			class = c != ']'
		case c == '[':
			class = true
		case c == '(':
			parens++
		case c == ')':
			parens--
		case c == '/' && parens == 0:
			return run[:i]
		}
	}
	return run
}

// testName is a name go test -list prints for a test it runs with -run: a
// test, a fuzz test or an example, not a benchmark.
var testName = regexp.MustCompile(`^(Test|Fuzz|Example)\S*$`)

// listedTests are the tests go test -list printed in out.
func listedTests(out string) []string {
	var names []string
	for _, l := range splitLines(out) {
		if testName.MatchString(l) {
			names = append(names, l)
		}
	}
	return names
}

// shellSafe are the words a shell reads as they are.
var shellSafe = regexp.MustCompile(`^[A-Za-z0-9_./:=,+@%-]+$`)

// shellLine is the command line a shell reads as args.
func shellLine(args []string) string {
	words := make([]string, len(args))
	for i, a := range args {
		if shellSafe.MatchString(a) {
			words[i] = a
		} else {
			words[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		}
	}
	return strings.Join(words, " ")
}

// procLoadavg reads the 1-minute load average from Linux's /proc/loadavg.
func procLoadavg(s string) (float64, error) {
	f := strings.Fields(s)
	if len(f) == 0 {
		return 0, errors.New("/proc/loadavg is empty")
	}
	return strconv.ParseFloat(f[0], 64)
}

// sysctlLoadavg reads the 1-minute load average from what macOS's sysctl -n
// vm.loadavg prints: "{ 1.25 1.10 1.05 }".
func sysctlLoadavg(s string) (float64, error) {
	f := strings.Fields(s)
	if len(f) < 3 || f[0] != "{" {
		return 0, fmt.Errorf("vm.loadavg is %q", strings.TrimSpace(s))
	}
	return strconv.ParseFloat(f[1], 64)
}

// loadAverageText is how the evidence gives a load average, or why there is
// none.
func loadAverageText(v float64, err error) string {
	if err != nil {
		return "no load average (" + err.Error() + ")"
	}
	return fmt.Sprintf("load average (1 min) %.2f", v)
}

// runCounts are the runs of a load run by how they ended; a run whose
// process ended in it counts as failed.
type runCounts struct{ passed, failed, skipped int }

// tally counts the runs of the top-level tests of a process's run, and gives
// for each run that did not pass a line saying which, with its reason for a
// skip, and for a failure what go test prints of it without -json. When the
// run's pattern names subtests, a run that passed with no subtest tested
// nothing and counts as skipped: go test runs a test whose subtests the
// pattern misses, and it passes.
func tally(proc int, run *testRun, subtests bool) (runCounts, []string) {
	var c runCounts
	var shown []string
	for _, name := range run.order {
		seen := map[string]int{}
		for _, t := range run.packages[name].runs {
			seen[t.name]++
			which := fmt.Sprintf("process %d, %s, run %d: ", proc, t.name, seen[t.name])
			switch {
			case t.outcome == "pass" && subtests && !t.hasSubtest():
				c.skipped++
				shown = append(shown, which+"skipped: no subtest the pattern names ran")
				continue
			case t.outcome == "pass":
				c.passed++
				continue
			case t.outcome == "skip":
				c.skipped++
				shown = append(shown, which+"skipped: "+t.reason())
				continue
			case t.outcome == "fail":
				shown = append(shown, which+"failed")
			default:
				shown = append(shown, which+"never ended")
			}
			c.failed++
			var b strings.Builder
			t.plain(&b)
			for _, l := range splitLines(b.String()) {
				shown = append(shown, indentStep+l)
			}
		}
	}
	return c, shown
}

// hasSubtest reports whether a subtest of the test ran.
func (t *goTest) hasSubtest() bool {
	return slices.ContainsFunc(t.items, func(it testItem) bool { return it.sub != nil })
}

// A loadRun is what the evidence of a load run says of the run as a whole.
type loadRun struct {
	head               string // HEAD's full sha, with " with uncommitted changes" when the tree is not clean
	cores, workers     int
	command            []string // the go test of each process
	procs              int
	timeout            time.Duration
	dir                string // where the processes' output is
	start, end         time.Time
	loadStart, loadEnd string // the load average at the start and at the end, as loadAverageText gives it
	subtests           bool   // whether the pattern names subtests (tally)
}

// A loadProcess is a test process of a load run once it ended.
type loadProcess struct {
	status string // how it ended, as os.ProcessState says it: "exit status 0"
	file   string // the file of its whole output
	run    *testRun
}

// headLines are the lines of the evidence printed when the tests start.
func (r loadRun) headLines() []string {
	limit := "(-timeout)"
	if r.timeout == loadTimeout {
		limit = "(the default)"
	}
	return []string{
		"commit " + r.head,
		fmt.Sprintf("cores %d, busy workers %d", r.cores, r.workers),
		fmt.Sprintf("processes %d, each: %s", r.procs, shellLine(r.command)),
		fmt.Sprintf("time limit %s %s", r.timeout, limit),
		"output in " + r.dir,
		"started " + r.start.UTC().Format(time.RFC3339) + ", " + r.loadStart,
	}
}

// endLines are the lines of the evidence printed once every test process
// ended: per process how it ended, the file of its output and its last
// lines, then each run that did not pass, and, unless the run was stopped
// (the time limit, an interruption), a last line with the counts. ok is
// whether every one of the expected runs passed, and no other, in processes
// that all exited 0.
func (r loadRun) endLines(procs []loadProcess, expected int, stopped bool) (lines []string, ok bool) {
	lines = []string{"ended " + r.end.UTC().Format(time.RFC3339) + ", " + r.loadEnd}
	var all runCounts
	var shown []string
	exited := true
	for i, p := range procs {
		out := p.run.plainLines()
		if len(out) == 0 {
			lines = append(lines, fmt.Sprintf("process %d: %s, output %s, no output", i+1, p.status, p.file))
		} else {
			lines = append(lines, fmt.Sprintf("process %d: %s, output %s, last lines:", i+1, p.status, p.file))
			for _, l := range out[max(0, len(out)-lastLineCount):] {
				lines = append(lines, indentStep+l)
			}
		}
		c, s := tally(i+1, p.run, r.subtests)
		all.passed, all.failed, all.skipped = all.passed+c.passed, all.failed+c.failed, all.skipped+c.skipped
		shown = append(shown, s...)
		exited = exited && p.status == "exit status 0"
	}
	lines = append(lines, shown...)
	ok = !stopped && exited && all.failed == 0 && all.skipped == 0 && all.passed == expected
	if !stopped {
		lines = append(lines, fmt.Sprintf("load: %d passed, %d failed, %d skipped of %d runs expected on %s",
			all.passed, all.failed, all.skipped, expected, r.head))
	}
	return lines, ok
}
