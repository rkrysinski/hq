package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
)

// Where a mutation run learns which changed lines a test runs (#275). Go
// test gives each test binary a coverage directory of its own, and a
// program a test builds and starts writes none unless it was built with
// coverage, so a run arranges two things for the tests it starts with
// coverage (the tool's first run of the unit tests, and the higher levels):
//
//   - go test runs each test binary through this binary in the role
//     roleTestExec (go test -exec), which points GOCOVERDIR, inherited by
//     everything the test starts, at a directory of the run's: a program
//     built with coverage writes there when it ends, and so does a test
//     binary started again as another program (testgate load's helper, the
//     test binary linked as go). The test binary's own lines go to its
//     -coverprofile as before, so the tool's verdicts do not change.
//   - it also puts a go of the run's (coverGoScript) first in the test
//     binary's PATH, in front of the GOROOT/bin that go test puts first:
//     this binary in the role roleCoverGo, which adds -cover to every go
//     build and go run in the staged module, so the programs the tests
//     build of it write coverage.
//
// A program ended by a signal writes nothing: its lines count as not run.
// A run that is itself started under a run's coverage (a gate a test of the
// gate starts) hands what its programs wrote on to that run (handOn).
const (
	roleCoverGo  = "-testgate-cover-go"  // args: the real go, the staged tree, the module's path, go's arguments
	roleTestExec = "-testgate-test-exec" // args: the directory for GOCOVERDIR, the run's go's, the test binary, its arguments

	// envCoverDir names, in a test binary's environment, the directory its
	// started programs write coverage to: a run started in that binary hands
	// what its own programs wrote on to it. It starts with HQ_MUTATION_, so
	// mutationEnv keeps it from the commands of a run.
	envCoverDir = "HQ_MUTATION_COVERDIR"
	// envCover names, in the environment of the tool, the directory where
	// the gate's go puts what the tool's first run of the unit tests
	// covered (coverDir); envCoverGo the directory of the run's go
	// (coverGoScript) for the tests of that run.
	envCover   = "HQ_MUTATION_COVER"
	envCoverGo = "HQ_MUTATION_COVER_GO"
)

// runRole runs this binary in the role args name, when it was started in
// one by a mutation run, and reports whether it was: the go of a mutation
// run (mutationGo), the go of a run's tests with coverage (coverGo) or the
// program go test runs each test binary with (testExec). main and the test
// binary's TestMain call it first.
func runRole(args []string) (code int, ok bool) {
	if len(args) > 1 {
		switch args[1] {
		case roleCoverGo:
			return coverGo(args[2:]), true
		case roleTestExec:
			return testExec(args[2:]), true
		}
	}
	if len(args) > 0 && isMutationGo(args[0], os.Getenv(envRealGo)) {
		return mutationGo(args[1:]), true
	}
	return 0, false
}

