package main

import (
	"os"
	"reflect"
	"slices"
	"testing"
)

func TestCoverArgsBuildWithCoverageOnlyForBuildAndRun(t *testing.T) {
	flags := []string{"-cover", "-covermode=set", "-coverpkg=example.com/m/..."}
	for _, c := range []struct {
		args, want []string
	}{
		{[]string{"build", "-o", "/t/hq", "./cmd/hq"}, slices.Concat([]string{"build"}, flags, []string{"-o", "/t/hq", "./cmd/hq"})},
		{[]string{"run", "./tools/testgate", "skips"}, slices.Concat([]string{"run"}, flags, []string{"./tools/testgate", "skips"})},
		{[]string{"test", "-count=1", "./..."}, []string{"test", "-count=1", "./..."}},
		{[]string{"build", "-overlay", "/t/o.json", "-o", "/t/hq", "."}, []string{"build", "-overlay", "/t/o.json", "-o", "/t/hq", "."}},
		{[]string{"build", "-overlay=/t/o.json", "."}, []string{"build", "-overlay=/t/o.json", "."}},
		{[]string{"env", "GOCACHE"}, []string{"env", "GOCACHE"}},
		{nil, nil},
	} {
		if got := coverArgs(c.args, "example.com/m"); !reflect.DeepEqual(got, c.want) {
			t.Errorf("coverArgs(%q) = %q, want %q", c.args, got, c.want)
		}
	}
}

