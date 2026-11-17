package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// testSrc is a test file whose one function holds body, with the time,
// context and testing packages imported.
func testSrc(body string) []byte {
	return []byte("package a\n\nimport (\n\t\"context\"\n\t\"testing\"\n\t\"time\"\n)\n\n" +
		"var _ = context.Background\n\nfunc TestX(t *testing.T) {\n\tstart := time.Now()\n" + body + "\n}\n")
}

// bodyLine is the line of the first line of a testSrc body.
const bodyLine = 13

func TestTimeboundFindsUpperBoundsUnderASecondOnRealTime(t *testing.T) {
	for _, tc := range []struct{ body, bound string }{
		{"\tif time.Since(start) > 300*time.Millisecond {\n\t\tt.Fatal()\n\t}", "300ms"},
		{"\tif time.Since(start) >= 999*time.Millisecond {\n\t\tt.Fatal()\n\t}", "999ms"},
		{"\tif time.Now().Sub(start) > time.Second/2 {\n\t\tt.Fatal()\n\t}", "500ms"},
		{"\tif 300*time.Millisecond < time.Since(start) {\n\t\tt.Fatal()\n\t}", "300ms"},
		{"\tif 300*time.Millisecond <= time.Now().Sub(start) {\n\t\tt.Fatal()\n\t}", "300ms"},
		{"\tif d := time.Since(start); d > 300*time.Millisecond {\n\t\tt.Fatal(d)\n\t}", "300ms"},
		{"\td := time.Since(start)\n\tif d >= 300*time.Millisecond {\n\t\tt.Fatal(d)\n\t}", "300ms"},
		{"\td := time.Now().Sub(start)\n\tif d > 300*time.Millisecond {\n\t\tt.Fatal(d)\n\t}", "300ms"},
		{"\tvar d time.Duration\n\td = time.Since(start)\n\tif 300*time.Millisecond < d {\n\t\tt.Fatal(d)\n\t}", "300ms"},
		{"\tvar d = time.Since(start)\n\tif 300*time.Millisecond <= d {\n\t\tt.Fatal(d)\n\t}", "300ms"},
		{"\tif time.Since(start) > 2*time.Second-1500*time.Millisecond {\n\t\tt.Fatal()\n\t}", "500ms"},
		{"\tif time.Since(start) > time.Second-time.Nanosecond {\n\t\tt.Fatal()\n\t}", "999.999999ms"},
		{"\tif time.Since(start) > (time.Duration(3) * 100 * time.Millisecond) {\n\t\tt.Fatal()\n\t}", "300ms"},
		{"\tif time.Since(start) > 3e8 {\n\t\tt.Fatal()\n\t}", "300ms"},
		{"\tconst bound = 300 * time.Millisecond\n\tif time.Since(start) > bound {\n\t\tt.Fatal()\n\t}", "300ms"},
		{"\tok := time.Since(start) > 300*time.Millisecond\n\t_ = ok", "300ms"},
	} {
		hits, err := scanTimebound("a_test.go", testSrc(tc.body))
		if err != nil {
			t.Fatal(err)
		}
		line := bodyLine + strings.Count(tc.body[:boundIndex(tc.body)], "\n")
		want := []timeboundHit{{"a_test.go", line, tc.bound}}
		if !reflect.DeepEqual(hits, want) {
			t.Errorf("%q: hits %v, want %v", tc.body, hits, want)
		}
	}
}

// boundIndex is where the comparison of a testSrc body starts: its first
// line with a comparison operator.
func boundIndex(body string) int {
	lines := strings.SplitAfter(body, "\n")
	n := 0
	for _, l := range lines {
		if strings.ContainsAny(l, "<>") {
			return n
		}
		n += len(l)
	}
	return 0
}

