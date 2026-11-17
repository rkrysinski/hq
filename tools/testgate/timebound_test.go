package main

import (
	"go/ast"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// testSrc is a test file whose one function holds body, with the time,
// context and testing packages imported; body starts on line 13.
func testSrc(body string) []byte {
	return []byte("package a\n\nimport (\n\t\"context\"\n\t\"testing\"\n\t\"time\"\n)\n\n" +
		"var _ = context.Background\n\nfunc TestX(t *testing.T) {\n\tstart := time.Now()\n" + body + "\n}\n")
}

func TestTimeboundFindsUpperBoundsUnderASecondOnRealTime(t *testing.T) {
	for _, tc := range []struct {
		body  string
		line  int
		bound string
	}{
		{"\tif time.Since(start) > 300*time.Millisecond {\n\t\tt.Fatal()\n\t}", 13, "300ms"},
		{"\tif time.Since(start) >= 999*time.Millisecond {\n\t\tt.Fatal()\n\t}", 13, "999ms"},
		{"\tif time.Now().Sub(start) > time.Second/2 {\n\t\tt.Fatal()\n\t}", 13, "500ms"},
		{"\tif 300*time.Millisecond < time.Since(start) {\n\t\tt.Fatal()\n\t}", 13, "300ms"},
		{"\tif 300*time.Millisecond <= time.Now().Sub(start) {\n\t\tt.Fatal()\n\t}", 13, "300ms"},
		{"\tif d := time.Since(start); d > 300*time.Millisecond {\n\t\tt.Fatal(d)\n\t}", 13, "300ms"},
		{"\td := time.Since(start)\n\tif d >= 300*time.Millisecond {\n\t\tt.Fatal(d)\n\t}", 14, "300ms"},
		{"\td := time.Now().Sub(start)\n\tif d > 300*time.Millisecond {\n\t\tt.Fatal(d)\n\t}", 14, "300ms"},
		{"\tvar d time.Duration\n\td = time.Since(start)\n\tif 300*time.Millisecond < d {\n\t\tt.Fatal(d)\n\t}", 15, "300ms"},
		{"\tvar d = time.Since(start)\n\tif 300*time.Millisecond <= d {\n\t\tt.Fatal(d)\n\t}", 14, "300ms"},
		{"\tif time.Since(start) > 2*time.Second-1500*time.Millisecond {\n\t\tt.Fatal()\n\t}", 13, "500ms"},
		{"\tif time.Since(start) > time.Second-time.Nanosecond {\n\t\tt.Fatal()\n\t}", 13, "999.999999ms"},
		{"\tif time.Since(start) > (time.Duration(3) * 100 * time.Millisecond) {\n\t\tt.Fatal()\n\t}", 13, "300ms"},
		{"\tif time.Since(start) > 3e8 {\n\t\tt.Fatal()\n\t}", 13, "300ms"},
		{"\tif time.Since(start) > time.Second/3 {\n\t\tt.Fatal()\n\t}", 13, "333.333333ms"},
		{"\tif time.Since(start) > 1.5e9/3 {\n\t\tt.Fatal()\n\t}", 13, "500ms"},
		{"\tconst half = 250 * time.Millisecond\n\tif time.Since(start) > half+half {\n\t\tt.Fatal()\n\t}", 14, "500ms"},
		{"\tconst bound = 300 * time.Millisecond\n\tif time.Since(start) > bound {\n\t\tt.Fatal()\n\t}", 14, "300ms"},
		{"\tok := time.Since(start) > 300*time.Millisecond\n\t_ = ok", 13, "300ms"},
	} {
		hits, err := scanTimebound("a_test.go", testSrc(tc.body), nil)
		if err != nil {
			t.Fatal(err)
		}
		if want := []timeboundHit{{"a_test.go", tc.line, tc.bound}}; !reflect.DeepEqual(hits, want) {
			t.Errorf("%q: hits %v, want %v", tc.body, hits, want)
		}
	}
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
		"\td := time.Since(start)\n\td = time.Hour\n\tif d > 300*time.Millisecond {\n\t\tt.Fatal(d)\n\t}",
		"\td := time.Since(start)\n\td += time.Hour\n\tif d > 300*time.Millisecond {\n\t\tt.Fatal(d)\n\t}",
	} {
		src := append(testSrc(body+"\n\t_ = start"), "\nfunc limitOf(*testing.T) time.Duration { return 0 }\n"...)
		hits, err := scanTimebound("a_test.go", src, nil)
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
	hits, err := scanTimebound("a_test.go", []byte(src), nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := []timeboundHit{{"a_test.go", 6, "300ms"}}; !reflect.DeepEqual(hits, want) {
		t.Fatalf("hits %v, want %v", hits, want)
	}
	other := "package a\n\nimport time \"example.com/clock\"\n\nfunc f(start int) bool {\n\treturn time.Since(start) > 3\n}\n"
	if hits, err := scanTimebound("a_test.go", []byte(other), nil); err != nil || len(hits) != 0 {
		t.Fatalf("another package named time: hits %v, err %v", hits, err)
	}
}

func TestTimeboundStopsAtConstantsThatReferToThemselves(t *testing.T) {
	src := "package a\n\nimport \"time\"\n\nconst (\n\tloop = again\n\tagain = loop * time.Millisecond\n)\n\n" +
		"func f(start time.Time) bool {\n\treturn time.Since(start) > loop\n}\n"
	if hits, err := scanTimebound("a_test.go", []byte(src), nil); err != nil || len(hits) != 0 {
		t.Fatalf("hits %v, err %v, want none", hits, err)
	}
	other := map[string]pkgConst{}
	file := newTimeScan(&ast.File{}, other)
	file.pkg = "time"
	other["far"] = pkgConst{ast.NewIdent("near"), file}
	other["near"] = pkgConst{ast.NewIdent("far"), file}
	cyclic := "package a\n\nimport \"time\"\n\nfunc f(start time.Time) bool {\n\treturn time.Since(start) > far\n}\n"
	if hits, err := scanTimebound("a_test.go", []byte(cyclic), other); err != nil || len(hits) != 0 {
		t.Fatalf("across files: hits %v, err %v, want none", hits, err)
	}
}

// A constant that repeats the one before it implicitly is not followed,
// and does not stop the scan.
func TestTimeboundReadsPastAnImplicitlyRepeatedConstant(t *testing.T) {
	src := "package a\n\nimport \"time\"\n\nconst (\n\tfirst = 2 * time.Second\n\tsecond\n)\n\n" +
		"func f(start time.Time) bool {\n\treturn time.Since(start) > second || time.Since(start) > first/4\n}\n"
	hits, err := scanTimebound("a_test.go", []byte(src), nil)
	if want := []timeboundHit{{"a_test.go", 11, "500ms"}}; err != nil || !reflect.DeepEqual(hits, want) {
		t.Fatalf("hits %v, err %v, want %v", hits, err, want)
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
	if _, err := scanTimebound("a_test.go", []byte("package a\n\nfunc {"), nil); err == nil {
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
		// Constants of the package, in other files and under another name
		// for the time package.
		"internal/c/bounds_test.go": "package c\n\nimport \"time\"\n\nconst tight = 300 * time.Millisecond\n",
		"internal/c/c.go":           "package c\n\nimport tm \"time\"\n\nconst loose, half = 2 * tm.Second, tm.Second / 2\n",
		"internal/c/c_test.go": "package c\n\nimport (\n\t\"testing\"\n\t\"time\"\n)\n\nfunc TestX(t *testing.T) {\n" +
			"\tif time.Since(time.Now()) > loose || time.Since(time.Now()) > tight || time.Since(time.Now()) > half {\n\t\tt.Fatal()\n\t}\n}\n",
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
		{"internal/c/c_test.go", 9, "300ms"},
		{"internal/c/c_test.go", 9, "500ms"},
		{"tools/x/x_test.go", 9, "300ms"},
	}
	if !reflect.DeepEqual(hits, want) || checked != 6 {
		t.Fatalf("hits %v in %d files, want %v in 6", hits, checked, want)
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
