//go:build integration

package main

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The runs below judge the changed lines that no unit test runs with the
// higher levels of the small repository's tests (#275): five tests, six
// runs of the gate, seven of the tool (the run in parts runs it twice). Each level's tests note in the file $TESTGATE_MARKS
// that they ran, which the gate's environment passes on to them.

const envMarks = "TESTGATE_MARKS"

// marks points $TESTGATE_MARKS at a new file and returns it.
func marks(t *testing.T) string {
	t.Helper()
	f := filepath.Join(t.TempDir(), "marks")
	t.Setenv(envMarks, f)
	return f
}

// marked returns the lines the levels' tests wrote to the file of marks.
func marked(t *testing.T, file string) []string {
	t.Helper()
	data, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Fields(string(data))
}

// markGo is a Go function, for a test of the small repository, that appends
// its word to the file of marks.
const markGo = `
func mark(t *testing.T, word string) {
	f, err := os.OpenFile(os.Getenv("` + envMarks + `"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(word + "\n"); err != nil {
		t.Fatal(err)
	}
}
`

// levels gives the small repository an integration test and a journey,
// each noting that it ran, and lib a function each of them runs.
func (r *mutationRepo) levels() {
	write(r.t, r.dir, "lib/levels.go", "package lib\n\n"+
		"// Integ is run only by an integration test.\nfunc Integ(x int) int { return x + 1 }\n\n"+
		"// Journey is run only by a journey.\nfunc Journey() string { return \"journey\" }\n")
	write(r.t, r.dir, "lib/integ_integration_test.go", "//go:build integration\n\npackage lib\n\n"+
		"import (\n\t\"os\"\n\t\"os/exec\"\n\t\"path/filepath\"\n\t\"testing\"\n)\n"+markGo+`
func TestInteg(t *testing.T) {
	mark(t, "integration")
	if Integ(1) != 2 {
		t.Fatal("Integ(1)")
	}
	// A program a test builds and starts, as hq's integration tests build hq.
	bin := filepath.Join(t.TempDir(), "prog")
	if out, err := exec.Command("go", "build", "-o", bin, "example.com/m/cmd/prog").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	if out, err := exec.Command(bin).CombinedOutput(); err != nil || string(out) != "started\n" {
		t.Fatalf("prog: %q, %v", out, err)
	}
}
`)
	write(r.t, r.dir, "e2e/journey_e2e_test.go", "//go:build e2e\n\npackage e2e\n\n"+
		"import (\n\t\"os\"\n\t\"testing\"\n\n\t\"example.com/m/lib\"\n)\n"+markGo+`
func TestJourney(t *testing.T) {
	mark(t, "e2e")
	if lib.Journey() != "journey" {
		t.Fatal("Journey")
	}
}
`)
	write(r.t, r.dir, "cmd/prog/main.go", "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(started())\n}\n\n"+
		"// started runs only in the program, which no unit test starts.\nfunc started() string {\n\treturn \"started\"\n}\n")
}

// levelsOnMain puts the levels into main, the base, and work on top of it.
func (r *mutationRepo) levelsOnMain() {
	r.t.Helper()
	r.git(r.dir, "checkout", "-q", "main")
	r.levels()
	r.git(r.dir, "add", "-A")
	r.git(r.dir, "commit", "-q", "-m", "Levels")
	r.git(r.dir, "checkout", "-q", "-B", "work", "main")
}

