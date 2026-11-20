package main

import (
	"slices"
	"strings"
	"testing"
)

// jsonLines joins the lines of a go test -json output.
func jsonLines(lines ...string) *strings.Reader {
	return strings.NewReader(strings.Join(lines, "\n") + "\n")
}

func equalStrings(t *testing.T, got, want []string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") || len(got) != len(want) {
		t.Fatalf("got %d lines:\n%s\nwant %d:\n%s", len(got), strings.Join(got, "\n"), len(want), strings.Join(want, "\n"))
	}
}

// skippingRun is the go test -json output of a package whose tests skip in
// every way a test can, beside one that passes, and of a package with no
// test files, which go test reports skipped as a package.
var skippingRun = []string{
	`{"Action":"start","Package":"example.com/s/a"}`,
	`{"Action":"start","Package":"example.com/s/c"}`,
	`{"Action":"output","Package":"example.com/s/c","Output":"?   \texample.com/s/c\t[no test files]\n"}`,
	`{"Action":"skip","Package":"example.com/s/c","Elapsed":0}`,
	`{"Action":"run","Package":"example.com/s/a","Test":"TestPasses"}`,
	`{"Action":"output","Package":"example.com/s/a","Test":"TestPasses","Output":"=== RUN   TestPasses\n"}`,
	`{"Action":"output","Package":"example.com/s/a","Test":"TestPasses","Output":"    a_test.go:5: passing log\n"}`,
	`{"Action":"output","Package":"example.com/s/a","Test":"TestPasses","Output":"--- PASS: TestPasses (0.00s)\n"}`,
	`{"Action":"pass","Package":"example.com/s/a","Test":"TestPasses","Elapsed":0}`,
	`{"Action":"run","Package":"example.com/s/a","Test":"TestSkips"}`,
	`{"Action":"output","Package":"example.com/s/a","Test":"TestSkips","Output":"=== RUN   TestSkips\n"}`,
	`{"Action":"output","Package":"example.com/s/a","Test":"TestSkips","Output":"    a_test.go:6: first a log\n"}`,
	`{"Action":"output","Package":"example.com/s/a","Test":"TestSkips","Output":"    a_test.go:6: no tmux here: 4095 of 5000\n"}`,
	`{"Action":"output","Package":"example.com/s/a","Test":"TestSkips","Output":"--- SKIP: TestSkips (0.00s)\n"}`,
	`{"Action":"skip","Package":"example.com/s/a","Test":"TestSkips","Elapsed":0}`,
	`{"Action":"run","Package":"example.com/s/a","Test":"TestSkipsNow"}`,
	`{"Action":"output","Package":"example.com/s/a","Test":"TestSkipsNow","Output":"=== RUN   TestSkipsNow\n"}`,
	`{"Action":"output","Package":"example.com/s/a","Test":"TestSkipsNow","Output":"--- SKIP: TestSkipsNow (0.00s)\n"}`,
	`{"Action":"skip","Package":"example.com/s/a","Test":"TestSkipsNow","Elapsed":0}`,
	`{"Action":"run","Package":"example.com/s/a","Test":"TestMultiLine"}`,
	`{"Action":"output","Package":"example.com/s/a","Test":"TestMultiLine","Output":"=== RUN   TestMultiLine\n"}`,
	`{"Action":"output","Package":"example.com/s/a","Test":"TestMultiLine","Output":"    a_test.go:8: line one\n"}`,
	`{"Action":"output","Package":"example.com/s/a","Test":"TestMultiLine","Output":"        line two\n"}`,
	`{"Action":"output","Package":"example.com/s/a","Test":"TestMultiLine","Output":"--- SKIP: TestMultiLine (0.00s)\n"}`,
	`{"Action":"skip","Package":"example.com/s/a","Test":"TestMultiLine","Elapsed":0}`,
	`{"Action":"run","Package":"example.com/s/a","Test":"TestParent"}`,
	`{"Action":"output","Package":"example.com/s/a","Test":"TestParent","Output":"=== RUN   TestParent\n"}`,
	`{"Action":"run","Package":"example.com/s/a","Test":"TestParent/sub_case"}`,
	`{"Action":"output","Package":"example.com/s/a","Test":"TestParent/sub_case","Output":"=== RUN   TestParent/sub_case\n"}`,
	`{"Action":"output","Package":"example.com/s/a","Test":"TestParent/sub_case","Output":"    a_test.go:10: sub reason\n"}`,
	`{"Action":"output","Package":"example.com/s/a","Test":"TestParent/sub_case","Output":"--- SKIP: TestParent/sub_case (0.00s)\n"}`,
	`{"Action":"skip","Package":"example.com/s/a","Test":"TestParent/sub_case","Elapsed":0}`,
	`{"Action":"output","Package":"example.com/s/a","Test":"TestParent","Output":"--- PASS: TestParent (0.00s)\n"}`,
	`{"Action":"pass","Package":"example.com/s/a","Test":"TestParent","Elapsed":0}`,
	`{"Action":"output","Package":"example.com/s/a","Output":"PASS\n"}`,
	`{"Action":"output","Package":"example.com/s/a","Output":"coverage: 80.0% of statements in ./...\n"}`,
	`{"Action":"output","Package":"example.com/s/a","Output":"ok  \texample.com/s/a\t0.001s\tcoverage: 80.0% of statements in ./...\n"}`,
	`{"Action":"pass","Package":"example.com/s/a","Elapsed":0.002}`,
}

