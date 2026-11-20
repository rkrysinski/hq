//go:build integration

package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// stubGo stands in for the toolchain: it logs each call and the TMPDIR it
// runs with, hands the testgate commands that read a test step's output to
// the testgate built from this checkout (STUB_TESTGATE), prints another
// gate's figure as "figure <gate>", and fails with "boom" when the call
// contains STUB_FAIL. A go test call prints the go test -json events of a
// test that passed, of a test and a subtest that skipped too when it
// contains STUB_SKIP, and of a test that failed before "boom".
// stubGofmt names an unformatted file when STUB_UNFORMATTED is set.
const stubGo = `#!/bin/sh
echo "go $*" >> "$STUB_LOG"
echo "TMPDIR=$TMPDIR" >> "$STUB_LOG"
case "$*" in "run ./tools/testgate skips "* | "run ./tools/testgate plain "*) shift 2; exec "$STUB_TESTGATE" "$@" ;; esac
case "$*" in "run ./tools/testgate "*) echo "figure $3" ;; esac
[ "$1" = test ] && cat "$STUB_EVENTS/passed.json"
case "$*" in *"$STUB_SKIP"*) [ "$1" = test ] && [ -n "$STUB_SKIP" ] && cat "$STUB_EVENTS/skipped.json" ;; esac
case "$*" in *"$STUB_FAIL"*) [ -n "$STUB_FAIL" ] && { [ "$1" = test ] && cat "$STUB_EVENTS/failed.json"; echo boom; exit 1; } ;; esac
exit 0
`

// The go test -json events the stub go prints.
const (
	passedEvents = `{"Action":"start","Package":"example.com/p"}
{"Action":"run","Package":"example.com/p","Test":"TestPasses"}
{"Action":"output","Package":"example.com/p","Test":"TestPasses","Output":"--- PASS: TestPasses (0.00s)\n"}
{"Action":"pass","Package":"example.com/p","Test":"TestPasses","Elapsed":0}
`
	skippedEvents = `{"Action":"run","Package":"example.com/p","Test":"TestSkipped"}
{"Action":"output","Package":"example.com/p","Test":"TestSkipped","Output":"    p_test.go:9: no tmux here\n"}
{"Action":"output","Package":"example.com/p","Test":"TestSkipped","Output":"--- SKIP: TestSkipped (0.00s)\n"}
{"Action":"skip","Package":"example.com/p","Test":"TestSkipped","Elapsed":0}
{"Action":"run","Package":"example.com/p","Test":"TestParent"}
{"Action":"run","Package":"example.com/p","Test":"TestParent/sub_case"}
{"Action":"output","Package":"example.com/p","Test":"TestParent/sub_case","Output":"    p_test.go:12: 4095 of 5000 bytes\n"}
{"Action":"output","Package":"example.com/p","Test":"TestParent/sub_case","Output":"        on two lines\n"}
{"Action":"skip","Package":"example.com/p","Test":"TestParent/sub_case","Elapsed":0}
{"Action":"pass","Package":"example.com/p","Test":"TestParent","Elapsed":0}
`
	failedEvents = `{"Action":"run","Package":"example.com/p","Test":"TestBroken"}
{"Action":"output","Package":"example.com/p","Test":"TestBroken","Output":"=== RUN   TestBroken\n"}
{"Action":"output","Package":"example.com/p","Test":"TestBroken","Output":"    p_test.go:7: broken\n"}
{"Action":"output","Package":"example.com/p","Test":"TestBroken","Output":"--- FAIL: TestBroken (0.00s)\n"}
{"Action":"fail","Package":"example.com/p","Test":"TestBroken","Elapsed":0}
{"Action":"output","Package":"example.com/p","Output":"FAIL\n"}
{"Action":"output","Package":"example.com/p","Output":"FAIL\texample.com/p\t0.01s\n"}
{"Action":"fail","Package":"example.com/p","Elapsed":0.01}
`
)

// buildTestgate builds the testgate of this checkout, which the stub go
// runs for the commands that read a test step's go test -json output.
func buildTestgate(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	bin := filepath.Join(t.TempDir(), "testgate")
	cmd := exec.Command("go", "build", "-o", bin, "./tools/testgate")
	cmd.Dir = filepath.Join(filepath.Dir(file), "..")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build ./tools/testgate: %v\n%s", err, out)
	}
	return bin
}

const stubGofmt = `#!/bin/sh
echo "gofmt $*" >> "$STUB_LOG"
[ -n "$STUB_UNFORMATTED" ] && echo "$STUB_UNFORMATTED"
exit 0
`

// checkRepo is a git repository holding a copy of scripts/check.sh, with the
// stub toolchain first on PATH and a TMPDIR of the test's own, where the
// script makes its directory.
type checkRepo struct {
	t   *testing.T
	dir string
	log string
	env []string
}