// coverGo is the go of a run's tests with coverage: it runs the real go
// with args and returns its exit code, and adds -cover and -coverpkg for
// every package of the module to a go build or go run started in the staged
// tree src, so the program it builds writes coverage.
func coverGo(args []string) int {
	if len(args) < 3 {
		fmt.Fprintln(os.Stderr, "go: the run's go needs the real go, the staged tree and the module")
		return 2
	}
	realGo, src, module, goArgs := args[0], args[1], args[2], args[3:]
	if dir, err := os.Getwd(); err == nil && within(src, dir) {
		goArgs = coverArgs(goArgs, module)
	}
	cmd := exec.Command(realGo, goArgs...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return exitCode(cmd.Run())
}

// coverArgs are go's arguments args with the flags of a build with coverage
// of every package of module after build or run, unchanged for any other
// command.
func coverArgs(args []string, module string) []string {
	if len(args) == 0 || args[0] != "build" && args[0] != "run" {
		return args
	}
	return slices.Concat(args[:1], []string{"-cover", "-covermode=set", "-coverpkg=" + module + "/..."}, args[1:])
}

// within tells whether dir is top or a directory below it, symlinks
// resolved.
func within(top, dir string) bool {
	realTop, err1 := filepath.EvalSymlinks(top)
	realDir, err2 := filepath.EvalSymlinks(dir)
	if err1 != nil || err2 != nil {
		return false
	}
	rel, err := filepath.Rel(realTop, realDir)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// testExec is the program go test runs each test binary with (go test
// -exec): it runs the binary, args[2], with its arguments, GOCOVERDIR set to
// args[0] and the directory of the run's go, args[1], first in PATH, passes
// on the signals that would end it, and returns its exit code.
func testExec(args []string) int {
	if len(args) < 3 {
		fmt.Fprintln(os.Stderr, "testgate: the test exec needs a directory, the run's go and a test binary")
		return 2
	}
	cmd := exec.Command(args[2], args[3:]...)
	cmd.Env = append(withPath(os.Environ(), args[1]), "GOCOVERDIR="+args[0], envCoverDir+"="+args[0])
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	signals := make(chan os.Signal, 4)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM, syscall.SIGQUIT, syscall.SIGHUP)
	defer signal.Stop(signals)
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "testgate:", err)
		return 2
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	for {
		select {
		case s := <-signals:
			cmd.Process.Signal(s)
		case err := <-done:
			return exitCode(err)
		}
	}
}

// shellQuote quotes s for sh, and for go test's -exec, which splits its
// value as sh would a line of words in quotes.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// coverGoScript is the go of a run's tests with coverage: a script that
// starts self in the role roleCoverGo with the real go, the staged tree src
// and the module's path. It holds them itself, so it reaches the same real
// go whatever the environment of the command that starts it, also when a
// run a test starts puts its own go in front of it.
func coverGoScript(self, realGo, src, module string) string {
	return "#!/bin/sh\nexec " + strings.Join([]string{shellQuote(self), roleCoverGo,
		shellQuote(realGo), shellQuote(src), shellQuote(module)}, " ") + ` "$@"` + "\n"
}

// testExecFlag is go test's -exec value that runs each test binary through
// self in the role roleTestExec, with GOCOVERDIR set to dir and the run's
// go, in bin, first in PATH.
func testExecFlag(self, dir, bin string) string {
	return strings.Join([]string{shellQuote(self), roleTestExec, shellQuote(dir), shellQuote(bin)}, " ")
}

// modulePath reads the module's path from the go.mod of src.
func modulePath(src string) (string, error) {
	data, err := os.ReadFile(filepath.Join(src, "go.mod"))
	if err != nil {
		return "", err
	}
	return moduleOf(string(data))
}

// moduleOf is the module's path that the go.mod gomod names.
func moduleOf(gomod string) (string, error) {
	for _, l := range strings.Split(gomod, "\n") {
		l, _, _ = strings.Cut(l, "//")
		if rest, ok := strings.CutPrefix(strings.TrimSpace(l), "module"); ok && rest != "" && (rest[0] == ' ' || rest[0] == '\t') {
			return strings.Trim(strings.TrimSpace(rest), `"`), nil
		}
	}
	return "", errors.New("go.mod names no module")
}

// coverSetup is what a run's tests with coverage need, made once per run in
// its temporary directory: the run's go, in bin, and the module's path.
type coverSetup struct {
	self   string // this binary
	bin    string // the directory of the run's go (coverGoScript)
	module string
}

// newCoverSetup writes the run's go into dir/cover-go for the staged tree
// src.
func newCoverSetup(src, dir string) (coverSetup, error) {
	self, err := os.Executable()
	if err != nil {
		return coverSetup{}, err
	}
	realGo, err := exec.LookPath("go")
	if err != nil {
		return coverSetup{}, err
	}
	module, err := modulePath(src)
	if err != nil {
		return coverSetup{}, err
	}
	bin := filepath.Join(dir, "cover-go")
	if err := os.Mkdir(bin, 0o755); err != nil {
		return coverSetup{}, err
	}
	if err := os.WriteFile(filepath.Join(bin, "go"), []byte(coverGoScript(self, realGo, src, module)), 0o755); err != nil {
		return coverSetup{}, err
	}
	return coverSetup{self, bin, module}, nil
}

