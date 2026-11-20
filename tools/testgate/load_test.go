package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestTopLevelPatternIsTheRunPatternBeforeItsFirstSlash(t *testing.T) {
	for run, want := range map[string]string{
		"TestX":                  "TestX",
		"^TestX$":                "^TestX$",
		"TestX/sub":              "TestX",
		"TestX/sub/deeper":       "TestX",
		"Test[/]X/sub":           "Test[/]X",
		"Test(a/b)X/sub":         "Test(a/b)X",
		"Test((a)/b)/sub":        "Test((a)/b)",
		`Test\/X/sub`:            `Test\/X`,
		"Test[a]X/sub":           "Test[a]X",
		"TestA|TestB":            "TestA|TestB",
		"/sub":                   "",
		"Test[^/]X/s":            "Test[^/]X",
		"Test(x)[/]/s":           "Test(x)[/]",
		"Test[(]/s":              "Test[(]",
		"Test[)]x/s":             "Test[)]x",
		"Test(a)b/s(c/d)":        "Test(a)b",
		`Test\\/sub`:             `Test\\`,
		"Test(\\)/x)/s":          "Test(\\)/x)",
		"TestNoSlash[unclosed/x": "TestNoSlash[unclosed/x",
	} {
		if got := topLevelPattern(run); got != want {
			t.Errorf("%q: got %q, want %q", run, got, want)
		}
	}
}

