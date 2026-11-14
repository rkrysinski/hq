//go:build integration

package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// stubGo stands in for the toolchain: it logs each call, prints a gate's
// figure as "figure <gate>", and fails with "boom" when the call contains
// STUB_FAIL. stubGofmt names an unformatted file when STUB_UNFORMATTED is set.
const stubGo = `#!/bin/sh
echo "go $*" >> "$STUB_LOG"
case "$*" in "run ./tools/testgate "*) echo "figure $3" ;; esac
case "$*" in *"$STUB_FAIL"*) [ -n "$STUB_FAIL" ] && { echo boom; exit 1; } ;; esac
exit 0
`

const stubGofmt = `#!/bin/sh
echo "gofmt $*" >> "$STUB_LOG"
[ -n "$STUB_UNFORMATTED" ] && echo "$STUB_UNFORMATTED"
exit 0
`

// checkRepo is a git repository holding a copy of scripts/check.sh, with the
// stub toolchain first on PATH.
type checkRepo struct {
	t   *testing.T
	dir string
	log string
	env []string
}

func newCheckRepo(t *testing.T) *checkRepo {
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
	writeFile(t, filepath.Join(r.dir, "scripts", "check.sh"), string(script), 0o755)
	writeFile(t, filepath.Join(r.dir, "e2e", "journey_test.go"), "package e2e\n", 0o644)
	r.env = append(os.Environ(),
		"PATH="+stubs+string(os.PathListSeparator)+os.Getenv("PATH"),
		"STUB_LOG="+r.log, "STUB_FAIL=", "STUB_UNFORMATTED=", "GITHUB_STEP_SUMMARY=")
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
	t.Run("a passing run names every step and the commit", func(t *testing.T) {
		r := newCheckRepo(t)
		summary := filepath.Join(t.TempDir(), "summary")
		lines, code := r.run("GITHUB_STEP_SUMMARY=" + summary)
		sha := r.git("rev-parse", "HEAD")
		equalLines(t, lines, []string{
			"check: ok   vet",
			"check: ok   format",
			"check: ok   pyramid",
			"check: ok   platform",
			"check: ok   unit",
			"check: ok   integration",
			"check: ok   coverage",
			"check: ok   e2e",
			"check: all 8 steps passed on " + sha,
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
			"go test ./...",
			"go test -count=1 -tags integration -coverpkg=./internal/... -coverprofile=",
			"go run ./tools/testgate coverage ",
			"go test -tags e2e ./e2e/...",
		} {
			if !strings.Contains(calls, want) {
				t.Errorf("no %q in calls:\n%s", want, calls)
			}
		}
	})

	t.Run("a failing step stops the run with its output", func(t *testing.T) {
		r := newCheckRepo(t)
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

	t.Run("a dirty tree is named in the passing and in the failing last line", func(t *testing.T) {
		r := newCheckRepo(t)
		writeFile(t, filepath.Join(r.dir, "new.txt"), "x\n", 0o644)
		sha := r.git("rev-parse", "HEAD")

		lines, code := r.run()
		if last := lines[len(lines)-1]; code != 0 || last != "check: all 8 steps passed on "+sha+" with uncommitted changes" {
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
