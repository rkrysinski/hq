package main

import (
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestJudgedFileIsGoCodeThatIsNoTestAndThatTheGoCommandBuilds(t *testing.T) {
	for _, c := range []struct {
		file string
		want bool
	}{
		{"main.go", true},
		{"internal/cli/start.go", true},
		{"tools/testgate/mutation.go", true},
		{"internal/cli/start_test.go", false},
		{"README.md", false},
		{"scripts/check.sh", false},
		{"go.mod", false},
		{"tools/testgate/testdata/loadfixture/fixture.go", false},
		{"testdata/x.go", false},
		{"internal/_old/x.go", false},
		{".github/x.go", false},
		{"internal/.hidden/x.go", false},
	} {
		if got := judgedFile(c.file); got != c.want {
			t.Errorf("judgedFile(%q) = %v, want %v", c.file, got, c.want)
		}
	}
}

func TestAddedLinesReadsTheNewSideOfEveryHunk(t *testing.T) {
	diff := `diff --git a/lib/lib.go b/lib/lib.go
index 1111111..2222222 100644
--- a/lib/lib.go
+++ b/lib/lib.go
@@ -4 +4 @@ func Half(x int) int { return x / 2 }
-func Half(x int) int { return x / 2 }
+func Half(x int) int { return x / 3 }
@@ -7,0 +8,3 @@ func Half(x int) int { return x / 2 }
+// Double doubles x.
+func Double(x int) int {
+	return x * 2
@@ -12,2 +15,0 @@ func gone() {
-	a()
-	b()
diff --git a/lib/lib_test.go b/lib/lib_test.go
--- a/lib/lib_test.go
+++ b/lib/lib_test.go
@@ -1 +1,2 @@
+package lib
diff --git a/old.go b/old.go
deleted file mode 100644
--- a/old.go
+++ /dev/null
@@ -1,3 +0,0 @@
-package m
diff --git "a/odd \"name\".go" "b/odd \"name\".go"
new file mode 100644
--- /dev/null
+++ "b/odd \"name\".go"
@@ -0,0 +1,2 @@
+package m
+var x = 1
diff --git a/docs/notes.md b/docs/notes.md
--- a/docs/notes.md
+++ b/docs/notes.md
@@ -1 +1 @@
+notes
`
	got, err := addedLines(diff)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]int{
		"lib/lib.go":    {4, 8, 9, 10},
		`odd "name".go`: {1, 2},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v\nwant %v", got, want)
	}
}

func TestAddedLinesTakesALineThatLooksLikeAHeaderInAHunkForALine(t *testing.T) {
	diff := "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -3,0 +4,2 @@\n+var s = `\n+++ x\n@@ -9 +11 @@\n+`\n" +
		"diff --git a/b.go b/b.go\n--- a/b.go\n+++ b/b.go\n@@ -1 +1 @@\n+package b\n"
	got, err := addedLines(diff)
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string][]int{"a.go": {4, 5, 11}, "b.go": {1}}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestAddedLinesRefusesAQuotedNameItCannotRead(t *testing.T) {
	if _, err := addedLines("diff --git a/x b/x\n+++ \"b/a\\q.go\"\n@@ -1 +1 @@\n"); err == nil {
		t.Error("a quoted name it cannot read is read without an error")
	}
	if got, err := addedLines("diff --git a/R b/R\n+++ b/README.md\n@@ -1 +1,2 @@\n"); err != nil || len(got) != 0 {
		t.Errorf("a hunk of a file it does not judge: got %v, %v; want it skipped", got, err)
	}
}

// shapes is Go source with a statement of every shape the coverage tool
// splits blocks at; shapesProfile is the profile of a real go test
// -covermode=set run that called Shapes(1, …) and Shapes(3, …) (go1.26).
const shapes = `package s

func Shapes(n int, c chan int) (out int) {
	switch n {
	case 1:
		out = 1
	case 2:
	default:
		out = 3
	}
	switch x := any(n).(type) {
	case int:
		_ = x
	}
	select {
	case v := <-c:
		out = v
	default:
	}
loop:
	for i := 0; i < n; i++ {
		if i == 2 {
			break loop
		} else if i == 5 {
			continue
		} else {
			out++
		}
	}
	f := func() {
		out += 10
	}
	if n > 100 {
		f()
	}
	for range n {
		out--
	}
	{
		out *= 1
	}
	return out
}
`

