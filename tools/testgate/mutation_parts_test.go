package main

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// partsRepo is a directory laid out like a repository's working tree, with
// the list of its files git tracks or would track.
func partsRepo(t *testing.T) (top string, files []string) {
	t.Helper()
	top = t.TempDir()
	files = []string{"go.mod", "main.go", "lib/lib.go", "lib/lib_test.go", "lib/doc.txt",
		"lib/deep/deep.go", "cmd/tool/main.go", "tests/only_test.go"}
	for _, f := range files {
		write(t, top, f, "package x\n")
	}
	write(t, top, "ignored/ignored.go", "package ignored\n") // on disk, not in the list
	write(t, top, "notes/notes.md", "notes\n")
	return top, files
}

func TestADirectoryPartCoversTheGoFilesDirectlyInIt(t *testing.T) {
	top, files := partsRepo(t)
	parts, err := resolveParts(top, top, []string{"lib", "./cmd/tool/", "tests", "."}, files)
	if err != nil {
		t.Fatal(err)
	}
	want := []part{
		{"lib", []string{"lib/lib.go", "lib/lib_test.go"}}, // not lib/deep/deep.go
		{"./cmd/tool/", []string{"cmd/tool/main.go"}},
		{"tests", []string{"tests/only_test.go"}},
		{".", []string{"main.go"}},
	}
	if !reflect.DeepEqual(parts, want) {
		t.Errorf("got %q\nwant %q", parts, want)
	}
}

func TestAFilePartCoversThatFileAlone(t *testing.T) {
	top, files := partsRepo(t)
	parts, err := resolveParts(top, filepath.Join(top, "lib"), []string{"lib.go", "../cmd/tool/main.go"}, files)
	if err != nil {
		t.Fatal(err)
	}
	want := []part{{"lib.go", []string{"lib/lib.go"}}, {"../cmd/tool/main.go", []string{"cmd/tool/main.go"}}}
	if !reflect.DeepEqual(parts, want) {
		t.Errorf("got %q\nwant %q", parts, want)
	}
}

func TestAPartThatIsNoGoCodeOfTheRepositoryIsAnError(t *testing.T) {
	top, files := partsRepo(t)
	outside := t.TempDir()
	write(t, outside, "x.go", "package x\n")
	if err := os.Symlink(outside, filepath.Join(top, "link")); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ path, want string }{
		{"nothing", "the part nothing does not exist"},
		{"lib/gone.go", "the part lib/gone.go does not exist"},
		{outside, "the part " + outside + " lies outside the repository"},
		{"../" + filepath.Base(outside), "the part ../" + filepath.Base(outside) + " lies outside the repository"},
		{"link/x.go", "the part link/x.go lies outside the repository"},
		{"notes", "the part notes holds no Go file of the repository"},
		{"ignored", "the part ignored holds no Go file of the repository"},
		{"lib/doc.txt", "the part lib/doc.txt is not a Go file of the repository"},
		{"ignored/ignored.go", "the part ignored/ignored.go is not a Go file of the repository"},
	} {
		parts, err := resolveParts(top, top, []string{"lib", c.path}, files)
		if err == nil || err.Error() != c.want {
			t.Errorf("%s: parts %q, err %v; want one saying %q", c.path, parts, err, c.want)
		}
	}
}

func TestAFileTwoPartsCoverIsAnError(t *testing.T) {
	top, files := partsRepo(t)
	for _, c := range []struct {
		paths []string
		want  string
	}{
		{[]string{"lib", "cmd/tool", "lib/lib.go"}, "the parts lib and lib/lib.go both cover lib/lib.go"},
		{[]string{"lib/lib_test.go", "lib"}, "the parts lib/lib_test.go and lib both cover lib/lib_test.go"},
		{[]string{"cmd/tool", "./cmd/tool/"}, "the parts cmd/tool and ./cmd/tool/ both cover cmd/tool/main.go"},
	} {
		parts, err := resolveParts(top, top, c.paths, files)
		if err == nil || err.Error() != c.want {
			t.Errorf("%q: parts %q, err %v; want %q", c.paths, parts, err, c.want)
		}
	}
}