func TestTimeboundIgnoresBoundsOfASecondOrMoreLowerBoundsFakeClocksAndTimers(t *testing.T) {
	for _, body := range []string{
		"\tif time.Since(start) > time.Second {\n\t\tt.Fatal()\n\t}",
		"\tif time.Since(start) >= 1000*time.Millisecond {\n\t\tt.Fatal()\n\t}",
		"\tif time.Now().Sub(start) > 30*time.Second {\n\t\tt.Fatal()\n\t}",
		"\tif d := time.Since(start); d > 2*time.Second {\n\t\tt.Fatal(d)\n\t}",
		"\tif time.Second < time.Since(start) {\n\t\tt.Fatal()\n\t}",
		"\tif time.Since(start) < 300*time.Millisecond {\n\t\tt.Fatal()\n\t}",
		"\tif time.Since(start) <= 300*time.Millisecond {\n\t\tt.Fatal()\n\t}",
		"\tif 300*time.Millisecond > time.Since(start) {\n\t\tt.Fatal()\n\t}",
		"\tif 300*time.Millisecond >= time.Since(start) {\n\t\tt.Fatal()\n\t}",
		"\td := time.Since(start)\n\tif d < 300*time.Millisecond {\n\t\tt.Fatal(d)\n\t}",
		"\tif time.Since(start) == 300*time.Millisecond {\n\t\tt.Fatal()\n\t}",
		"\tf := struct{ now time.Time }{time.Now()}\n\tif f.now.Sub(start) > 300*time.Millisecond {\n\t\tt.Fatal()\n\t}",
		"\tif start.Sub(start) > 300*time.Millisecond {\n\t\tt.Fatal()\n\t}",
		"\tselect {\n\tcase <-time.After(20 * time.Millisecond):\n\t}",
		"\tctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)\n\tdefer cancel()\n\t<-ctx.Done()",
		"\tlimit := 300 * time.Millisecond\n\tif time.Since(start) > limit {\n\t\tt.Fatal()\n\t}",
		"\tif time.Since(start) > 300*time.Millisecond+limitOf(t) {\n\t\tt.Fatal()\n\t}",
		"\td := 300 * time.Millisecond\n\tif d > 200*time.Millisecond {\n\t\tt.Fatal(d)\n\t}",
	} {
		src := testSrc(body + "\n\t_ = start")
		src = append(src, []byte("\nfunc limitOf(*testing.T) time.Duration { return 0 }\n")...)
		hits, err := scanTimebound("a_test.go", src)
		if err != nil {
			t.Fatal(err)
		}
		if len(hits) != 0 {
			t.Errorf("%q: hits %v, want none", body, hits)
		}
	}
}

func TestTimeboundFollowsTheTimePackageUnderAnyName(t *testing.T) {
	src := "package a\n\nimport tm \"time\"\n\nfunc f(start tm.Time) bool {\n\treturn tm.Since(start) > 300*tm.Millisecond\n}\n"
	hits, err := scanTimebound("a_test.go", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if want := []timeboundHit{{"a_test.go", 6, "300ms"}}; !reflect.DeepEqual(hits, want) {
		t.Fatalf("hits %v, want %v", hits, want)
	}
	other := "package a\n\nimport time \"example.com/clock\"\n\nfunc f(start int) bool {\n\treturn time.Since(start) > 3\n}\n"
	if hits, err := scanTimebound("a_test.go", []byte(other)); err != nil || len(hits) != 0 {
		t.Fatalf("another package named time: hits %v, err %v", hits, err)
	}
}

func TestTimeboundHitNamesFileLineAndBound(t *testing.T) {
	s := timeboundHit{"internal/proc/proc_integration_test.go", 129, "300ms"}.String()
	for _, want := range []string{"internal/proc/proc_integration_test.go:129", "300ms", "1s"} {
		if !strings.Contains(s, want) {
			t.Errorf("%q names no %q", s, want)
		}
	}
}

func TestTimeboundReportsAFileItCannotParse(t *testing.T) {
	if _, err := scanTimebound("a_test.go", []byte("package a\n\nfunc {")); err == nil {
		t.Fatal("a file that does not parse is no error")
	}
}

// TestCheckTimeboundReadsTestFilesOfEveryTagAndOnlyThem walks a tree with a
// bound in a test file of each build tag, under tools/ too, and the same
// bound where the gate does not look.
func TestCheckTimeboundReadsTestFilesOfEveryTagAndOnlyThem(t *testing.T) {
	root := t.TempDir()
	bound := "\nimport (\n\t\"testing\"\n\t\"time\"\n)\n\nfunc TestX(t *testing.T) {\n\tif time.Since(time.Now()) > 300*time.Millisecond {\n\t\tt.Fatal()\n\t}\n}\n"
	files := map[string]string{
		"internal/a/a_test.go":             "package a\n" + bound,
		"internal/a/a_integration_test.go": "//go:build integration\n\npackage a\n" + bound,
		"e2e/journey_test.go":              "//go:build e2e\n\npackage e2e\n" + bound,
		"tools/x/x_test.go":                "package x\n" + bound,
		"internal/a/a.go":                  "package a\n" + bound,
		"internal/a/testdata/t_test.go":    "package t\n" + bound,
		".claude/worktrees/w/b_test.go":    "package b\n" + bound,
		"internal/a/notes.txt":             "time.Since(start) > 300*time.Millisecond",
	}
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	hits, checked, err := checkTimebound(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []timeboundHit{
		{"e2e/journey_test.go", 11, "300ms"},
		{"internal/a/a_integration_test.go", 11, "300ms"},
		{"internal/a/a_test.go", 9, "300ms"},
		{"tools/x/x_test.go", 9, "300ms"},
	}
	if !reflect.DeepEqual(hits, want) || checked != 4 {
		t.Fatalf("hits %v in %d files, want %v in 4", hits, checked, want)
	}

	if err := os.WriteFile(filepath.Join(root, "internal", "a", "bad_test.go"), []byte("package a\nfunc {"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := checkTimebound(root); err == nil || !strings.Contains(err.Error(), "bad_test.go") {
		t.Fatalf("err %v, want one naming the file that does not parse", err)
	}
	if _, _, err := checkTimebound(filepath.Join(root, "missing")); err == nil {
		t.Fatal("a missing root is no error")
	}
}