// withPath is env with dir first in its PATH.
func withPath(env []string, dir string) []string {
	path := dir
	var out []string
	for _, kv := range env {
		if p, ok := strings.CutPrefix(kv, "PATH="); ok {
			path = dir + string(os.PathListSeparator) + p
			continue
		}
		out = append(out, kv)
	}
	return append(out, "PATH="+path)
}

// coverDir is a directory of a run where tests with coverage leave what
// they covered: profile, the -coverprofile of go test, and counters, the
// GOCOVERDIR of the programs they start.
type coverDir string

func (d coverDir) profile() string  { return filepath.Join(string(d), "profile") }
func (d coverDir) counters() string { return filepath.Join(string(d), "counters") }

func (d coverDir) make() error { return os.MkdirAll(d.counters(), 0o755) }

// read adds what the tests covered to c: the profile, which has to be
// there, and what the programs wrote, turned into a profile by go tool
// covdata with env. Then it hands the programs' files on (handOn).
func (d coverDir) read(ctx context.Context, env []string, c ranBlocks) error {
	f, err := os.Open(d.profile())
	if err != nil {
		return fmt.Errorf("the tests wrote no coverage profile: %w", err)
	}
	err = c.read(f)
	f.Close()
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(d.counters())
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}
	programs := filepath.Join(string(d), "programs")
	if out, err := command(ctx, string(d), env, "go", "tool", "covdata", "textfmt", "-i="+d.counters(), "-o="+programs).CombinedOutput(); err != nil {
		return fmt.Errorf("go tool covdata: %v\n%s", err, out)
	}
	f, err = os.Open(programs)
	if err != nil {
		return err
	}
	err = c.read(f)
	f.Close()
	if err != nil {
		return err
	}
	return handOn(d.counters(), os.Getenv(envCoverDir))
}