func TestSkipLinesNameEverySkippedTestAndSubtestWithItsReasonOnOneLine(t *testing.T) {
	got, err := skipLines("integration", jsonLines(skippingRun...))
	if err != nil {
		t.Fatal(err)
	}
	equalStrings(t, got, []string{
		"check: skip integration example.com/s/a TestSkips: first a log no tmux here: 4095 of 5000",
		"check: skip integration example.com/s/a TestSkipsNow: no reason given",
		"check: skip integration example.com/s/a TestMultiLine: line one line two",
		"check: skip integration example.com/s/a TestParent/sub_case: sub reason",
	})
}

func TestSkipLinesAreNoneWhenNoTestSkipped(t *testing.T) {
	got, err := skipLines("unit", jsonLines(
		`{"Action":"start","Package":"example.com/s/c"}`,
		`{"Action":"output","Package":"example.com/s/c","Output":"?   \texample.com/s/c\t[no test files]\n"}`,
		`{"Action":"skip","Package":"example.com/s/c","Elapsed":0}`,
		`{"Action":"run","Package":"example.com/s/a","Test":"TestPasses"}`,
		`{"Action":"output","Package":"example.com/s/a","Test":"TestPasses","Output":"--- PASS: TestPasses (0.00s)\n"}`,
		`{"Action":"pass","Package":"example.com/s/a","Test":"TestPasses","Elapsed":0}`,
		"go: downloading example.com/x v1.0.0",
	))
	if err != nil {
		t.Fatal(err)
	}
	equalStrings(t, got, nil)
}