func TestMutationRunsTheHigherLevelsForTheLinesNoUnitTestRuns(t *testing.T) {
	r := newMutationRepo(t)
	r.levels()
	file := marks(t)
	tmp, cache := isolated(t)
	var progress strings.Builder
	var out strings.Builder
	failed, err := runMutation(context.Background(), r.dir, "main", &out, &progress)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	head := r.git(r.dir, "rev-parse", "HEAD")
	want := []string{
		// Integ's mutant: no unit test covers it, the integration test runs it
		"not covered lib/levels.go:4:34 arithmetic base",
		// no line not run: Integ's by the integration test, Journey's by the
		// journey, started's in the program the integration test starts
		"mutation: 0 killed, 0 alive, 0 timed out, 1 not covered, 0 not run against main on " + head + " with uncommitted changes",
	}
	if !reflect.DeepEqual(lines, want) {
		t.Errorf("got\n%s\nwant\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
	if failed {
		t.Error("a mutant not covered fails the run")
	}
	// The three cases, each on its own.
	for _, c := range []struct{ name, line string }{
		{"a line only an integration test runs", "not run    lib/levels.go:4"},
		{"a line only a journey runs", "not run    lib/levels.go:7"},
		{"a line that runs only in a program a test builds and starts", "not run    cmd/prog/main.go:11"},
		{"main of that program", "not run    cmd/prog/main.go:6"},
	} {
		if slices.Contains(lines, c.line) {
			t.Errorf("%s is not run: %q", c.name, c.line)
		}
	}
	if got := marked(t, file); !reflect.DeepEqual(got, []string{"integration", "e2e"}) {
		t.Errorf("the levels ran %q, want the integration tests and then the journeys, once each", got)
	}
	for _, say := range []string{
		"mutation: 4 changed lines are not run so far, running the integration tests once with coverage\nmutation: the integration tests done in ",
		"mutation: 1 changed line is not run so far, running the journeys once with coverage\nmutation: the journeys done in ",
	} {
		if !strings.Contains(progress.String(), say) {
			t.Errorf("the progress does not say %q:\n%s", say, progress.String())
		}
	}
	assertEmpty(t, "the temporary directory", tmp)
	assertEmpty(t, "the shared build cache", cache)
}

func TestMutationFailsOnAChangedLineNoTestRunsAlsoInParts(t *testing.T) {
	r := newMutationRepo(t)
	r.levelsOnMain()
	// Two lines with no mutant on them, in two packages: no test runs them.
	write(t, r.dir, "lib/never.go", "package lib\n\nimport \"fmt\"\n\n// Never runs under no test.\nfunc Never() {\n\tfmt.Println(\"never\")\n}\n")
	write(t, r.dir, "cmd/tool/never.go", "package main\n\n// never runs under no test.\nfunc never() {\n\tkeep()\n}\n")
	// A git configuration whose diffs name files without a/ and b/, or
	// with i/ and w/: the gate reads its diff with its own prefixes.
	config := t.TempDir()
	write(t, config, "git/config", "[diff]\n\tnoprefix = true\n\tmnemonicPrefix = true\n")
	t.Setenv("XDG_CONFIG_HOME", config)
	if got := r.git(r.dir, "config", "--get", "diff.noprefix"); got != "true" {
		t.Fatalf("git does not read the configuration: diff.noprefix is %q", got)
	}
	file := marks(t)
	tmp, cache := isolated(t)
	lines, failed, err := runMutationIn(t, r.dir, "main")
	if err != nil {
		t.Fatal(err)
	}
	head := r.git(r.dir, "rev-parse", "HEAD")
	wantWhole := []string{
		"not run    cmd/tool/never.go:5",
		"not run    lib/never.go:7",
		"mutation: no mutants in the lines changed, 2 not run against main on " + head + " with uncommitted changes",
	}
	if !reflect.DeepEqual(lines, wantWhole) {
		t.Errorf("got\n%s\nwant\n%s", strings.Join(lines, "\n"), strings.Join(wantWhole, "\n"))
	}
	if !failed {
		t.Error("a changed line no test runs passes")
	}
	if got := marked(t, file); !reflect.DeepEqual(got, []string{"integration", "e2e"}) {
		t.Errorf("the levels ran %q, want the integration tests and then the journeys, once each", got)
	}
	// In parts, the tool runs twice and the levels once: the same lines.
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	watch := &partWatch{t: t, tmp: tmp}
	lines, failed, err = runPartsIn(t, r.dir, "main", watch, "lib", "cmd/tool")
	if err != nil {
		t.Fatal(err)
	}
	wantParts := []string{
		"not run    cmd/tool/never.go:5",
		"not run    lib/never.go:7",
		"mutation: no mutants in the lines changed, 2 not run, 0 changed files in no part against main on " + head + " with uncommitted changes",
	}
	if !reflect.DeepEqual(lines, wantParts) || !failed {
		t.Errorf("in parts: got\n%s\nfailed %v; want\n%s\nfailed", strings.Join(lines, "\n"), failed, strings.Join(wantParts, "\n"))
	}
	if watch.started != 2 {
		t.Errorf("%d parts ran the tool, want 2", watch.started)
	}
	if got := marked(t, file); !reflect.DeepEqual(got, []string{"integration", "e2e"}) {
		t.Errorf("in parts the levels ran %q, want the integration tests and then the journeys, once each", got)
	}
	assertEmpty(t, "the temporary directory", tmp)
	assertEmpty(t, "the shared build cache", cache)
}

func TestMutationStartsNoHigherLevelWhenTheUnitTestsRunEveryChangedLine(t *testing.T) {
	r := newMutationRepo(t)
	r.levelsOnMain()
	// Double, run by a unit test of another package.
	write(t, r.dir, "lib/lib.go", "package lib\n\n// Half halves x.\nfunc Half(x int) int { return x / 2 }\n\n"+
		"// Double doubles x.\nfunc Double(x int) int {\n\treturn x * 2\n}\n")
	write(t, r.dir, "user/user_test.go", "package user\n\nimport (\n\t\"testing\"\n\n\t\"example.com/m/lib\"\n)\n\n"+
		"func TestDouble(t *testing.T) {\n\tif lib.Double(4) != 8 {\n\t\tt.Fatal(\"Double(4)\")\n\t}\n}\n")
	file := marks(t)
	isolated(t)
	var progress strings.Builder
	var out strings.Builder
	failed, err := runMutation(context.Background(), r.dir, "main", &out, &progress)
	if err != nil {
		t.Fatal(err)
	}
	head := r.git(r.dir, "rev-parse", "HEAD")
	want := "mutation: 1 killed, 0 alive, 0 timed out, 0 not covered, 0 not run against main on " + head + " with uncommitted changes\n"
	if out.String() != want || failed {
		t.Errorf("got %q, failed %v; want %q, passed", out.String(), failed, want)
	}
	if got := marked(t, file); got != nil {
		t.Errorf("the levels ran %q, want none", got)
	}
	if strings.Contains(progress.String(), "not run so far") {
		t.Errorf("the progress names a level:\n%s", progress.String())
	}
}

func TestMutationEndsWithAHigherLevelThatFails(t *testing.T) {
	r := newMutationRepo(t)
	r.levels()
	write(t, r.dir, "lib/fail_integration_test.go", "//go:build integration\n\npackage lib\n\nimport \"testing\"\n\n"+
		"func TestFails(t *testing.T) { t.Fatal(\"an integration test that fails (simulated)\") }\n")
	marks(t)
	tmp, cache := isolated(t)
	lines, failed, err := runMutationIn(t, r.dir, "main")
	if err == nil || !strings.Contains(err.Error(), "the integration tests failed, so no line is judged") ||
		!strings.Contains(err.Error(), "--- FAIL: TestFails") ||
		!strings.Contains(err.Error(), "an integration test that fails (simulated)") {
		t.Errorf("err %v; want one saying the integration tests failed, with the failed test's output", err)
	}
	if err != nil && strings.Contains(err.Error(), "ok  \t") {
		t.Errorf("the error holds the lines of packages that passed:\n%v", err)
	}
	if failed || !reflect.DeepEqual(lines, []string{""}) {
		t.Errorf("printed %q, failed %v; want nothing", lines, failed)
	}
	assertEmpty(t, "the temporary directory", tmp)
	assertEmpty(t, "the shared build cache", cache)
}

func TestMutationInterruptedWhileAHigherLevelRunsLeavesNothingBehind(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("no tmux")
	}
	r := newMutationRepo(t)
	r.levels()
	// An integration test that starts a tmux server of its own, notes its
	// process, and waits: it is ended before its cleanup.
	write(t, r.dir, "lib/wait_integration_test.go", "//go:build integration\n\npackage lib\n\n"+
		"import (\n\t\"os/exec\"\n\t\"strings\"\n\t\"testing\"\n\t\"time\"\n)\n\n"+`
func TestWait(t *testing.T) {
	name := "mutation-wait"
	if out, err := exec.Command("tmux", "-L", name, "new-session", "-d", "sleep 120").CombinedOutput(); err != nil {
		t.Fatalf("tmux: %v\n%s", err, out)
	}
	t.Cleanup(func() { exec.Command("tmux", "-L", name, "kill-server").Run() })
	pid, err := exec.Command("tmux", "-L", name, "display-message", "-p", "#{pid}").Output()
	if err != nil {
		t.Fatal(err)
	}
	mark(t, "server-"+strings.TrimSpace(string(pid)))
	time.Sleep(120 * time.Second)
}
`)
	file := marks(t)
	_, cache := isolated(t)
	// tmux's sockets go into the run's temporary directory: one under a
	// test's TempDir, named after the test, makes a path longer than a
	// socket's may be.
	tmp, err := os.MkdirTemp("/tmp", "m")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(tmp) })
	t.Setenv("TMPDIR", tmp)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := runMutation(ctx, r.dir, "main", io.Discard, io.Discard)
		done <- err
	}()
	pid := 0
	deadline := time.Now().Add(2 * time.Minute)
	for pid == 0 {
		for _, m := range marked(t, file) {
			if p, ok := strings.CutPrefix(m, "server-"); ok {
				pid, _ = strconv.Atoi(p)
			}
		}
		select {
		case err := <-done:
			t.Fatalf("the run ended before the integration test started its tmux server: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("the integration test did not start its tmux server within two minutes")
		}
		time.Sleep(100 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, errInterrupted) {
			t.Errorf("err %v, want %v", err, errInterrupted)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the run did not stop within 20 s of being cancelled")
	}
	gone := time.Now().Add(10 * time.Second)
	for syscall.Kill(pid, 0) == nil {
		if time.Now().After(gone) {
			syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("the test's tmux server %d still runs 10 s after the run stopped", pid)
		}
		time.Sleep(100 * time.Millisecond)
	}
	assertEmpty(t, "the temporary directory", tmp)
	assertEmpty(t, "the shared build cache", cache)
}
