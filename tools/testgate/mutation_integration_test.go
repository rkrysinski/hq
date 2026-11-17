//go:build integration

package main

import (
	"bytes"
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
	"testing"
	"time"
)

// mutationRepo is a git repository holding a small Go module of its own:
// branch main, and branch work checked out, which forked from main before
// main changed a line of lib/lib.go.
type mutationRepo struct {
	*gitRepo
	base string // main's first commit, where work forked
}

func newMutationRepo(t *testing.T) *mutationRepo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	r := &mutationRepo{gitRepo: &gitRepo{t, t.TempDir()}}
	r.git(r.dir, "init", "-q", "-b", "main")
	r.git(r.dir, "config", "user.email", "t@example.com")
	r.git(r.dir, "config", "user.name", "t")
	write(t, r.dir, "go.mod", "module example.com/m\n\ngo 1.26\n")
	write(t, r.dir, "lib/lib.go", "package lib\n\n// Half halves x.\nfunc Half(x int) int { return x / 2 }\n")
	write(t, r.dir, "lib/lib_test.go", "package lib\n\nimport \"testing\"\n\n"+
		"func TestHalf(t *testing.T) {\n\tif Half(8) != 4 {\n\t\tt.Fatal(\"Half(8)\")\n\t}\n}\n")
	write(t, r.dir, "cmd/tool/main.go", "package main\n\nfunc main() {}\n\n// keep stays as it was.\nfunc keep() {}\n")
	r.base = r.commit("README.md", "m\n", "Base")
	r.git(r.dir, "checkout", "-q", "-b", "work")
	return r
}

// change gives work its changes: in package main a comparison that the
// tests do not tell from its boundary mutant and, after two unchanged lines
// in the same hunk of the diff, a line no test runs, committed; on main
// afterwards a change to the line of Half, which work
// must not be charged for; and uncommitted in work a line of lib that only
// a test of another package, untracked, runs.
func (r *mutationRepo) change() {
	write(r.t, r.dir, "cmd/tool/main.go", "package main\n\nfunc main() {}\n\n"+
		"// big tells whether n is big.\nfunc big(n int) bool { return n > 10 }\n\n"+
		"// keep stays as it was.\nfunc keep() {}\n\n"+
		"func unused(n int) int { return n + 1 }\n")
	write(r.t, r.dir, "cmd/tool/main_test.go", "package main\n\nimport \"testing\"\n\n"+
		"func TestBig(t *testing.T) {\n\tif !big(20) || big(1) {\n\t\tt.Fatal(\"big\")\n\t}\n}\n")
	r.commit("README.md", "m, work\n", "Work")
	r.git(r.dir, "checkout", "-q", "main")
	r.commit("lib/lib.go", "package lib\n\n// Half halves x.\nfunc Half(x int) int { return x / 2 } // halved\n", "Main moves on")
	r.git(r.dir, "checkout", "-q", "work")
	write(r.t, r.dir, "lib/lib.go", "package lib\n\n// Half halves x.\nfunc Half(x int) int { return x / 2 }\n\n"+
		"// Double doubles x.\nfunc Double(x int) int { return x * 2 }\n")
	write(r.t, r.dir, "user/user_test.go", "package user\n\nimport (\n\t\"testing\"\n\n\t\"example.com/m/lib\"\n)\n\n"+
		"func TestDouble(t *testing.T) {\n\tif lib.Double(4) != 8 {\n\t\tt.Fatal(\"Double(4)\")\n\t}\n}\n")
}

// Each real run of the tool installs it with an empty cache and starts as
// many workers as there are cores, which loads the machine while the other
// packages' integration tests run: so only four tests of this file run it,
// five times (the next one, the machine that fails, the slowed run, which
// runs it twice, and the interrupted run), and six tests of runs in parts
// add seven runs (mutation_parts_integration_test.go).
// The passing run and a change without mutants are proven by the unit tests
// of mutationReport.

// isolated points the temporary directory and the shared build cache of
// the test at empty directories of its own and returns them.
func isolated(t *testing.T) (tmp, cache string) {
	t.Helper()
	tmp, cache = t.TempDir(), t.TempDir()
	t.Setenv("TMPDIR", tmp)
	t.Setenv("GOCACHE", cache)
	return tmp, cache
}

func assertEmpty(t *testing.T, what, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) > 0 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("%s is left with %q", what, names)
	}
}

func runMutationIn(t *testing.T, dir, base string) ([]string, bool, error) {
	t.Helper()
	var out bytes.Buffer
	failed, err := runMutation(context.Background(), dir, base, &out, io.Discard)
	return strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n"), failed, err
}