func TestTestExecFlagQuotesEachPath(t *testing.T) {
	got := testExecFlag("/t/test gate", "/t/c", "/t/it's")
	want := `'/t/test gate' ` + roleTestExec + ` '/t/c' '/t/it'\''s'`
	if got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestWithPathPutsADirectoryFirst(t *testing.T) {
	got := withPath([]string{"HOME=/h", "PATH=/usr/bin:/bin", "X=1"}, "/t/cover-go")
	if want := []string{"HOME=/h", "X=1", "PATH=/t/cover-go:/usr/bin:/bin"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
	if got := withPath([]string{"HOME=/h"}, "/t/cover-go"); !reflect.DeepEqual(got, []string{"HOME=/h", "PATH=/t/cover-go"}) {
		t.Errorf("without PATH: got %q", got)
	}
}

func TestLevelArgsRunTheLevelWithCoverageThroughTheTestExec(t *testing.T) {
	setup := coverSetup{self: "/t/gate", bin: "/t/run/cover-go", module: "example.com/m"}
	got := levelArgs(levelIntegration, setup, coverDir("/t/run/level"))
	want := []string{"test", "-json", "-count=1", "-cover", "-covermode=set", "-coverpkg=./...",
		"-coverprofile=/t/run/level/profile", "-exec", "'/t/gate' " + roleTestExec + " '/t/run/level/counters' '/t/run/cover-go'",
		"-tags", "integration", "./..."}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q\nwant %q", got, want)
	}
	if !slices.Equal(levelE2E.args, []string{"-tags", "e2e", "./e2e/..."}) || levelE2E.dir != "e2e" || levelIntegration.dir != "." {
		t.Errorf("the levels are not the full build's: %v, %v", levelIntegration, levelE2E)
	}
}

func TestLevelEnvKeepsTheLevelsTestsAwayFromTheUsersTmux(t *testing.T) {
	got := levelEnv([]string{"PATH=/usr/bin", "TMUX=/tmp/tmux-1000/default,1,0", "TMUX_PANE=%1",
		"TMUX_TMPDIR=/home/u/t", "GOCOVERDIR=/home/u/cov", "HOME=/h"}, "/t/run")
	want := []string{"PATH=/usr/bin", "HOME=/h",
		"GOCACHE=/t/run/cache", "GOBIN=/t/run/bin", "TMPDIR=/t/run/tmp", "GOFLAGS=", "NO_COLOR=1", "TMUX_TMPDIR=/t/run"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestFailedOutputLeavesOutThePackagesThatPassed(t *testing.T) {
	out := `{"Action":"start","Package":"example.com/m/cmd/prog"}
{"Action":"output","Package":"example.com/m/cmd/prog","Output":"\texample.com/m/cmd/prog\t\tcoverage: 0.0% of statements\n"}
{"Action":"skip","Package":"example.com/m/cmd/prog"}
{"Action":"start","Package":"example.com/m/user"}
{"Action":"run","Package":"example.com/m/user","Test":"TestDouble"}
{"Action":"pass","Package":"example.com/m/user","Test":"TestDouble"}
{"Action":"output","Package":"example.com/m/user","Output":"ok  \texample.com/m/user\t0.01s\tcoverage: 50.0% of statements in ./...\n"}
{"Action":"pass","Package":"example.com/m/user"}
{"Action":"start","Package":"example.com/m/lib"}
{"Action":"run","Package":"example.com/m/lib","Test":"TestFails"}
{"Action":"output","Package":"example.com/m/lib","Test":"TestFails","Output":"=== RUN   TestFails\n"}
{"Action":"output","Package":"example.com/m/lib","Test":"TestFails","Output":"    fail_test.go:5: it fails\n"}
{"Action":"output","Package":"example.com/m/lib","Test":"TestFails","Output":"--- FAIL: TestFails (0.00s)\n"}
{"Action":"fail","Package":"example.com/m/lib","Test":"TestFails"}
{"Action":"output","Package":"example.com/m/lib","Output":"FAIL\texample.com/m/lib\t0.01s\n"}
{"Action":"fail","Package":"example.com/m/lib"}
`
	got := failedOutput([]byte(out))
	want := "--- FAIL: TestFails (0.00s)\n    fail_test.go:5: it fails\nFAIL\texample.com/m/lib\t0.01s\nFAIL"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	if got := failedOutput([]byte("# example.com/m/lib\nlib/lib.go:3:1: syntax error\n")); got != "# example.com/m/lib\nlib/lib.go:3:1: syntax error" {
		t.Errorf("lines that are no events: got %q, want them as they are", got)
	}
}

func TestToolCoverageRunIsTheToolsFirstRun(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	d := coverDir("/t/run/cover")
	args := []string{"test", "-coverpkg", "./...", "-cover", "-coverprofile", "/t/g/coverage", "./..."}
	got, profile, ok := toolCoverageRun(args, d, "/t/run/cover-go")
	want := []string{"test", "-exec", testExecFlag(self, "/t/run/cover/counters", "/t/run/cover-go"),
		"-coverpkg", "./...", "-cover", "-coverprofile", "/t/g/coverage", "./..."}
	if !ok || profile != "/t/g/coverage" || !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, %q, %v\nwant %q, /t/g/coverage", got, profile, ok, want)
	}
	if got, profile, ok := toolCoverageRun([]string{"test", "-coverprofile", "/t/c", "./..."}, d, "/t/b"); !ok || profile != "/t/c" ||
		!slices.Equal(got[3:], []string{"-coverprofile", "/t/c", "./..."}) {
		t.Errorf("-coverprofile right after test: got %q, %q, %v", got, profile, ok)
	}
	for _, args := range [][]string{
		{"test", "-timeout", "32s", "-failfast", "./..."},
		{"test", "./...", "-coverprofile"},
		{"build", "-coverprofile", "x"},
		nil,
	} {
		if _, _, ok := toolCoverageRun(args, d, "/t/run/cover-go"); ok {
			t.Errorf("%q is taken for the tool's first run", args)
		}
	}
}

func TestModuleOfReadsGoMod(t *testing.T) {
	for _, c := range []struct {
		gomod, want string
	}{
		{"module example.com/m\n\ngo 1.26\n", "example.com/m"},
		{"// the module\nmodule\t\"github.com/o/r\"\n", "github.com/o/r"},
		{"  module example.com/x // the comment goes\n", "example.com/x"},
	} {
		if got, err := moduleOf(c.gomod); err != nil || got != c.want {
			t.Errorf("%q: got %q, %v; want %q", c.gomod, got, err, c.want)
		}
	}
	for _, gomod := range []string{"go 1.26\n", "modules example.com/m\n", "module\n"} {
		if got, err := moduleOf(gomod); err == nil {
			t.Errorf("%q: got %q, want an error", gomod, got)
		}
	}
}

func TestDescendantsAreEveryProcessBelowOne(t *testing.T) {
	ps := "    1     0\n  100     1\n  101   100\n  102   101\n  103   100\n  200     1\n  201   200\n garbage\n\n"
	if got := descendants(ps, 100); !slices.Equal(got, []int{101, 103, 102}) {
		t.Errorf("got %v, want 101, 103, 102", got)
	}
	if got := descendants(ps, 102); len(got) != 0 {
		t.Errorf("a process with none below it: got %v", got)
	}
}

func TestRunRoleKnowsItsRolesByTheirArguments(t *testing.T) {
	if code, ok := runRole([]string{"/t/gate", roleCoverGo}); !ok || code != 2 {
		t.Errorf("the run's go without its arguments: %d, %v; want 2, a role", code, ok)
	}
	if code, ok := runRole([]string{"/t/gate", roleTestExec, "/t/c"}); !ok || code != 2 {
		t.Errorf("the test exec without its arguments: %d, %v; want 2, a role", code, ok)
	}
	for _, args := range [][]string{{"/t/gate", "mutation", "origin/dev"}, {"/t/gate"}, nil} {
		if _, ok := runRole(args); ok {
			t.Errorf("%q: started in a role", args)
		}
	}
}

func TestRoleArgsSplitWhatARoleIsGivenFromWhatItPassesOn(t *testing.T) {
	given, rest, ok := roleArgs([]string{"/usr/bin/go", "/t/src", "example.com/m"}, 3)
	if !ok || !slices.Equal(given, []string{"/usr/bin/go", "/t/src", "example.com/m"}) || len(rest) != 0 {
		t.Errorf("exactly three: got %q, %q, %v", given, rest, ok)
	}
	given, rest, ok = roleArgs([]string{"/t/c", "/t/bin", "/t/x.test", "-test.v"}, 3)
	if !ok || !slices.Equal(given, []string{"/t/c", "/t/bin", "/t/x.test"}) || !slices.Equal(rest, []string{"-test.v"}) {
		t.Errorf("four: got %q, %q, %v", given, rest, ok)
	}
	if _, _, ok := roleArgs([]string{"/t/c", "/t/bin"}, 3); ok {
		t.Error("two of three are taken for enough")
	}
}

func TestCovdataWritesTheProgramsProfileToAFileOfTheRun(t *testing.T) {
	got := coverDir("/t/run/level").covdataArgs()
	want := []string{"tool", "covdata", "textfmt", "-i=/t/run/level/counters", "-o=/t/run/level/programs"}
	if !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}