func newCheckRepo(t *testing.T, testgate string) *checkRepo {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	script, err := os.ReadFile(filepath.Join(filepath.Dir(file), "check.sh"))
	if err != nil {
		t.Fatal(err)
	}
	r := &checkRepo{t: t, dir: t.TempDir()}
	stubs := t.TempDir()
	r.log = filepath.Join(stubs, "log")
	writeFile(t, filepath.Join(stubs, "go"), stubGo, 0o755)
	writeFile(t, filepath.Join(stubs, "gofmt"), stubGofmt, 0o755)
	events := t.TempDir()
	writeFile(t, filepath.Join(events, "passed.json"), passedEvents, 0o644)
	writeFile(t, filepath.Join(events, "skipped.json"), skippedEvents, 0o644)
	writeFile(t, filepath.Join(events, "failed.json"), failedEvents, 0o644)
	writeFile(t, filepath.Join(r.dir, "scripts", "check.sh"), string(script), 0o755)
	writeFile(t, filepath.Join(r.dir, "e2e", "journey_test.go"), "package e2e\n", 0o644)
	r.env = append(os.Environ(),
		"PATH="+stubs+string(os.PathListSeparator)+os.Getenv("PATH"),
		"STUB_LOG="+r.log, "STUB_FAIL=", "STUB_UNFORMATTED=", "STUB_SKIP=", "STUB_TESTGATE="+testgate, "STUB_EVENTS="+events,
		"GITHUB_STEP_SUMMARY=", "TMPDIR="+t.TempDir())
	r.git("init", "-q")
	r.git("add", ".")
	r.git("-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-qm", "init")
	return r
}

func writeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func (r *checkRepo) git(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = r.dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %v: %s", args, out)
	}
	return strings.TrimSpace(string(out))
}

// run runs the script from a directory outside the repository, with extra
// environment, and returns its output lines and exit status.
func (r *checkRepo) run(env ...string) ([]string, int) {
	r.t.Helper()
	cmd := exec.Command(filepath.Join(r.dir, "scripts", "check.sh"))
	cmd.Env = append(append([]string{}, r.env...), env...)
	cmd.Dir = r.t.TempDir()
	out, err := cmd.CombinedOutput()
	code := 0
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if err != nil {
		r.t.Fatal(err)
	}
	return strings.Split(strings.TrimRight(string(out), "\n"), "\n"), code
}

func (r *checkRepo) calls() string {
	r.t.Helper()
	b, err := os.ReadFile(r.log)
	if err != nil && !os.IsNotExist(err) {
		r.t.Fatal(err)
	}
	return string(b)
}