// handOn copies the files of from into to when to is not empty: a run
// started inside a test of a run with coverage (envCoverDir set in its own
// environment) hands what the programs it started wrote on to that run,
// whose binary they may be.
func handOn(from, to string) error {
	if to == "" {
		return nil
	}
	entries, err := os.ReadDir(from)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := copyEntry(filepath.Join(from, e.Name()), filepath.Join(to, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// A testLevel is a step of the full build's tests (scripts/check.sh) that
// a run runs with coverage, in src: its go test arguments, and a directory
// of src that holds its tests, without which the tree has none.
type testLevel struct {
	name string   // as the progress names it
	args []string // after go test's own
	dir  string
}

var (
	levelIntegration = testLevel{"the integration tests", []string{"-tags", "integration", "./..."}, "."}
	levelE2E         = testLevel{"the journeys", []string{"-tags", "e2e", "./e2e/..."}, "e2e"}
)

// levelArgs are go test's arguments for a level with coverage, its profile
// and counters in d, each test binary run through the setup's binary with
// its go.
func levelArgs(l testLevel, setup coverSetup, d coverDir) []string {
	return slices.Concat([]string{"test", "-json", "-count=1", "-cover", "-covermode=set", "-coverpkg=./...",
		"-coverprofile=" + d.profile(), "-exec", testExecFlag(setup.self, d.counters(), setup.bin)}, l.args)
}

// levelEnv is the environment of a level's tests: the run's (mutationEnv
// in tmp), with tmux's sockets in tmp, and without the tmux server it runs
// under, so no test reaches the user's.
func levelEnv(environ []string, tmp string) []string {
	var env []string
	for _, kv := range mutationEnv(environ, tmp) {
		name, _, _ := strings.Cut(kv, "=")
		if name == "TMUX" || name == "TMUX_PANE" || name == "TMUX_TMPDIR" {
			continue
		}
		env = append(env, kv)
	}
	return append(env, "TMUX_TMPDIR="+tmp)
}

// runLevel runs the tests of a level in src with coverage, in the run's
// directory tmp, and adds what they covered to c. A test that fails ends
// it with an error that holds what the failed tests printed.
func runLevel(ctx context.Context, l testLevel, src, tmp string, setup coverSetup, c ranBlocks) error {
	d := coverDir(filepath.Join(tmp, "level"))
	if err := d.make(); err != nil {
		return err
	}
	defer os.RemoveAll(string(d))
	env := levelEnv(os.Environ(), tmp)
	cmd := command(ctx, src, env, "go", levelArgs(l, setup, d)...)
	// A test may start a command of its own process group (a gate a test
	// of the gate starts does): ending go test's group leaves it running.
	cmd.Cancel = func() error { return killTree(cmd.Process.Pid) }
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	endTmuxServers(tmp)
	if ctx.Err() != nil {
		return errInterrupted
	}
	if err != nil {
		return fmt.Errorf("%s failed, so no line is judged:\n%s", l.name, failedOutput(out.Bytes()))
	}
	return d.read(ctx, env, c)
}

// failedOutput is what go test prints without -json of the go test -json
// output out, without the lines of the packages that passed or have no
// test files (with -coverpkg, a tab, the package, two tabs and its
// coverage).
func failedOutput(out []byte) string {
	run, _ := readTestRun(bytes.NewReader(out)) // reading from memory, it cannot fail
	var kept []string
	for _, l := range run.plainLines() {
		if strings.HasPrefix(l, "ok  \t") || strings.HasPrefix(l, "?   \t") || noTestFiles.MatchString(l) {
			continue
		}
		kept = append(kept, l)
	}
	return strings.Join(kept, "\n")
}

// noTestFiles is go test's line, with -coverpkg, for a package without test
// files.
var noTestFiles = regexp.MustCompile(`^\t\S+\t\tcoverage: `)

// endTmuxServers ends every tmux server whose socket tmux made in tmp, the
// TMUX_TMPDIR of a level's tests (in tmux-UID, a directory of its own): a
// test ended before its cleanup (an interrupted run) leaves its server
// running, apart from the run's processes. tmp itself, not a directory
// below it, keeps the sockets' paths short enough for tmux.
func endTmuxServers(tmp string) {
	sockets, _ := filepath.Glob(filepath.Join(tmp, "tmux-*", "*"))
	for _, p := range sockets {
		if info, err := os.Lstat(p); err != nil || info.Mode()&fs.ModeSocket == 0 {
			continue
		}
		kill := exec.Command("tmux", "-S", p, "kill-server")
		kill.Stdout, kill.Stderr = io.Discard, io.Discard
		kill.Run()
	}
}

// killTree ends with SIGKILL the process pid, its process group and every
// process below it, each with its own process group, found before any is
// ended: ps lists them, as no portable call does.
func killTree(pid int) error {
	out, err := exec.Command("ps", "-A", "-o", "pid=", "-o", "ppid=").Output()
	pids := []int{pid}
	if err == nil {
		pids = append(pids, descendants(string(out), pid)...)
	}
	for _, p := range pids {
		syscall.Kill(-p, syscall.SIGKILL)
		syscall.Kill(p, syscall.SIGKILL)
	}
	return nil
}

// descendants are the processes below pid in ps's list of pid and ppid
// pairs, one pair a line, in the order it finds them.
func descendants(ps string, pid int) []int {
	children := map[int][]int{}
	for _, l := range strings.Split(ps, "\n") {
		var p, parent int
		if _, err := fmt.Sscan(l, &p, &parent); err == nil {
			children[parent] = append(children[parent], p)
		}
	}
	var below []int
	for next := []int{pid}; len(next) > 0; {
		p := next[0]
		next = next[1:]
		for _, c := range children[p] {
			if !slices.Contains(below, c) && c != pid {
				below = append(below, c)
				next = append(next, c)
			}
		}
	}
	return below
}