func TestListedTestsAreTheTestsFuzzTestsAndExamplesGoTestLists(t *testing.T) {
	out := "TestPasses\nTestFails\nBenchmarkX\nFuzzY\nExampleZ\nok  \texample.com/x\t0.001s\n"
	if got, want := listedTests(out), []string{"TestPasses", "TestFails", "FuzzY", "ExampleZ"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
	if got := listedTests("ok  \texample.com/x\t0.001s\n"); len(got) != 0 {
		t.Errorf("no test listed: got %q", got)
	}
	if got := listedTests("testing: warning: no tests to run\nTest with space\nTester\n"); !reflect.DeepEqual(got, []string{"Tester"}) {
		t.Errorf("got %q, want only the name with no space", got)
	}
}

func TestTheGoTestCommandsOfALoadRun(t *testing.T) {
	o := loadOptions{procs: 3, timeout: 2 * time.Minute, count: 150, run: "^TestX$/sub"}
	if got, want := goTestArgs(o, "./pkg"), []string{"test", "-json", "-count", "150", "-run", "^TestX$/sub", "-timeout", "3m0s", "./pkg"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
	if got, want := listArgs(o, "./pkg"), []string{"test", "-list", "^TestX$", "./pkg"}; !reflect.DeepEqual(got, want) {
		t.Errorf("list: got %q, want %q", got, want)
	}
	o.tags = "integration"
	if got, want := goTestArgs(o, "./pkg"), []string{"test", "-json", "-count", "150", "-run", "^TestX$/sub", "-tags", "integration", "-timeout", "3m0s", "./pkg"}; !reflect.DeepEqual(got, want) {
		t.Errorf("with tags: got %q, want %q", got, want)
	}
	if got, want := listArgs(o, "./pkg"), []string{"test", "-list", "^TestX$", "-tags", "integration", "./pkg"}; !reflect.DeepEqual(got, want) {
		t.Errorf("list with tags: got %q, want %q", got, want)
	}
}

func TestShellLineQuotesWhatTheShellWouldReadOtherwise(t *testing.T) {
	got := shellLine([]string{"go", "test", "-run", "^TestX$|TestY", "-tags", "", "./pkg/a-b_c", "it's", "k=v,w:+1@x%"})
	if want := `go test -run '^TestX$|TestY' -tags '' ./pkg/a-b_c 'it'\''s' k=v,w:+1@x%`; got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestLoadAverageIsReadFromLinuxAndMacOSAlike(t *testing.T) {
	if v, err := procLoadavg("0.52 0.58 0.59 2/1234 5678\n"); err != nil || v != 0.52 {
		t.Errorf("/proc/loadavg: got %v, %v", v, err)
	}
	if v, err := sysctlLoadavg("{ 1.25 1.10 1.05 }\n"); err != nil || v != 1.25 {
		t.Errorf("sysctl: got %v, %v", v, err)
	}
	for _, bad := range []string{"", "x 1 2", "{ }", "{ x }"} {
		if _, err := procLoadavg(bad); err == nil {
			t.Errorf("/proc/loadavg %q: no error", bad)
		}
		if _, err := sysctlLoadavg(bad); err == nil {
			t.Errorf("sysctl %q: no error", bad)
		}
	}
	if _, err := sysctlLoadavg("1.25 1.10 1.05"); err == nil {
		t.Error("sysctl without its braces: no error")
	}
	if got := loadAverageText(10.314, nil); got != "load average (1 min) 10.31" {
		t.Errorf("got %q", got)
	}
	if got := loadAverageText(0, errors.New("no /proc")); got != "no load average (no /proc)" {
		t.Errorf("got %q", got)
	}
}

// loadFixtureRun is the go test -json output of one process of a load run
// with -count 2 over a test that passes, one that skips, one that fails and
// one whose process ended in its second run.
var loadFixtureRun = []string{
	`{"Action":"start","Package":"example.com/f"}`,
	`{"Action":"run","Package":"example.com/f","Test":"TestPasses"}`,
	`{"Action":"output","Package":"example.com/f","Test":"TestPasses","Output":"=== RUN   TestPasses\n"}`,
	`{"Action":"output","Package":"example.com/f","Test":"TestPasses","Output":"--- PASS: TestPasses (0.00s)\n"}`,
	`{"Action":"pass","Package":"example.com/f","Test":"TestPasses"}`,
	`{"Action":"run","Package":"example.com/f","Test":"TestSkips"}`,
	`{"Action":"output","Package":"example.com/f","Test":"TestSkips","Output":"=== RUN   TestSkips\n"}`,
	`{"Action":"output","Package":"example.com/f","Test":"TestSkips","Output":"    f_test.go:7: no tmux here\n"}`,
	`{"Action":"output","Package":"example.com/f","Test":"TestSkips","Output":"--- SKIP: TestSkips (0.00s)\n"}`,
	`{"Action":"skip","Package":"example.com/f","Test":"TestSkips"}`,
	`{"Action":"run","Package":"example.com/f","Test":"TestFails"}`,
	`{"Action":"output","Package":"example.com/f","Test":"TestFails","Output":"=== RUN   TestFails\n"}`,
	`{"Action":"run","Package":"example.com/f","Test":"TestFails/sub"}`,
	`{"Action":"output","Package":"example.com/f","Test":"TestFails/sub","Output":"=== RUN   TestFails/sub\n"}`,
	`{"Action":"output","Package":"example.com/f","Test":"TestFails/sub","Output":"--- SKIP: TestFails/sub (0.00s)\n"}`,
	`{"Action":"skip","Package":"example.com/f","Test":"TestFails/sub"}`,
	`{"Action":"output","Package":"example.com/f","Test":"TestFails","Output":"    f_test.go:9: boom\n"}`,
	`{"Action":"output","Package":"example.com/f","Test":"TestFails","Output":"--- FAIL: TestFails (0.00s)\n"}`,
	`{"Action":"fail","Package":"example.com/f","Test":"TestFails"}`,
	`{"Action":"run","Package":"example.com/f","Test":"TestPasses"}`,
	`{"Action":"output","Package":"example.com/f","Test":"TestPasses","Output":"=== RUN   TestPasses\n"}`,
	`{"Action":"output","Package":"example.com/f","Test":"TestPasses","Output":"--- PASS: TestPasses (0.00s)\n"}`,
	`{"Action":"pass","Package":"example.com/f","Test":"TestPasses"}`,
	`{"Action":"run","Package":"example.com/f","Test":"TestExits"}`,
	`{"Action":"output","Package":"example.com/f","Test":"TestExits","Output":"=== RUN   TestExits\n"}`,
	`{"Action":"output","Package":"example.com/f","Test":"TestExits","Output":"    f_test.go:12: about to exit\n"}`,
	`{"Action":"output","Package":"example.com/f","Output":"FAIL\texample.com/f\t0.003s\n"}`,
	`{"Action":"fail","Package":"example.com/f"}`,
}

func readRun(t *testing.T, lines ...string) *testRun {
	t.Helper()
	run, err := readTestRun(jsonLines(lines...))
	if err != nil {
		t.Fatal(err)
	}
	return run
}

// A run counts by its top-level test: a subtest that skipped in a run that
// failed is no skipped run, and a run its process ended in counts as failed,
// with what it printed.
func TestTallyCountsTheRunsOfTopLevelTestsAndShowsEachThatDidNotPass(t *testing.T) {
	c, shown := tally(2, readRun(t, loadFixtureRun...))
	if want := (runCounts{passed: 2, failed: 2, skipped: 1}); c != want {
		t.Errorf("got %+v, want %+v", c, want)
	}
	equalStrings(t, shown, []string{
		"process 2, TestSkips, run 1: skipped: no tmux here",
		"process 2, TestFails, run 1: failed",
		"    --- FAIL: TestFails (0.00s)",
		"        f_test.go:9: boom",
		"process 2, TestExits, run 1: never ended",
		"        f_test.go:12: about to exit",
	})
}

func TestTallyNumbersTheRunsOfEachTest(t *testing.T) {
	fail := func(n string) []string {
		return []string{
			`{"Action":"run","Package":"example.com/f","Test":"TestFails"}`,
			`{"Action":"output","Package":"example.com/f","Test":"TestFails","Output":"    f_test.go:9: boom ` + n + `\n"}`,
			`{"Action":"output","Package":"example.com/f","Test":"TestFails","Output":"--- FAIL: TestFails (0.00s)\n"}`,
			`{"Action":"fail","Package":"example.com/f","Test":"TestFails"}`,
		}
	}
	lines := append(append(append([]string{`{"Action":"start","Package":"example.com/f"}`}, fail("one")...),
		`{"Action":"run","Package":"example.com/f","Test":"TestPasses"}`,
		`{"Action":"pass","Package":"example.com/f","Test":"TestPasses"}`), fail("two")...)
	c, shown := tally(1, readRun(t, lines...))
	if want := (runCounts{passed: 1, failed: 2}); c != want {
		t.Errorf("got %+v, want %+v", c, want)
	}
	equalStrings(t, shown, []string{
		"process 1, TestFails, run 1: failed",
		"    --- FAIL: TestFails (0.00s)",
		"        f_test.go:9: boom one",
		"process 1, TestFails, run 2: failed",
		"    --- FAIL: TestFails (0.00s)",
		"        f_test.go:9: boom two",
	})
}

func evidenceRun() loadRun {
	return loadRun{
		head: "0123456789abcdef0123456789abcdef01234567", cores: 10, workers: 10,
		command: []string{"go", "test", "-json", "-count", "2", "-run", "^TestX$", "-timeout", "11m0s", "./pkg"},
		procs:   2, timeout: 10 * time.Minute, dir: "/tmp/load-1",
		start:     time.Date(2026, 11, 20, 16, 0, 0, 0, time.FixedZone("CET", 3600)),
		loadStart: "load average (1 min) 0.52",
	}
}

func TestTheHeadOfTheEvidenceSaysWhatRunsWhereAndWhen(t *testing.T) {
	r := evidenceRun()
	equalStrings(t, r.headLines(), []string{
		"commit 0123456789abcdef0123456789abcdef01234567",
		"cores 10, busy workers 10",
		"processes 2, each: go test -json -count 2 -run '^TestX$' -timeout 11m0s ./pkg",
		"time limit 10m0s (the default)",
		"output in /tmp/load-1",
		"started 2026-11-20T15:00:00Z, load average (1 min) 0.52",
	})
	r.timeout, r.head = 3*time.Minute, r.head+" with uncommitted changes"
	got := r.headLines()
	if got[0] != "commit 0123456789abcdef0123456789abcdef01234567 with uncommitted changes" || got[3] != "time limit 3m0s (-timeout)" {
		t.Errorf("got %q", got)
	}
}

func passingProcess(file string) loadProcess {
	return loadProcess{status: "exit status 0", file: file, run: &testRun{packages: map[string]*goPackage{}}}
}

// processWith is a process whose run ran the tests with these outcomes.
func processWith(t *testing.T, status string, outcomes ...string) loadProcess {
	t.Helper()
	lines := []string{`{"Action":"start","Package":"example.com/f"}`}
	for _, o := range outcomes {
		lines = append(lines, `{"Action":"run","Package":"example.com/f","Test":"TestX"}`)
		if o != "" {
			lines = append(lines, `{"Action":"`+o+`","Package":"example.com/f","Test":"TestX"}`)
		}
	}
	lines = append(lines, `{"Action":"output","Package":"example.com/f","Output":"ok  \texample.com/f\t0.010s\n"}`,
		`{"Action":"pass","Package":"example.com/f"}`)
	return loadProcess{status: status, file: "/tmp/load-1/process-1.out", run: readRun(t, lines...)}
}

func TestTheEndOfTheEvidenceGivesEachProcessTheRunsThatDidNotPassAndTheCounts(t *testing.T) {
	r := evidenceRun()
	r.end = time.Date(2026, 11, 20, 15, 0, 40, 0, time.UTC)
	r.loadEnd = "load average (1 min) 10.31"
	procs := []loadProcess{
		processWith(t, "exit status 0", "pass", "pass"),
		{status: "exit status 1", file: "/tmp/load-1/process-2.out", run: readRun(t, loadFixtureRun...)},
	}
	procs[0].file = "/tmp/load-1/process-1.out"
	lines, ok := r.endLines(procs, 4, false)
	if ok {
		t.Error("a failed run: ok")
	}
	equalStrings(t, lines, []string{
		"ended 2026-11-20T15:00:40Z, load average (1 min) 10.31",
		"process 1: exit status 0, output /tmp/load-1/process-1.out, last lines:",
		"    ok  \texample.com/f\t0.010s",
		"process 2: exit status 1, output /tmp/load-1/process-2.out, last lines:",
		"    --- FAIL: TestFails (0.00s)",
		"        f_test.go:9: boom",
		"        f_test.go:12: about to exit",
		"    FAIL\texample.com/f\t0.003s",
		"    FAIL",
		"process 2, TestSkips, run 1: skipped: no tmux here",
		"process 2, TestFails, run 1: failed",
		"    --- FAIL: TestFails (0.00s)",
		"        f_test.go:9: boom",
		"process 2, TestExits, run 1: never ended",
		"        f_test.go:12: about to exit",
		"load: 4 passed, 2 failed, 1 skipped of 4 runs expected on 0123456789abcdef0123456789abcdef01234567",
	})
}

func TestTheLastLinesOfAProcessAreItsLastFiveAsGoTestPrintsThem(t *testing.T) {
	lines := []string{`{"Action":"start","Package":"example.com/f"}`}
	for _, l := range []string{"one", "two", "three", "four", "five", "six"} {
		lines = append(lines, `{"Action":"output","Package":"example.com/f","Output":"`+l+`\n"}`)
	}
	lines = append(lines, `{"Action":"fail","Package":"example.com/f"}`)
	p := loadProcess{status: "exit status 1", file: "f", run: readRun(t, lines...)}
	got, _ := evidenceRun().endLines([]loadProcess{p}, 1, false)
	equalStrings(t, got[1:7], []string{
		"process 1: exit status 1, output f, last lines:",
		"    three", "    four", "    five", "    six", "    FAIL",
	})
	got, _ = evidenceRun().endLines([]loadProcess{passingProcess("f")}, 1, false)
	if got[1] != "process 1: exit status 0, output f, no output" {
		t.Errorf("a process with no output: got %q", got[1])
	}
}

// The verdict passes only when every expected run passed in processes that
// all exited 0; a stopped run gives no line of counts.
func TestTheVerdictOfALoadRun(t *testing.T) {
	for _, c := range []struct {
		name     string
		procs    func(t *testing.T) []loadProcess
		expected int
		stopped  bool
		ok       bool
		last     string
	}{
		{"every expected run passed", func(t *testing.T) []loadProcess {
			return []loadProcess{processWith(t, "exit status 0", "pass", "pass"), processWith(t, "exit status 0", "pass", "pass")}
		}, 4, false, true, "load: 4 passed, 0 failed, 0 skipped of 4 runs expected on "},
		{"a run missing", func(t *testing.T) []loadProcess {
			return []loadProcess{processWith(t, "exit status 0", "pass")}
		}, 2, false, false, "load: 1 passed, 0 failed, 0 skipped of 2 runs expected on "},
		{"a run more than expected", func(t *testing.T) []loadProcess {
			return []loadProcess{processWith(t, "exit status 0", "pass", "pass", "pass")}
		}, 2, false, false, "load: 3 passed, 0 failed, 0 skipped of 2 runs expected on "},
		{"a run failed beside the expected passes", func(t *testing.T) []loadProcess {
			return []loadProcess{processWith(t, "exit status 1", "pass", "pass", "fail")}
		}, 2, false, false, "load: 2 passed, 1 failed, 0 skipped of 2 runs expected on "},
		{"a run skipped beside the expected passes", func(t *testing.T) []loadProcess {
			return []loadProcess{processWith(t, "exit status 0", "pass", "pass", "skip")}
		}, 2, false, false, "load: 2 passed, 0 failed, 1 skipped of 2 runs expected on "},
		{"a process that did not exit 0 after its runs passed", func(t *testing.T) []loadProcess {
			return []loadProcess{processWith(t, "exit status 1", "pass", "pass")}
		}, 2, false, false, "load: 2 passed, 0 failed, 0 skipped of 2 runs expected on "},
		{"a process killed after its runs passed", func(t *testing.T) []loadProcess {
			return []loadProcess{processWith(t, "signal: killed", "pass", "pass")}
		}, 2, false, false, "load: 2 passed, 0 failed, 0 skipped of 2 runs expected on "},
		{"stopped", func(t *testing.T) []loadProcess {
			return []loadProcess{processWith(t, "exit status 0", "pass", "pass")}
		}, 2, true, false, "    ok  \texample.com/f\t0.010s"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := evidenceRun()
			lines, ok := r.endLines(c.procs(t), c.expected, c.stopped)
			last := lines[len(lines)-1]
			if ok != c.ok || !strings.HasPrefix(last, c.last) {
				t.Errorf("got ok %v, last line %q; want ok %v, %q", ok, last, c.ok, c.last)
			}
			if strings.HasPrefix(c.last, "load: ") && last != c.last+r.head {
				t.Errorf("last line %q does not end with the commit", last)
			}
		})
	}
}

func TestCommitTextSaysWhenTheTreeIsNotClean(t *testing.T) {
	if got := commitText("abc", ""); got != "abc" {
		t.Errorf("a clean tree: got %q", got)
	}
	if got := commitText("abc", " M a.go\n"); got != "abc with uncommitted changes" {
		t.Errorf("a changed tree: got %q", got)
	}
}

// The workers stop when asked, and stop returns once every one of them
// ended.
func TestStopEndsEveryBusyWorker(t *testing.T) {
	stop := startWorkers(1)
	done := make(chan struct{})
	go func() { stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("stop did not return within 10 s: a busy worker did not end")
	}
}

// envLoadHelper makes this package's test binary testgate load (TestMain):
// the integration tests start it as the command, with one busy worker, so
// that they keep at most one core busy.
const envLoadHelper = "TESTGATE_LOAD_HELPER"

// loadHelper is what main does for load, with one busy worker instead of one
// per core.
func loadHelper(args []string) int {
	inv, err := readArgs(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if inv.name != "load" {
		fmt.Fprintln(os.Stderr, "the load helper runs load only")
		return 2
	}
	return loadMain(inv.opts.(loadOptions), inv.args[0], 1, os.Stdout, os.Stderr)
}
