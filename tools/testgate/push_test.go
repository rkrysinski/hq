package main

import (
	"reflect"
	"strings"
	"testing"
)

// commitPatch is git show's patch of one commit as checkPush asks for it
// (patchArgs), written out by hand from a real run.
const commitPatch = "diff --git d/acme notes.txt d/acme notes.txt\n" +
	"new file mode 100644\n" +
	"index 0000000..b680253\n" +
	"--- /dev/null\n" +
	"+++ d/acme notes.txt\t\n" +
	"@@ -0,0 +1 @@\n" +
	"+fine\n" +
	"diff --git f.txt f.txt\n" +
	"index de98044..93e9516 100644\n" +
	"--- f.txt\n" +
	"+++ f.txt\n" +
	"@@ -2,2 +2 @@ a\n" +
	"-acme was here\n" +
	"-+++ acme.txt\n" +
	"+fine\n" +
	"@@ -9,0 +10,2 @@ c\n" +
	"++++ acme.txt\n" +
	"+and acme again\n" +
	"\\ No newline at end of file\n" +
	"diff --git gone.txt gone.txt\n" +
	"deleted file mode 100644\n" +
	"index 1111111..0000000\n" +
	"--- gone.txt\n" +
	"+++ /dev/null\n" +
	"@@ -1 +0,0 @@\n" +
	"-acme\n" +
	"diff --git i.png i.png\n" +
	"new file mode 100644\n" +
	"index 0000000..e524aef\n" +
	"Binary files /dev/null and i.png differ\n"

func strs(hits []hit) []string {
	out := []string{}
	for _, h := range hits {
		out = append(out, h.String())
	}
	return out
}

func TestACommitIsCheckedInItsMessageAddedLinesAndNewPathsOnly(t *testing.T) {
	pats, _ := parsePatterns("acme\n")
	got := strs(scanCommit("8eec91d1dfd234a2b33de75ba0cf9dd68f90b46c", "Ship it\n\nfor ACME as promised\n", commitPatch, pats))
	want := []string{
		"8eec91d message:3 matches private pattern 1",
		"8eec91d d/acme notes.txt (path) matches private pattern 1",
		"8eec91d f.txt:10 matches private pattern 1",
		"8eec91d f.txt:11 matches private pattern 1",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("hits\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestAnAddedLineIsCheckedWhateverBytesItHolds(t *testing.T) {
	pats, _ := parsePatterns("acme\n")
	patch := "+++ f.txt\n@@ -0,0 +1,2 @@\n+\x01acme\n+\x00acme\n"
	want := []string{"1234567 f.txt:1 matches private pattern 1", "1234567 f.txt:2 matches private pattern 1"}
	if got := strs(scanCommit("1234567", "Clean\n", patch, pats)); !reflect.DeepEqual(got, want) {
		t.Fatalf("hits %v, want %v", got, want)
	}
}

func TestAPushWithoutTheListIsRefused(t *testing.T) {
	summary, problems, err := checkPush("# nothing but a comment\n", t.TempDir(), strings.NewReader("refs/heads/main 1 refs/heads/main 0\n"))
	if err != nil || !strings.Contains(summary, "no pattern list") || len(problems) != 1 ||
		!strings.Contains(problems[0], PrivatePatternsEnv) || !strings.Contains(problems[0], PrivatePatternsFile) {
		t.Fatalf("summary %q problems %v err %v", summary, problems, err)
	}
}
