package main

import (
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestPatternsSkipBlankAndCommentLinesAndIgnoreCase(t *testing.T) {
	pats, err := parsePatterns("# a comment\n\nacme-corp\n  /Users/jdoe  \nproj-[0-9]+\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(pats) != 3 {
		t.Fatalf("got %d patterns, want 3", len(pats))
	}
	if !pats[0].MatchString("ACME-Corp") || !pats[1].MatchString("x /users/JDOE/y") || !pats[2].MatchString("proj-42") {
		t.Fatalf("patterns %v do not match case-insensitively", pats)
	}
}

func TestPatternsRejectABadExpression(t *testing.T) {
	_, err := parsePatterns("ok\n(unclosed\n")
	if err == nil || !strings.Contains(err.Error(), "pattern 2") || strings.Contains(err.Error(), "unclosed") {
		t.Fatalf("err %v, want one naming pattern 2 without its text", err)
	}
}

func TestScanReportsLineAndPatternNumberNotTheText(t *testing.T) {
	pats, _ := parsePatterns("acme\nsecret-host\n")
	hits := scan("a.txt", []byte("clean\nwe ship for ACME\nclean\nssh secret-host and acme\n"), pats)
	want := []hit{{"a.txt", 2, 1}, {"a.txt", 4, 1}, {"a.txt", 4, 2}}
	if !reflect.DeepEqual(hits, want) {
		t.Fatalf("hits %v, want %v", hits, want)
	}
	for _, h := range hits {
		if strings.Contains(strings.ToLower(h.String()), "acme") {
			t.Fatalf("%q repeats the matched text", h)
		}
	}
}

func TestScanSkipsBinaryContent(t *testing.T) {
	pats, _ := parsePatterns("acme\n")
	if hits := scan("a.png", []byte("\x89PNG\x00acme"), pats); len(hits) != 0 {
		t.Fatalf("hits %v in a binary file", hits)
	}
}

func TestScanPathsWalksDirectoriesAndSkipsGit(t *testing.T) {
	root := t.TempDir()
	write(t, root, "ev/1.txt", "acme\n")
	write(t, root, "ev/deep/2.txt", "fine\nAcme too\n")
	write(t, root, "ev/.git/config", "acme\n")
	write(t, root, "ev/deep/.git", "gitdir: /acme\n")
	write(t, root, "other.txt", "acme\n")
	pats, _ := parsePatterns("acme\n")
	hits, err := scanPaths([]string{filepath.Join(root, "ev")}, pats)
	if err != nil {
		t.Fatal(err)
	}
	want := []hit{{filepath.Join(root, "ev/1.txt"), 1, 1}, {filepath.Join(root, "ev/deep/2.txt"), 2, 1}}
	if !reflect.DeepEqual(hits, want) {
		t.Fatalf("hits %v, want %v", hits, want)
	}
}

func TestCheckPrivatePassesWithANoticeWithoutPatterns(t *testing.T) {
	summary, problems, err := checkPrivate("# only a comment\n", []string{t.TempDir()})
	if err != nil || len(problems) != 0 || !strings.Contains(summary, "not set") {
		t.Fatalf("summary %q problems %v err %v", summary, problems, err)
	}
}

func TestCheckPrivateMakesAProblemOfEveryHit(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.txt", "acme\nfine\nAcme\n")
	summary, problems, err := checkPrivate("acme\n", []string{root})
	want := []string{filepath.Join(root, "a.txt") + ":1 matches private pattern 1", filepath.Join(root, "a.txt") + ":3 matches private pattern 1"}
	if err != nil || summary != "private: 1 patterns, 2 matching lines" || !reflect.DeepEqual(problems, want) {
		t.Fatalf("summary %q problems %v err %v", summary, problems, err)
	}
}

func TestTrackedFilesListsOnlyWhatGitTracks(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	root := t.TempDir()
	write(t, root, "tracked.txt", "x\n")
	write(t, root, "sub/also.txt", "x\n")
	write(t, root, "untracked.txt", "x\n")
	for _, args := range [][]string{{"init", "-q"}, {"add", "tracked.txt", "sub/also.txt"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	files, err := trackedFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(root, "sub/also.txt"), filepath.Join(root, "tracked.txt")}
	if !reflect.DeepEqual(files, want) {
		t.Fatalf("files %v, want %v", files, want)
	}
}

func TestTheListComesFromTheVariableFirst(t *testing.T) {
	common := t.TempDir()
	write(t, common, "info/hq-private-patterns", "from-file\n")
	list, source, err := privateList("from-env\n", common)
	if err != nil || list != "from-env\n" || source != PrivatePatternsEnv {
		t.Fatalf("list %q source %q err %v", list, source, err)
	}
}

func TestTheListComesFromTheFileInGitWithoutTheVariable(t *testing.T) {
	common := t.TempDir()
	write(t, common, "info/hq-private-patterns", "from-file\n")
	list, source, err := privateList(" \n", common)
	if err != nil || list != "from-file\n" || source != filepath.Join(common, "info/hq-private-patterns") {
		t.Fatalf("list %q source %q err %v", list, source, err)
	}
}

func TestWithoutVariableOrFileThereIsNoList(t *testing.T) {
	list, source, err := privateList("", t.TempDir())
	if err != nil || list != "" || source != "" {
		t.Fatalf("list %q source %q err %v", list, source, err)
	}
	if list, source, err = privateList("", ""); err != nil || list != "" || source != "" { // not in a repository
		t.Fatalf("outside a repository: list %q source %q err %v", list, source, err)
	}
}

// pushLog is git log's output for two commits as prepush asks for it, written
// out by hand from a real run: each commit's full message between \x01 and
// \x02, then its patch with no context lines.
const pushLog = "\x01" + "8eec91d1dfd234a2b33de75ba0cf9dd68f90b46c\n" +
	"Ship it\n\nfor ACME as promised\n" + "\x02\n\n" +
	"diff --git d/acme notes.txt d/acme notes.txt\n" +
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
	"diff --git i.png i.png\n" +
	"new file mode 100644\n" +
	"index 0000000..e524aef\n" +
	"Binary files /dev/null and i.png differ\n" +
	"\x01" + "1234567aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n" +
	"Clean\n" + "\x02\n\n" +
	"diff --git gone.txt gone.txt\n" +
	"deleted file mode 100644\n" +
	"index 1111111..0000000\n" +
	"--- gone.txt\n" +
	"+++ /dev/null\n" +
	"@@ -1 +0,0 @@\n" +
	"-acme\n"

func TestScanLogFindsHitsInMessagesAddedLinesAndNewPathsOnly(t *testing.T) {
	pats, _ := parsePatterns("acme\n")
	got := []string{}
	for _, h := range scanLog(pushLog, pats) {
		got = append(got, h.String())
	}
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