const shapesProfile = `mode: set
example.com/s/s/s.go:3.42,4.11 1 1
example.com/s/s/s.go:5.9,6.10 1 1
example.com/s/s/s.go:7.9,7.9 0 0
example.com/s/s/s.go:8.10,9.10 1 1
example.com/s/s/s.go:11.2,11.28 1 1
example.com/s/s/s.go:12.11,13.8 1 1
example.com/s/s/s.go:15.2,15.9 1 1
example.com/s/s/s.go:16.16,17.10 1 0
example.com/s/s/s.go:18.10,18.10 0 1
example.com/s/s/s.go:20.1,21.25 1 1
example.com/s/s/s.go:21.25,22.13 1 1
example.com/s/s/s.go:22.13,23.14 1 1
example.com/s/s/s.go:24.9,24.20 1 1
example.com/s/s/s.go:24.20,25.12 1 0
example.com/s/s/s.go:26.9,28.4 1 1
example.com/s/s/s.go:30.2,30.14 1 1
example.com/s/s/s.go:30.14,32.3 1 0
example.com/s/s/s.go:33.2,33.13 1 1
example.com/s/s/s.go:33.13,35.3 1 0
example.com/s/s/s.go:36.2,36.14 1 1
example.com/s/s/s.go:36.14,38.3 1 1
example.com/s/s/s.go:39.2,41.3 1 1
example.com/s/s/s.go:42.2,42.12 1 1
`

func TestStatementLinesGivesEachLineTheStatementThatHoldsIt(t *testing.T) {
	got, err := statementLines("s.go", []byte(shapes))
	if err != nil {
		t.Fatal(err)
	}
	want := map[int]srcPos{
		4:  {4, 2}, // switch n {
		5:  {5, 9}, // case 1: after its colon
		6:  {6, 3},
		7:  {7, 9}, // case 2: with no body
		8:  {8, 10},
		9:  {9, 3},
		11: {11, 9}, // the type switch's x := …, in the same block as the switch
		12: {12, 11},
		13: {13, 3},
		15: {15, 2},
		16: {16, 16}, // a select's case: not its receive
		17: {17, 3},
		18: {18, 10},
		20: {20, 1},  // the label
		21: {21, 21}, // i++, in the same block as the for
		22: {22, 3},
		23: {23, 4},
		24: {24, 10}, // else if: the inner if, inside the block of the else
		25: {25, 4},
		27: {27, 4},
		30: {30, 2},
		31: {31, 3}, // inside the function literal: its own statement
		32: {30, 2}, // the literal's closing brace: the assignment
		33: {33, 2},
		34: {34, 3},
		36: {36, 2},
		37: {37, 3},
		40: {40, 3}, // in a block on its own; the braces hold nothing
		42: {42, 2},
	}
	for l := 1; l <= strings.Count(shapes, "\n"); l++ {
		if got[l] != want[l] {
			t.Errorf("line %d: got %v, want %v", l, got[l], want[l])
		}
	}
}

