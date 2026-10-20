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
	if _, err := parsePatterns("ok\n(unclosed\n"); err == nil || !strings.Contains(err.Error(), "pattern 2") {
		t.Fatalf("err %v, want one naming pattern 2", err)
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