func TestMutationJudgesTheChangedLinesWithTheWholeUnitSuite(t *testing.T) {
	r := newMutationRepo(t)
	r.change()
	tmp, cache := isolated(t)
	lines, failed, err := runMutationIn(t, r.dir, "main")
	if err != nil {
		t.Fatal(err)
	}
	head := r.git(r.dir, "rev-parse", "HEAD")
	want := []string{
		// package main: a real test run, so alive, not falsely killed
		"alive      cmd/tool/main.go:6:33 conditionals boundary",
		// killed: big's negation, and Double, whose only test is in package user;
		// not covered: unused, a changed line that follows unchanged ones in
		// its hunk; nothing of Half, which only main changed
		"mutation: 2 killed, 1 alive, 0 timed out, 1 not covered against main on " + head + " with uncommitted changes",
	}
	if !reflect.DeepEqual(lines, want) {
		t.Errorf("got\n%s\nwant\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
	if !failed {
		t.Error("an alive mutant passes")
	}
	assertEmpty(t, "the temporary directory", tmp)
	assertEmpty(t, "the shared build cache", cache)
}

// A unit test that passes in the tool's first run and fails in every run
// after it, mutant or not, as when the disk fills during a run: the run
// ends with an error, and no mutant is judged.
func TestMutationEndsWhenTheMachineFailsTheTests(t *testing.T) {
	r := newMutationRepo(t)
	r.change()
	full := filepath.Join(t.TempDir(), "full")
	write(t, r.dir, "machine/machine_test.go", "package machine\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\n"+
		"func TestDisk(t *testing.T) {\n"+
		"\tif _, err := os.Stat("+strconv.Quote(full)+"); err == nil {\n"+
		"\t\tt.Fatal(\"no space left on device (simulated)\")\n\t}\n"+
		"\tif err := os.WriteFile("+strconv.Quote(full)+", nil, 0o644); err != nil {\n\t\tt.Fatal(err)\n\t}\n}\n")
	tmp, cache := isolated(t)
	lines, failed, err := runMutationIn(t, r.dir, "main")
	if err == nil || !strings.Contains(err.Error(), "the unit tests fail without a mutant") ||
		!strings.Contains(err.Error(), "no space left on device (simulated)") {
		t.Errorf("err %v; want one saying the unit tests fail without a mutant, with their output", err)
	}
	if failed || !reflect.DeepEqual(lines, []string{""}) {
		t.Errorf("printed %q, failed %v; want nothing", lines, failed)
	}
	assertEmpty(t, "the temporary directory", tmp)
	assertEmpty(t, "the shared build cache", cache)
}

func TestMutationWithNothingToMutate(t *testing.T) {
	r := newMutationRepo(t)
	tmp, cache := isolated(t)
	write(t, r.dir, "docs/notes.md", "notes\n")
	write(t, r.dir, "lib/more_test.go", "package lib\n\nimport \"testing\"\n\nfunc TestMore(t *testing.T) {}\n")
	sha := r.commit("scripts/run.sh", "#!/bin/sh\n", "No Go code")
	lines, failed, err := runMutationIn(t, r.dir, "main")
	want := []string{"mutation: no mutants in the lines changed against main on " + sha}
	if err != nil || failed || !reflect.DeepEqual(lines, want) {
		t.Errorf("documents and tests: got %q, failed %v, err %v; want %q", lines, failed, err, want)
	}
	assertEmpty(t, "the temporary directory", tmp) // nothing staged, no tool run
	assertEmpty(t, "the shared build cache", cache)
}

func TestMutationRefusesABaseGitDoesNotKnow(t *testing.T) {
	r := newMutationRepo(t)
	for _, base := range []string{"origin/dev", "no-such-branch"} {
		_, _, err := runMutationIn(t, r.dir, base)
		if err == nil || !strings.Contains(err.Error(), "origin/dev for a single issue") {
			t.Errorf("%s: err %v; want one saying what to pass", base, err)
		}
	}
	if _, _, err := runMutationIn(t, t.TempDir(), "main"); err == nil {
		t.Error("a directory outside git is accepted")
	}
}

func TestMutationStagesOnlyWhatGitTracksOrWouldTrack(t *testing.T) {
	r := newMutationRepo(t)
	r.change()
	write(t, r.dir, ".gitignore", "big/\n.claude/worktrees/\n")
	write(t, r.dir, "big/blob.bin", strings.Repeat("x", 1<<20))
	r.git(r.dir, "worktree", "add", "-q", "-b", "other", ".claude/worktrees/other", "main")
	write(t, r.dir, "nested/go.mod", "module nested\n")
	r.git(filepath.Join(r.dir, "nested"), "init", "-q")
	r.git(r.dir, "rm", "-q", "README.md") // deleted, staged
	tmp := t.TempDir()
	src, gitDir := filepath.Join(tmp, "src"), filepath.Join(tmp, "git")
	if err := stageTree(context.Background(), r.dir, r.base, src, gitDir, mutationEnv(os.Environ(), tmp)); err != nil {
		t.Fatal(err)
	}
	var staged []string
	err := filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		staged = append(staged, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{".git", ".gitignore", "cmd/tool/main.go", "cmd/tool/main_test.go", "go.mod",
		"lib/lib.go", "lib/lib_test.go", "user/user_test.go"}
	if !slices.Equal(staged, want) {
		t.Errorf("staged %q\nwant %q", staged, want)
	}
	if dotGit, _ := os.ReadFile(filepath.Join(src, ".git")); !strings.HasPrefix(string(dotGit), "gitdir: ") {
		t.Errorf(".git is %q, want a one-line pointer to the staging repository", dotGit)
	}
	// The staging repository sees exactly the change against the fork point.
	diff := r.git(src, "diff", "--name-only", "--merge-base", r.base)
	wantDiff := ".gitignore\nREADME.md\ncmd/tool/main.go\ncmd/tool/main_test.go\nlib/lib.go\nuser/user_test.go"
	if diff != wantDiff {
		t.Errorf("diff in the staging repository:\n%s\nwant\n%s", diff, wantDiff)
	}
	// Its objects are the checkout's, borrowed: it holds only the three files
	// the checkout's git does not have (lib.go as changed, the untracked
	// user_test.go and .gitignore).
	own, err := filepath.Glob(filepath.Join(gitDir, "objects", "[0-9a-f][0-9a-f]", "*"))
	if err != nil || len(own) != 3 {
		t.Errorf("the staging repository holds %d objects of its own, want 3", len(own))
	}
}

func TestMutationInterruptedLeavesNothingBehind(t *testing.T) {
	r := newMutationRepo(t)
	// A slow unit suite, so the tool is still running when the run is cancelled.
	write(t, r.dir, "lib/slow_test.go", "package lib\n\nimport (\n\t\"testing\"\n\t\"time\"\n)\n\n"+
		"func TestSlow(t *testing.T) { time.Sleep(30 * time.Second) }\n")
	r.change()
	tmp, cache := isolated(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := runMutation(ctx, r.dir, "main", io.Discard, io.Discard)
		done <- err
	}()
	deadline := time.Now().Add(time.Minute)
	for {
		if bins, _ := filepath.Glob(filepath.Join(tmp, "hq-mutation-*", "bin", "gremlins")); len(bins) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the tool was not installed within a minute")
		}
		time.Sleep(100 * time.Millisecond)
	}
	time.Sleep(time.Second) // the tool's first run of the tests is under way
	start := time.Now()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, errInterrupted) {
			t.Errorf("err %v, want %v", err, errInterrupted)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the run did not stop within 20 s of being cancelled")
	}
	if waited := time.Since(start); waited > 10*time.Second {
		t.Errorf("the run took %s to stop: the tests it started were not ended", waited)
	}
	assertEmpty(t, "the temporary directory", tmp)
	assertEmpty(t, "the shared build cache", cache)
}

// slowGoFactor is how many times longer every mutant's run of the unit
// tests takes in TestMutationJudgesTheSameWhenEveryMutantRunsSlower: on #213's
// change the runs with every core kept busy took 1.2 times as long as those on
// an idle machine, and 3 leaves a margin of 2.5 over that (#234).
const slowGoFactor = 3

// slowGo puts first in PATH a go that runs the real one and, for a mutant's
// run of the unit tests (the tool's only go test with -failfast), then waits
// factor-1 times as long as that run took, as a loaded machine would make it
// take factor times as long.
func slowGo(t *testing.T, factor int) {
	t.Helper()
	real, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go")
	}
	dir := t.TempDir()
	write(t, dir, "go.mod", "module slowgo\n\ngo 1.26\n")
	write(t, dir, "main.go", `package main

import (
	"errors"
	"os"
	"os/exec"
	"slices"
	"time"
)

func main() {
	cmd := exec.Command(`+strconv.Quote(real)+`, os.Args[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	start := time.Now()
	err := cmd.Run()
	if slices.Contains(os.Args[1:], "-failfast") {
		time.Sleep(`+strconv.Itoa(factor-1)+` * time.Since(start))
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		os.Exit(exit.ExitCode())
	}
	if err != nil {
		os.Exit(1)
	}
}
`)
	bin := filepath.Join(dir, "bin")
	build := exec.Command(real, "build", "-o", filepath.Join(bin, "go"), ".")
	build.Dir = dir
	build.Env = append(os.Environ(), "GOCACHE="+t.TempDir())
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the slow go: %v\n%s", err, out)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestMutationJudgesTheSameWhenEveryMutantRunsSlower(t *testing.T) {
	r := newMutationRepo(t)
	r.change()
	isolated(t)
	want, wantFailed, err := runMutationIn(t, r.dir, "main")
	if err != nil {
		t.Fatal(err)
	}
	slowGo(t, slowGoFactor)
	tmp, cache := isolated(t)
	lines, failed, err := runMutationIn(t, r.dir, "main")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(lines, want) || failed != wantFailed {
		t.Errorf("every mutant %d times slower: got\n%s\nfailed %v; want\n%s\nfailed %v",
			slowGoFactor, strings.Join(lines, "\n"), failed, strings.Join(want, "\n"), wantFailed)
	}
	assertEmpty(t, "the temporary directory", tmp)
	assertEmpty(t, "the shared build cache", cache)
}