func TestStatementLinesHoldsEveryLineOfAStatementAndOnlyTheHeaderOfABody(t *testing.T) {
	src := "package s\n\nvar table = []int{\n\t1,\n}\n\nfunc f(a, b bool) []int {\n" + // 1-7
		"\tx := []int{\n\t\t1,\n\t\t2,\n\t}\n" + // 8-11: one statement over four lines
		"\tif a &&\n\t\tb {\n\t\t// a comment\n\t\treturn x\n\t}\n" + // 12-16
		"\treturn nil\n}\n" // 17-18
	got, err := statementLines("s.go", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	want := map[int]srcPos{8: {8, 2}, 9: {8, 2}, 10: {8, 2}, 11: {8, 2}, 12: {12, 2}, 13: {12, 2}, 15: {15, 3}, 17: {17, 2}}
	if !maps.Equal(got, want) {
		t.Errorf("got %v\nwant %v", got, want)
	}
	if _, err := statementLines("bad.go", []byte("package s\nfunc {")); err == nil {
		t.Error("source that does not parse is read without an error")
	}
}

func TestNotRunLinesAreTheChangedStatementsNoProfileCounted(t *testing.T) {
	stmts, err := statementLines("s/s.go", []byte(shapes))
	if err != nil {
		t.Fatal(err)
	}
	c := ranBlocks{}
	if err := c.read(strings.NewReader(shapesProfile)); err != nil {
		t.Fatal(err)
	}
	every := make([]int, 44)
	for i := range every {
		every[i] = i + 1
	}
	added := map[string][]int{"s/s.go": every, "s/other.go": {3}}
	statements := map[string]map[int]srcPos{"s/s.go": stmts, "s/other.go": {3: {3, 2}}}
	got := notRunLines(added, statements, "example.com/s", c)
	want := []lineRef{
		{"s/other.go", 3}, // no profile lists its file
		{"s/s.go", 7}, {"s/s.go", 16}, {"s/s.go", 17}, {"s/s.go", 25}, {"s/s.go", 31}, {"s/s.go", 34},
	}
	if !slices.Equal(got, want) {
		t.Errorf("got %v\nwant %v", got, want)
	}
	// Another run that calls the function literal: its line ran, in either
	// profile.
	if err := c.read(strings.NewReader("mode: set\nexample.com/s/s/s.go:30.14,32.3 1 2\nexample.com/s/s/s.go:7.9,7.9 0 0\n")); err != nil {
		t.Fatal(err)
	}
	if got := notRunLines(added, statements, "example.com/s", c); slices.Contains(got, lineRef{"s/s.go", 31}) || !slices.Contains(got, lineRef{"s/s.go", 7}) {
		t.Errorf("after a second profile: got %v", got)
	}
}

func TestRanTakesTheInnermostBlockAndCountsItsEnds(t *testing.T) {
	c := ranBlocks{}
	profile := "mode: set\n" +
		"m/a.go:1.1,9.1 3 1\n" + // the outer block ran
		"m/a.go:3.5,5.2 1 0\n" + // a block inside it did not
		"m/a.go:3.5,4.2 1 1\n" + // nor did … but this one, ending first, did
		"m/a.go:7.4,7.4 0 0\n"
	if err := c.read(strings.NewReader(profile)); err != nil {
		t.Fatal(err)
	}
	for _, x := range []struct {
		p    srcPos
		want bool
	}{
		{srcPos{2, 1}, true},  // only the outer block
		{srcPos{3, 4}, true},  // a column before the inner blocks start
		{srcPos{3, 5}, true},  // where both inner blocks start: the one ending first
		{srcPos{4, 2}, true},  // its last column
		{srcPos{4, 3}, false}, // past it, in the other inner block
		{srcPos{5, 2}, false},
		{srcPos{5, 3}, true}, // past the inner blocks
		{srcPos{7, 4}, false},
		{srcPos{9, 1}, true},
		{srcPos{9, 2}, false}, // past every block
	} {
		if got := c.ran("m/a.go", x.p); got != x.want {
			t.Errorf("%v: got %v, want %v", x.p, got, x.want)
		}
	}
	if c.ran("m/b.go", srcPos{2, 1}) {
		t.Error("a file no profile lists ran")
	}
}

func TestRanBlocksRefusesALineItCannotRead(t *testing.T) {
	for _, line := range []string{
		"m/a.go:1.1,2.1 1",
		"m/a.go 1 1",
		"m/a.go:1.1-2.1 1 1",
		"m/a.go:1.1,2.1 x 1",
		"m/a.go:1.1,2.1 1 y",
	} {
		if err := (ranBlocks{}).read(strings.NewReader("mode: set\n" + line + "\n")); err == nil {
			t.Errorf("%q is read without an error", line)
		}
	}
	c := ranBlocks{}
	if err := c.read(strings.NewReader(":1.1,2.1 1 1\n")); err != nil || !c[coverBlock{"", srcPos{1, 1}, srcPos{2, 1}}] {
		t.Errorf("a block with an empty file name: %v, %v", c, err)
	}
}
