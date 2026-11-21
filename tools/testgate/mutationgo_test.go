package main

import (
	"os"
	"reflect"
	"testing"
	"time"
)

// TestMain lets this package's test binary take the roles a mutation run
// gives the gate's binary (runRole): the integration tests that run the
// tool link the test binary as the go of the run. With $TESTGATE_LOAD_HELPER set it is
// testgate load with one busy worker (loadHelper).
func TestMain(m *testing.M) {
	if code, ok := runRole(os.Args); ok {
		os.Exit(code)
	}
	if os.Getenv(envLoadHelper) != "" {
		os.Exit(loadHelper(os.Args[1:]))
	}
	os.Exit(m.Run())
}

func TestRerunArgs(t *testing.T) {
	for _, c := range []struct {
		name, out string
		want      [][]string
	}{
		{"the top-level tests that failed, once each, of each package that failed",
			"--- FAIL: TestBig (0.00s)\n    --- FAIL: TestBig/small (0.00s)\n        main_test.go:7: big\n" +
				"--- FAIL: TestOther (0.00s)\n--- FAIL: TestBig (0.00s)\nFAIL\nFAIL\texample.com/m/cmd/tool\t0.002s\n" +
				"ok  \texample.com/m/lib\t0.001s\n--- FAIL: TestDouble (0.01s)\nFAIL\texample.com/m/user\t0.010s\nFAIL\n",
			[][]string{
				{"test", "-count=1", "-run", "^(TestBig|TestOther)$", "example.com/m/cmd/tool"},
				{"test", "-count=1", "-run", "^(TestDouble)$", "example.com/m/user"},
			}},
		{"a package that did not build: its build",
			"# example.com/m/lib\nlib/lib.go:4:30: invalid operation\nFAIL\texample.com/m/lib [build failed]\nFAIL\n",
			[][]string{{"test", "-count=1", "-run", "^$", "example.com/m/lib"}}},
		{"a package that failed with no test named: all its tests",
			"panic: init\nFAIL\texample.com/m/lib\t0.001s\nFAIL\n",
			[][]string{{"test", "-count=1", "example.com/m/lib"}}},
		{"a failed test line with no name, as no test",
			"--- FAIL: \nFAIL\texample.com/m/lib\t0.001s\n",
			[][]string{{"test", "-count=1", "example.com/m/lib"}}},
		{"no package named: the whole unit suite",
			"go: writing go.sum: no space left on device\n",
			[][]string{{"test", "-count=1", "./..."}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := rerunArgs(c.out); !reflect.DeepEqual(got, c.want) {
				t.Errorf("got %q\nwant %q", got, c.want)
			}
		})
	}
}

func TestRerunDeadlineEndsAMarginBeforeTheMutantsTimeLimit(t *testing.T) {
	start := time.Date(2026, 11, 17, 15, 0, 0, 0, time.UTC)
	got, ok := rerunDeadline([]string{"test", "-timeout", "32s", "-failfast", "./..."}, start)
	// The tool gives go test its limit of 30 s plus 2 s; the margin is 2 s.
	if want := start.Add(28 * time.Second); !ok || !got.Equal(want) {
		t.Errorf("got %v, %v; want %v", got, ok, want)
	}
	if got, ok := rerunDeadline([]string{"-timeout", "4s"}, start); !ok || !got.Equal(start) {
		t.Errorf("-timeout first: got %v, %v; want %v", got, ok, start)
	}
	for _, args := range [][]string{
		{"test", "-failfast", "./..."},
		{"test", "-failfast", "./...", "-timeout"},
		{"test", "-timeout", "soon", "-failfast", "./..."},
	} {
		if got, ok := rerunDeadline(args, start); ok {
			t.Errorf("%q: got %v; want none", args, got)
		}
	}
}

func TestIsMutantRunTellsAMutantsRunFromTheToolsOtherCalls(t *testing.T) {
	for _, c := range []struct {
		args []string
		want bool
	}{
		{[]string{"test", "-timeout", "32s", "-failfast", "./..."}, true},
		{[]string{"test", "-coverpkg", "./...", "-cover", "-coverprofile", "/t/c.out", "./..."}, false},
		{[]string{"list", "-failfast"}, false},
		{[]string{"env", "GOMOD"}, false},
		{nil, false},
	} {
		if got := isMutantRun(c.args); got != c.want {
			t.Errorf("%q: got %v, want %v", c.args, got, c.want)
		}
	}
}

func TestIsMutationGoNeedsTheNameGoAndTheRealGo(t *testing.T) {
	for _, c := range []struct {
		arg0, realGo string
		want         bool
	}{
		{"/t/run/mutation-go/go", "/usr/bin/go", true},
		{"go", "/usr/bin/go", true},
		{"/t/run/mutation-go/go", "", false},
		{"/tmp/go-build1/b001/testgate.test", "/usr/bin/go", false},
	} {
		if got := isMutationGo(c.arg0, c.realGo); got != c.want {
			t.Errorf("%q with %q: got %v, want %v", c.arg0, c.realGo, got, c.want)
		}
	}
}

func TestExitCodeOfACommandThatSucceeded(t *testing.T) {
	if got := exitCode(nil); got != 0 {
		t.Errorf("got %d, want 0", got)
	}
}

func TestMutationEnvDropsAnOuterRunsGo(t *testing.T) {
	env := mutationEnv([]string{"PATH=/usr/bin", envRealGo + "=/usr/bin/go", envSource + "=/t/src",
		envMachine + "=/t/machine"}, "/t/run")
	want := []string{"PATH=/usr/bin",
		"GOCACHE=/t/run/cache", "GOBIN=/t/run/bin", "TMPDIR=/t/run/tmp", "GOFLAGS=", "NO_COLOR=1"}
	if !reflect.DeepEqual(env, want) {
		t.Errorf("got %q\nwant %q", env, want)
	}
}