func TestChangedSourcesAreTheChangedGoFilesThatAreNoTests(t *testing.T) {
	files := []string{"go.mod", "a/a.go", "a/a_test.go", "b/b.go", "b/new.go", "c/c.go", "README.md"}
	// as changedPaths gives them: the diff, then the untracked files, and an
	// empty last entry; b/gone.go is deleted, so it is in no list of files
	changed := []string{"README.md", "a/a_test.go", "b/gone.go", "c/c.go", "a/a.go", "go.mod", "b/new.go", ""}
	want := []string{"a/a.go", "b/new.go", "c/c.go"}
	if got := changedSources(changed, files); !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestTheChangedSourcesInNoPartAreListedInPathOrder(t *testing.T) {
	sources := []string{"a/a.go", "b/b.go", "b/new.go", "c/c.go", "d/d.go"}
	parts := []part{{"c", []string{"c/c.go", "c/c_test.go"}}, {"b/b.go", []string{"b/b.go"}}, {"e", []string{"e/e.go"}}}
	if got, want := inNoPart(sources, parts), []string{"a/a.go", "b/new.go", "d/d.go"}; !reflect.DeepEqual(got, want) {
		t.Errorf("in no part: got %q, want %q", got, want)
	}
	if got := inNoPart(sources, []part{{".", sources}}); len(got) != 0 {
		t.Errorf("every source in a part: got %q in no part", got)
	}
	for _, c := range []struct {
		p    part
		want bool
	}{{parts[0], true}, {parts[1], true}, {parts[2], false}, {part{"c", []string{"c/c_test.go"}}, false}} {
		if got := c.p.coversAny(sources); got != c.want {
			t.Errorf("part %q changes a source: %v, want %v", c.p.path, got, c.want)
		}
	}
}

func TestPartsReportListsTheFilesInNoPartAfterTheSurvivors(t *testing.T) {
	ms := []mutant{
		{"b/b.go", 3, 1, "ARITHMETIC_BASE", "TIMED OUT"}, // of the second part
		{"a/a.go", 9, 2, "CONDITIONALS_BOUNDARY", "LIVED"},
		{"a/a.go", 10, 1, "INVERT_NEGATIVES", "KILLED"},
		{"b/b.go", 1, 1, "ARITHMETIC_BASE", "NOT COVERED"},
		{"z/z.go", 1, 1, "ARITHMETIC_BASE", "SKIPPED"},
	}
	lines, failed, err := partsReport(ms, []string{"c/c.go", "d/d.go"}, "origin/dev", head)
	want := []string{
		"alive      a/a.go:9:2 conditionals boundary",
		"timed out  b/b.go:3:1 arithmetic base",
		"no part    c/c.go",
		"no part    d/d.go",
		"mutation: 1 killed, 1 alive, 1 timed out, 1 not covered, 2 changed files in no part against origin/dev on " + head,
	}
	if err != nil || !failed || !reflect.DeepEqual(lines, want) {
		t.Errorf("got %q, failed %v, err %v\nwant %q", lines, failed, err, want)
	}
}

func TestPartsReportCountsTheFilesInNoPartAlsoWhenThereAreNone(t *testing.T) {
	ms := []mutant{{"a/a.go", 10, 1, "INVERT_NEGATIVES", "KILLED"}}
	for _, c := range []struct {
		noPart []string
		want   []string
	}{
		{nil, []string{"mutation: 1 killed, 0 alive, 0 timed out, 0 not covered, 0 changed files in no part against origin/dev on " + head}},
		{[]string{"c/c.go"}, []string{"no part    c/c.go",
			"mutation: 1 killed, 0 alive, 0 timed out, 0 not covered, 1 changed file in no part against origin/dev on " + head}},
	} {
		lines, failed, err := partsReport(ms, c.noPart, "origin/dev", head)
		if err != nil || failed || !reflect.DeepEqual(lines, c.want) {
			t.Errorf("got %q, failed %v, err %v\nwant %q", lines, failed, err, c.want)
		}
	}
}

func TestPartsReportWithoutMutantsStillCountsTheFilesInNoPart(t *testing.T) {
	for _, ms := range [][]mutant{nil, {{"a/a.go", 3, 1, "ARITHMETIC_BASE", "SKIPPED"}}} {
		lines, failed, err := partsReport(ms, []string{"c/c.go", "d/d.go"}, "origin/dev", head)
		want := []string{"no part    c/c.go", "no part    d/d.go",
			"mutation: no mutants in the lines changed, 2 changed files in no part against origin/dev on " + head}
		if err != nil || failed || !reflect.DeepEqual(lines, want) {
			t.Errorf("%v: got %q, failed %v, err %v\nwant %q", ms, lines, failed, err, want)
		}
	}
}

func TestMutationArgsAreTheBaseAndThePaths(t *testing.T) {
	for _, c := range []struct {
		args  []string
		base  string
		paths []string
	}{
		{[]string{"origin/dev"}, "origin/dev", nil},
		{[]string{"origin/dev", "internal/state", "internal/cli/cursor.go"}, "origin/dev", []string{"internal/state", "internal/cli/cursor.go"}},
	} {
		base, paths, err := mutationArgs(c.args)
		if err != nil || base != c.base || !slices.Equal(paths, c.paths) {
			t.Errorf("%q: base %q, paths %q, err %v; want %q, %q", c.args, base, paths, err, c.base, c.paths)
		}
	}
	if _, _, err := mutationArgs(nil); err == nil || !strings.Contains(err.Error(), "needs the base") {
		t.Errorf("no base: err %v, want one asking for the base", err)
	}
}