func equalLines(t *testing.T, got, want []string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("output:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// TestCheckPrintsTheFullBuildContract checks the lines the team's release
// skills read off scripts/check.sh (#176).
func TestCheckPrintsTheFullBuildContract(t *testing.T) {
	testgate := buildTestgate(t)
	t.Run("a passing run names every step and the commit", func(t *testing.T) {
		r := newCheckRepo(t, testgate)
		summary := filepath.Join(t.TempDir(), "summary")
		lines, code := r.run("GITHUB_STEP_SUMMARY=" + summary)
		sha := r.git("rev-parse", "HEAD")
		equalLines(t, lines, []string{
			"check: ok   vet",
			"check: ok   format",
			"check: ok   pyramid",
			"check: ok   platform",
			"check: ok   timebound",
			"check: ok   unit",
			"check: ok   integration",
			"check: ok   coverage",
			"check: ok   e2e",
			"check: all 9 steps passed on " + sha,
		})
		if code != 0 {
			t.Fatalf("exit %d", code)
		}
		if b, _ := os.ReadFile(summary); string(b) != "figure pyramid\nfigure coverage\n" {
			t.Errorf("job summary %q, want the two gates' figures", b)
		}
		calls := r.calls()
		for _, want := range []string{
			"go vet ./...", "go vet -tags integration ./...", "go vet -tags e2e ./...",
			"gofmt -l .",
			"go run ./tools/testgate pyramid",
			"go run ./tools/testgate platform",
			"go run ./tools/testgate timebound",
			"go test -json ./...",
			"go test -json -count=1 -tags integration -coverpkg=./internal/... -coverprofile=",
			"go run ./tools/testgate coverage ",
			"go test -json -tags e2e ./e2e/...",
			"go run ./tools/testgate skips unit ",
			"go run ./tools/testgate skips integration ",
			"go run ./tools/testgate skips e2e ",
		} {
			if !strings.Contains(calls, want) {
				t.Errorf("no %q in calls:\n%s", want, calls)
			}
		}
	})

	t.Run("a passing run names each test a test step skipped after its ok line, in the job summary too", func(t *testing.T) {
		r := newCheckRepo(t, testgate)
		summary := filepath.Join(t.TempDir(), "summary")
		lines, code := r.run("GITHUB_STEP_SUMMARY="+summary, "STUB_SKIP=-json")
		sha := r.git("rev-parse", "HEAD")
		skips := func(step string) []string {
			return []string{
				"check: skip " + step + " example.com/p TestSkipped: no tmux here",
				"check: skip " + step + " example.com/p TestParent/sub_case: 4095 of 5000 bytes on two lines",
			}
		}
		want := []string{
			"check: ok   vet",
			"check: ok   format",
			"check: ok   pyramid",
			"check: ok   platform",
			"check: ok   timebound",
			"check: ok   unit",
		}
		want = append(append(want, skips("unit")...), "check: ok   integration")
		want = append(append(want, skips("integration")...), "check: ok   coverage", "check: ok   e2e")
		want = append(append(want, skips("e2e")...), "check: all 9 steps passed on "+sha)
		equalLines(t, lines, want)
		if code != 0 {
			t.Fatalf("exit %d", code)
		}
		block := func(step string) string { return "```text\n" + strings.Join(skips(step), "\n") + "\n```\n" }
		if b, _ := os.ReadFile(summary); string(b) != "figure pyramid\n"+block("unit")+block("integration")+"figure coverage\n"+block("e2e") {
			t.Errorf("job summary:\n%s\nwant the two gates' figures and each test step's skipped tests", b)
		}
	})

	t.Run("a test step that skipped only in one step names its tests after that step alone", func(t *testing.T) {
		r := newCheckRepo(t, testgate)
		lines, code := r.run("STUB_SKIP=-coverprofile")
		if code != 0 || len(lines) != 12 || lines[6] != "check: ok   integration" ||
			lines[7] != "check: skip integration example.com/p TestSkipped: no tmux here" || lines[9] != "check: ok   coverage" {
			t.Fatalf("exit %d, output:\n%s", code, strings.Join(lines, "\n"))
		}
	})

	t.Run("a test step whose skipped tests cannot be listed fails", func(t *testing.T) {
		r := newCheckRepo(t, testgate)
		lines, code := r.run("STUB_TESTGATE=false")
		sha := r.git("rev-parse", "HEAD")
		equalLines(t, lines, []string{
			"check: ok   vet",
			"check: ok   format",
			"check: ok   pyramid",
			"check: ok   platform",
			"check: ok   timebound",
			"check: FAIL unit",
			"the tests passed, but testgate could not list the tests they skipped",
			"check: stopped at unit on " + sha,
		})
		if code != 1 {
			t.Fatalf("exit %d", code)
		}
		if calls := r.calls(); !strings.Contains(calls, "go run ./tools/testgate skips unit ") {
			t.Fatalf("testgate skips was not run:\n%s", calls)
		}
	})

	t.Run("a failing test step prints its output as go test prints it without -json", func(t *testing.T) {
		r := newCheckRepo(t, testgate)
		lines, code := r.run("STUB_FAIL=-coverprofile")
		sha := r.git("rev-parse", "HEAD")
		equalLines(t, lines, []string{
			"check: ok   vet",
			"check: ok   format",
			"check: ok   pyramid",
			"check: ok   platform",
			"check: ok   timebound",
			"check: ok   unit",
			"check: FAIL integration",
			"--- FAIL: TestBroken (0.00s)",
			"    p_test.go:7: broken",
			"FAIL",
			"FAIL\texample.com/p\t0.01s",
			"boom",
			"FAIL",
			"check: stopped at integration on " + sha,
		})
		if code != 1 {
			t.Fatalf("exit %d", code)
		}
		if calls := r.calls(); strings.Contains(calls, "testgate coverage") || strings.Contains(calls, "e2e/...") {
			t.Fatalf("a later step ran:\n%s", calls)
		}
	})

	t.Run("a failing test step's output stays when testgate cannot read it", func(t *testing.T) {
		r := newCheckRepo(t, testgate)
		lines, code := r.run("STUB_FAIL=test -json ./...", "STUB_TESTGATE=false")
		sha := r.git("rev-parse", "HEAD")
		want := []string{
			"check: ok   vet",
			"check: ok   format",
			"check: ok   pyramid",
			"check: ok   platform",
			"check: ok   timebound",
			"check: FAIL unit",
		}
		want = append(want, strings.Split(strings.TrimSuffix(passedEvents+failedEvents, "\n"), "\n")...)
		equalLines(t, lines, append(want, "boom", "check: stopped at unit on "+sha))
		if code != 1 {
			t.Fatalf("exit %d", code)
		}
	})

	t.Run("a failing step stops the run with its output", func(t *testing.T) {
		r := newCheckRepo(t, testgate)
		lines, code := r.run("STUB_FAIL=testgate platform")
		sha := r.git("rev-parse", "HEAD")
		equalLines(t, lines, []string{
			"check: ok   vet",
			"check: ok   format",
			"check: ok   pyramid",
			"check: FAIL platform",
			"figure platform",
			"boom",
			"check: stopped at platform on " + sha,
		})
		if code != 1 {
			t.Fatalf("exit %d", code)
		}
		if calls := r.calls(); strings.Contains(calls, "go test") {
			t.Fatalf("a later step ran:\n%s", calls)
		}
	})

	t.Run("a tight time bound in a test stops the run after the platform gate", func(t *testing.T) {
		r := newCheckRepo(t, testgate)
		lines, code := r.run("STUB_FAIL=testgate timebound")
		sha := r.git("rev-parse", "HEAD")
		equalLines(t, lines, []string{
			"check: ok   vet",
			"check: ok   format",
			"check: ok   pyramid",
			"check: ok   platform",
			"check: FAIL timebound",
			"figure timebound",
			"boom",
			"check: stopped at timebound on " + sha,
		})
		if code != 1 {
			t.Fatalf("exit %d", code)
		}
		if calls := r.calls(); strings.Contains(calls, "go test") {
			t.Fatalf("a later step ran:\n%s", calls)
		}
	})

	t.Run("a dirty tree is named in the passing and in the failing last line", func(t *testing.T) {
		r := newCheckRepo(t, testgate)
		writeFile(t, filepath.Join(r.dir, "new.txt"), "x\n", 0o644)
		sha := r.git("rev-parse", "HEAD")

		lines, code := r.run()
		if last := lines[len(lines)-1]; code != 0 || last != "check: all 9 steps passed on "+sha+" with uncommitted changes" {
			t.Fatalf("exit %d, last line %q", code, last)
		}

		lines, code = r.run("STUB_UNFORMATTED=bad.go")
		equalLines(t, lines, []string{
			"check: ok   vet",
			"check: FAIL format",
			"bad.go",
			"check: stopped at format on " + sha + " with uncommitted changes",
		})
		if code != 1 {
			t.Fatalf("exit %d", code)
		}
	})
}

// The steps run with a temporary directory of the checkout's own, outside
// it, so a test run leaves no file loose in the shared one and one
// checkout's cleanup never meets another checkout's run (#226).
func TestCheckKeepsTheTemporaryFilesOfARunInADirectoryOfTheCheckoutsOwn(t *testing.T) {
	base := t.TempDir()
	// tmpdir runs the script with TMPDIR set to tmp and returns the one
	// TMPDIR every step ran with.
	tmpdir := func(r *checkRepo, tmp string) string {
		t.Helper()
		if err := os.Truncate(r.log, 0); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		lines, code := r.run("TMPDIR=" + tmp)
		if last := lines[len(lines)-1]; code != 0 || strings.HasSuffix(last, "with uncommitted changes") {
			t.Fatalf("exit %d, last line %q", code, last)
		}
		var dirs []string
		for _, line := range strings.Split(r.calls(), "\n") {
			if dir, ok := strings.CutPrefix(line, "TMPDIR="); ok && !slices.Contains(dirs, dir) {
				dirs = append(dirs, dir)
			}
		}
		if len(dirs) != 1 {
			t.Fatalf("the steps ran with TMPDIR %q, want one directory", dirs)
		}
		if info, err := os.Stat(dirs[0]); err != nil || !info.IsDir() {
			t.Fatalf("TMPDIR %s is no directory: %v", dirs[0], err)
		}
		return dirs[0]
	}

	testgate := buildTestgate(t)
	a, b := newCheckRepo(t, testgate), newCheckRepo(t, testgate)
	dir := tmpdir(a, base)
	if filepath.Dir(dir) != base || !strings.HasPrefix(filepath.Base(dir), "hq-check-") {
		t.Fatalf("TMPDIR %s, want a directory hq-check-* in the caller's %s", dir, base)
	}
	if again := tmpdir(a, base+"/"); again != dir {
		t.Errorf("the checkout's next run had TMPDIR %s, want its own %s again", again, dir)
	}
	if within := tmpdir(a, dir); within != dir {
		t.Errorf("a run from the TMPDIR it exports had %s, want %s again", within, dir)
	}
	if other := tmpdir(b, base); other == dir || filepath.Dir(other) != base {
		t.Errorf("another checkout had TMPDIR %s beside %s, want one of its own in %s", other, dir, base)
	}
	shared := tmpdir(a, "")
	t.Cleanup(func() { _ = os.Remove(shared) })
	if filepath.Dir(shared) != "/tmp" || filepath.Base(shared) != filepath.Base(dir) {
		t.Errorf("without a TMPDIR the run had %s, want /tmp/%s", shared, filepath.Base(dir))
	}
}
