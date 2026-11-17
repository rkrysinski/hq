//go:build integration

package main

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The runs in parts below run the tool, as two tests of
// mutation_integration_test.go do; a run whose parts change nothing does
// not.

func runPartsIn(t *testing.T, dir, base string, progress io.Writer, paths ...string) ([]string, bool, error) {
	t.Helper()
	var out bytes.Buffer
	failed, err := runMutationParts(context.Background(), dir, base, paths, &out, progress)
	return strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n"), failed, err
}

// partWatch is the progress of a run in parts: when a part starts, it
// notes which directories of earlier parts are still in the run's
// temporary directory, under tmp.
type partWatch struct {
	t        *testing.T
	tmp      string
	started  int
	leftOver []string
}

func (w *partWatch) Write(p []byte) (int, error) {
	for _, line := range strings.Split(string(p), "\n") {
		if !strings.HasPrefix(line, "mutation: part ") {
			continue
		}
		w.started++
		dirs, err := filepath.Glob(filepath.Join(w.tmp, "hq-mutation-*", "part-*"))
		if err != nil {
			w.t.Error(err)
		}
		w.leftOver = append(w.leftOver, dirs...)
	}
	return len(p), nil
}

func TestMutationInPartsJudgesAsTheRunOverTheWholeTree(t *testing.T) {
	r := newMutationRepo(t)
	r.change()
	tmp, cache := isolated(t)
	watch := &partWatch{t: t, tmp: tmp}
	lines, failed, err := runPartsIn(t, r.dir, "main", watch, "cmd/tool", "lib")
	if err != nil {
		t.Fatal(err)
	}
	head := r.git(r.dir, "rev-parse", "HEAD")
	// TestMutationJudgesTheChangedLinesWithTheWholeUnitSuite's lines
	want := []string{
		"alive      cmd/tool/main.go:6:33 conditionals boundary",
		"mutation: 2 killed, 1 alive, 0 timed out, 1 not covered, 0 changed files in no part against main on " +
			head + " with uncommitted changes",
	}
	if !reflect.DeepEqual(lines, want) {
		t.Errorf("got\n%s\nwant\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
	if !failed {
		t.Error("an alive mutant passes")
	}
	if watch.started != 2 || len(watch.leftOver) > 0 {
		t.Errorf("%d parts started, with %q of earlier parts left; want 2 with nothing left", watch.started, watch.leftOver)
	}
	assertEmpty(t, "the temporary directory", tmp)
	assertEmpty(t, "the shared build cache", cache)
}

func TestMutationInAFilePartRunsOnlyThatFile(t *testing.T) {
	r := newMutationRepo(t)
	r.change()
	write(t, r.dir, "lib/extra.go", "package lib\n\n// Triple triples x.\nfunc Triple(x int) int { return x * 3 }\n")
	tmp, cache := isolated(t)
	lines, failed, err := runPartsIn(t, r.dir, "main", io.Discard, "lib/lib.go")
	if err != nil {
		t.Fatal(err)
	}
	head := r.git(r.dir, "rev-parse", "HEAD")
	want := []string{
		// the alive mutant of cmd/tool is not run, and lib/extra.go of the
		// same package is in no part as well
		"no part    cmd/tool/main.go",
		"no part    lib/extra.go",
		"mutation: 1 killed, 0 alive, 0 timed out, 0 not covered, 2 changed files in no part against main on " +
			head + " with uncommitted changes",
	}
	if !reflect.DeepEqual(lines, want) {
		t.Errorf("got\n%s\nwant\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
	if failed {
		t.Error("the run fails on files in no part")
	}
	assertEmpty(t, "the temporary directory", tmp)
	assertEmpty(t, "the shared build cache", cache)
}

func TestMutationInPartsThatChangeNothing(t *testing.T) {
	r := newMutationRepo(t)
	r.change()
	tmp, cache := isolated(t)
	// user holds only a test; cmd/tool/main.go stays in no part
	lines, failed, err := runPartsIn(t, r.dir, "main", io.Discard, "user", "lib/lib_test.go")
	head := r.git(r.dir, "rev-parse", "HEAD")
	want := []string{"no part    cmd/tool/main.go", "no part    lib/lib.go",
		"mutation: no mutants in the lines changed, 2 changed files in no part against main on " + head + " with uncommitted changes"}
	if err != nil || failed || !reflect.DeepEqual(lines, want) {
		t.Errorf("got %q, failed %v, err %v\nwant %q", lines, failed, err, want)
	}
	assertEmpty(t, "the temporary directory", tmp) // no part ran the tool
	assertEmpty(t, "the shared build cache", cache)
}

func TestMutationInPartsEndsWithAPartThatFails(t *testing.T) {
	r := newMutationRepo(t)
	r.change()
	write(t, r.dir, "lib/fail_test.go", "package lib\n\nimport \"testing\"\n\nfunc TestFail(t *testing.T) { t.Fatal(\"fails\") }\n")
	tmp, cache := isolated(t)
	lines, failed, err := runPartsIn(t, r.dir, "main", io.Discard, "user", "lib", "cmd/tool")
	if err == nil || !strings.Contains(err.Error(), "the part lib: go-gremlins failed") {
		t.Errorf("err %v; want the part lib's failure", err)
	}
	if failed || !reflect.DeepEqual(lines, []string{""}) {
		t.Errorf("printed %q, failed %v; want nothing", lines, failed)
	}
	assertEmpty(t, "the temporary directory", tmp)
	assertEmpty(t, "the shared build cache", cache)
}

func TestMutationRefusesAPartBeforeRunningAny(t *testing.T) {
	r := newMutationRepo(t)
	r.change()
	tmp, cache := isolated(t)
	_, _, err := runPartsIn(t, r.dir, "main", io.Discard, "lib", "cmd/nothing")
	if err == nil || err.Error() != "the part cmd/nothing does not exist" {
		t.Errorf("err %v; want the missing part named", err)
	}
	assertEmpty(t, "the temporary directory", tmp)
	assertEmpty(t, "the shared build cache", cache)
}