// A reason that test2json passes on in pieces, and one written with a
// file name that is not a test's position, stay whole.
func TestSkipLinesJoinTheReasonsPiecesAndKeepWhatIsNotAPosition(t *testing.T) {
	got, err := skipLines("e2e", jsonLines(
		`{"Action":"output","Package":"p","Test":"TestA","Output":"    a_test.go:12: half "}`,
		`{"Action":"output","Package":"p","Test":"TestA","Output":"and half\n"}`,
		`{"Action":"skip","Package":"p","Test":"TestA","Elapsed":0}`,
		`{"Action":"output","Package":"p","Test":"TestB","Output":"    b_test.go:3: see notes.go: here\n"}`,
		`{"Action":"skip","Package":"p","Test":"TestB","Elapsed":0}`,
		`{"Action":"output","Package":"p","Test":"TestC","Output":"    notes.go: no line number\n"}`,
		`{"Action":"skip","Package":"p","Test":"TestC","Elapsed":0}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	equalStrings(t, got, []string{
		"check: skip e2e p TestA: half and half",
		"check: skip e2e p TestB: see notes.go: here",
		"check: skip e2e p TestC: notes.go: no line number",
	})
}

// failingRun is the go test -json output of a run in which a package
// passes, one fails a test, a log of a subtest and a direct print among
// them, and one has no test files; the failing package started second and
// ended last.
var failingRun = []string{
	`{"Action":"start","Package":"example.com/s/a"}`,
	`{"Action":"start","Package":"example.com/s/b"}`,
	`{"Action":"start","Package":"example.com/s/c"}`,
	`{"Action":"output","Package":"example.com/s/c","Output":"?   \texample.com/s/c\t[no test files]\n"}`,
	`{"Action":"skip","Package":"example.com/s/c","Elapsed":0}`,
	`{"Action":"run","Package":"example.com/s/b","Test":"TestFails"}`,
	`{"Action":"output","Package":"example.com/s/b","Test":"TestFails","Output":"=== RUN   TestFails\n"}`,
	`{"Action":"output","Package":"example.com/s/b","Test":"TestFails","Output":"    b_test.go:5: a log\n"}`,
	`{"Action":"output","Package":"example.com/s/b","Test":"TestFails","Output":"    b_test.go:5: broken\n"}`,
	`{"Action":"output","Package":"example.com/s/b","Test":"TestFails","Output":"        on two lines\n"}`,
	`{"Action":"output","Package":"example.com/s/b","Test":"TestFails","Output":"--- FAIL: TestFails (0.00s)\n"}`,
	`{"Action":"fail","Package":"example.com/s/b","Test":"TestFails","Elapsed":0}`,
	`{"Action":"run","Package":"example.com/s/b","Test":"TestFine"}`,
	`{"Action":"output","Package":"example.com/s/b","Test":"TestFine","Output":"=== RUN   TestFine\n"}`,
	`{"Action":"output","Package":"example.com/s/b","Test":"TestFine","Output":"    b_test.go:6: fine log\n"}`,
	`{"Action":"output","Package":"example.com/s/b","Test":"TestFine","Output":"printed directly\n"}`,
	`{"Action":"output","Package":"example.com/s/b","Test":"TestFine","Output":"--- PASS: TestFine (0.00s)\n"}`,
	`{"Action":"pass","Package":"example.com/s/b","Test":"TestFine","Elapsed":0}`,
	`{"Action":"run","Package":"example.com/s/b","Test":"TestParentFails"}`,
	`{"Action":"output","Package":"example.com/s/b","Test":"TestParentFails","Output":"=== RUN   TestParentFails\n"}`,
	`{"Action":"output","Package":"example.com/s/b","Test":"TestParentFails","Output":"    b_test.go:7: parent log\n"}`,
	`{"Action":"run","Package":"example.com/s/b","Test":"TestParentFails/bad"}`,
	`{"Action":"output","Package":"example.com/s/b","Test":"TestParentFails/bad","Output":"=== RUN   TestParentFails/bad\n"}`,
	`{"Action":"run","Package":"example.com/s/b","Test":"TestParentFails/bad/deeper"}`,
	`{"Action":"output","Package":"example.com/s/b","Test":"TestParentFails/bad/deeper","Output":"=== RUN   TestParentFails/bad/deeper\n"}`,
	`{"Action":"output","Package":"example.com/s/b","Test":"TestParentFails/bad/deeper","Output":"    b_test.go:9: deep broken\n"}`,
	`{"Action":"output","Package":"example.com/s/b","Test":"TestParentFails/bad/deeper","Output":"--- FAIL: TestParentFails/bad/deeper (0.00s)\n"}`,
	`{"Action":"fail","Package":"example.com/s/b","Test":"TestParentFails/bad/deeper","Elapsed":0}`,
	`{"Action":"output","Package":"example.com/s/b","Test":"TestParentFails/bad","Output":"    b_test.go:8: sub broken\n"}`,
	`{"Action":"output","Package":"example.com/s/b","Test":"TestParentFails/bad","Output":"--- FAIL: TestParentFails/bad (0.00s)\n"}`,
	`{"Action":"fail","Package":"example.com/s/b","Test":"TestParentFails/bad","Elapsed":0}`,
	`{"Action":"run","Package":"example.com/s/b","Test":"TestParentFails/good"}`,
	`{"Action":"output","Package":"example.com/s/b","Test":"TestParentFails/good","Output":"=== RUN   TestParentFails/good\n"}`,
	`{"Action":"output","Package":"example.com/s/b","Test":"TestParentFails/good","Output":"    b_test.go:9: good log\n"}`,
	`{"Action":"output","Package":"example.com/s/b","Test":"TestParentFails/good","Output":"--- PASS: TestParentFails/good (0.00s)\n"}`,
	`{"Action":"pass","Package":"example.com/s/b","Test":"TestParentFails/good","Elapsed":0}`,
	`{"Action":"output","Package":"example.com/s/b","Test":"TestParentFails","Output":"--- FAIL: TestParentFails (0.00s)\n"}`,
	`{"Action":"run","Package":"example.com/s/a","Test":"TestPasses"}`,
	`{"Action":"output","Package":"example.com/s/a","Test":"TestPasses","Output":"=== RUN   TestPasses\n"}`,
	`{"Action":"fail","Package":"example.com/s/b","Test":"TestParentFails","Elapsed":0}`,
	`{"Action":"output","Package":"example.com/s/a","Test":"TestPasses","Output":"    a_test.go:5: passing log\n"}`,
	`{"Action":"output","Package":"example.com/s/a","Test":"TestPasses","Output":"printed directly in a package that passes\n"}`,
	`{"Action":"output","Package":"example.com/s/a","Test":"TestPasses","Output":"--- PASS: TestPasses (0.00s)\n"}`,
	`{"Action":"output","Package":"example.com/s/b","Output":"FAIL\n"}`,
	`{"Action":"pass","Package":"example.com/s/a","Test":"TestPasses","Elapsed":0}`,
	`{"Action":"output","Package":"example.com/s/a","Output":"PASS\n"}`,
	`{"Action":"output","Package":"example.com/s/a","Output":"coverage: 80.0% of statements\n"}`,
	`{"Action":"output","Package":"example.com/s/a","Output":"ok  \texample.com/s/a\t0.001s\tcoverage: 80.0% of statements\n"}`,
	`{"Action":"pass","Package":"example.com/s/a","Elapsed":0.002}`,
	`{"Action":"output","Package":"example.com/s/b","Output":"FAIL\texample.com/s/b\t0.002s\n"}`,
	`{"Action":"fail","Package":"example.com/s/b","Elapsed":0.002}`,
}

// The lines are those go test prints without -json for the same run.
func TestPlainOutputIsWhatGoTestPrintsWithoutJSON(t *testing.T) {
	got, err := plainOutput(jsonLines(failingRun...))
	if err != nil {
		t.Fatal(err)
	}
	equalStrings(t, got, []string{
		"ok  \texample.com/s/a\t0.001s\tcoverage: 80.0% of statements",
		"--- FAIL: TestFails (0.00s)",
		"    b_test.go:5: a log",
		"    b_test.go:5: broken",
		"        on two lines",
		"printed directly",
		"--- FAIL: TestParentFails (0.00s)",
		"    b_test.go:7: parent log",
		"    --- FAIL: TestParentFails/bad (0.00s)",
		"        --- FAIL: TestParentFails/bad/deeper (0.00s)",
		"            b_test.go:9: deep broken",
		"        b_test.go:8: sub broken",
		"FAIL",
		"FAIL\texample.com/s/b\t0.002s",
		"?   \texample.com/s/c\t[no test files]",
		"FAIL",
	})
}

// A package that did not build: the compiler's lines come when go test
// reports them, each package's result in the order the packages started,
// once it and every package before it have ended.
func TestPlainOutputGivesTheBuildsOutputAsItComesAndThePackagesInTheirOrder(t *testing.T) {
	got, err := plainOutput(jsonLines(
		`{"ImportPath":"example.com/t/d [example.com/t/d.test]","Action":"build-output","Output":"# example.com/t/d [example.com/t/d.test]\n"}`,
		`{"ImportPath":"example.com/t/d [example.com/t/d.test]","Action":"build-output","Output":"d/d_test.go:5:28: undefined: undefined\n"}`,
		`{"ImportPath":"example.com/t/d [example.com/t/d.test]","Action":"build-fail"}`,
		`{"Action":"start","Package":"example.com/t/d"}`,
		`{"Action":"output","Package":"example.com/t/d","Output":"FAIL\texample.com/t/d [build failed]\n"}`,
		`{"Action":"fail","Package":"example.com/t/d","Elapsed":0,"FailedBuild":"example.com/t/d [example.com/t/d.test]"}`,
		`{"ImportPath":"example.com/t/f [example.com/t/f.test]","Action":"build-output","Output":"# example.com/t/f\n"}`,
		`{"ImportPath":"example.com/t/f [example.com/t/f.test]","Action":"build-output","Output":"f/f_test.go:5:42: fmt.Printf format %d has arg \"s\" of wrong type string\n"}`,
		`{"ImportPath":"example.com/t/f [example.com/t/f.test]","Action":"build-fail"}`,
		`{"Action":"start","Package":"example.com/t/e"}`,
		`{"Action":"start","Package":"example.com/t/f"}`,
		`{"Action":"output","Package":"example.com/t/f","Output":"FAIL\texample.com/t/f [build failed]\n"}`,
		`{"Action":"fail","Package":"example.com/t/f","Elapsed":0,"FailedBuild":"example.com/t/f [example.com/t/f.test]"}`,
		`{"Action":"run","Package":"example.com/t/e","Test":"TestPanics"}`,
		`{"Action":"output","Package":"example.com/t/e","Test":"TestPanics","Output":"=== RUN   TestPanics\n"}`,
		`{"Action":"output","Package":"example.com/t/e","Test":"TestPanics","Output":"--- FAIL: TestPanics (0.00s)\n"}`,
		`{"Action":"output","Package":"example.com/t/e","Test":"TestPanics","Output":"panic: boom [recovered, repanicked]\n"}`,
		`{"Action":"output","Package":"example.com/t/e","Test":"TestPanics","Output":"\n"}`,
		`{"Action":"output","Package":"example.com/t/e","Test":"TestPanics","Output":"goroutine 20 [running]:\n"}`,
		`{"Action":"output","Package":"example.com/t/e","Test":"TestPanics","Output":"\t/src/testing/testing.go:1974 +0x1a0\n"}`,
		`{"Action":"fail","Package":"example.com/t/e","Test":"TestPanics","Elapsed":0}`,
		`{"Action":"output","Package":"example.com/t/e","Output":"FAIL\texample.com/t/e\t0.004s\n"}`,
		`{"Action":"fail","Package":"example.com/t/e","Elapsed":0.004}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	equalStrings(t, got, []string{
		"# example.com/t/d [example.com/t/d.test]",
		"d/d_test.go:5:28: undefined: undefined",
		"FAIL\texample.com/t/d [build failed]",
		"# example.com/t/f",
		`f/f_test.go:5:42: fmt.Printf format %d has arg "s" of wrong type string`,
		"--- FAIL: TestPanics (0.00s)",
		"panic: boom [recovered, repanicked]",
		"",
		"goroutine 20 [running]:",
		"\t/src/testing/testing.go:1974 +0x1a0",
		"FAIL\texample.com/t/e\t0.004s",
		"FAIL\texample.com/t/f [build failed]",
		"FAIL",
	})
}

// What is not go test's JSON (a line of the go command's own, a script's
// message) stays as it is, where it came; output that test2json passes on
// in pieces is one line again; a run that ended before its test and its
// package did, killed by a timeout, still shows them.
func TestPlainOutputKeepsOtherLinesAndARunThatDidNotEnd(t *testing.T) {
	got, err := plainOutput(jsonLines(
		"go: downloading example.com/x v1.0.0",
		`{"Action":"start","Package":"p"}`,
		`{"Action":"run","Package":"p","Test":"TestHangs"}`,
		`{"Action":"output","Package":"p","Test":"TestHangs","Output":"=== RUN   TestHangs\n"}`,
		`{"Action":"output","Package":"p","Test":"TestHangs","Output":"    h_test.go:3: wait"}`,
		`{"Action":"output","Package":"p","Test":"TestHangs","Output":"ing\n"}`,
		`{"Action":"output","Package":"p","Output":"panic: test timed out after 1s\n"}`,
		"{not json",
		"boom",
	))
	if err != nil {
		t.Fatal(err)
	}
	equalStrings(t, got, []string{
		"go: downloading example.com/x v1.0.0",
		"{not json",
		"boom",
		"    h_test.go:3: waiting",
		"panic: test timed out after 1s",
	})
}

// A package that passed with no line of its own (its events cut short)
// prints nothing.
func TestPlainOutputOfARunThatPassedIsTheResultLines(t *testing.T) {
	got, err := plainOutput(jsonLines(append(slices.Clone(skippingRun),
		`{"Action":"start","Package":"example.com/s/quiet"}`,
		`{"Action":"pass","Package":"example.com/s/quiet","Elapsed":0}`)...))
	if err != nil {
		t.Fatal(err)
	}
	equalStrings(t, got, []string{
		"ok  \texample.com/s/a\t0.001s\tcoverage: 80.0% of statements in ./...",
		"?   \texample.com/s/c\t[no test files]",
	})
}

func TestTestOutputReadsTheFileTheCommandNames(t *testing.T) {
	for _, c := range []struct {
		args []string // before the file
		want string
	}{
		{[]string{"skips", "unit"}, "check: skip unit example.com/s/a TestSkips: first a log no tmux here: 4095 of 5000\n" +
			"check: skip unit example.com/s/a TestSkipsNow: no reason given\n" +
			"check: skip unit example.com/s/a TestMultiLine: line one line two\n" +
			"check: skip unit example.com/s/a TestParent/sub_case: sub reason\n"},
		{[]string{"plain"}, "ok  \texample.com/s/a\t0.001s\tcoverage: 80.0% of statements in ./...\n" +
			"?   \texample.com/s/c\t[no test files]\n"},
	} {
		dir := t.TempDir()
		write(t, dir, "out.json", strings.Join(skippingRun, "\n")+"\n")
		inv := invocation{c.args[0], append(c.args[1:], dir+"/out.json")}
		var b strings.Builder
		if err := testOutput(inv, &b); err != nil || b.String() != c.want {
			t.Errorf("%v: got %q, err %v; want %q", inv, b.String(), err, c.want)
		}
	}
	var b strings.Builder
	if err := testOutput(invocation{"plain", []string{t.TempDir() + "/missing.json"}}, &b); err == nil || b.Len() != 0 {
		t.Errorf("a file that is not there: printed %q, err %v; want an error", b.String(), err)
	}
}
