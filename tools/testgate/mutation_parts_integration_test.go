//go:build integration

package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// The runs in parts below run the tool, as two tests of
// mutation_integration_test.go do: six tests, seven runs (one test also
// runs over the whole tree); a run whose parts change nothing does not.

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

func TestMutationInOnePackageRunsOnlyItsMutants(t *testing.T) {
	r := newMutationRepo(t)
	r.change()
	// lib/lib.go as it was moves to lib/half.go, a rename with no line
	// changed, and Double goes to a new file
	if err := os.Remove(filepath.Join(r.dir, "lib/lib.go")); err != nil {
		t.Fatal(err)
	}
	write(t, r.dir, "lib/half.go", "package lib\n\n// Half halves x.\nfunc Half(x int) int { return x / 2 }\n")
	write(t, r.dir, "lib/extra.go", "package lib\n\n// Double doubles x.\nfunc Double(x int) int { return x * 2 }\n")
	tmp, cache := isolated(t)
	lines, failed, err := runPartsIn(t, r.dir, "main", io.Discard, "lib")
	if err != nil {
		t.Fatal(err)
	}
	head := r.git(r.dir, "rev-parse", "HEAD")
	want := []string{
		// the alive mutant of cmd/tool is not run, and none of the renamed
		// file's lines, which the run over the whole tree does not change
		"no part    cmd/tool/main.go",
		"mutation: 1 killed, 0 alive, 0 timed out, 0 not covered, 1 changed file in no part against main on " +
			head + " with uncommitted changes",
	}
	if !reflect.DeepEqual(lines, want) {
		t.Errorf("got\n%s\nwant\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
	if failed {
		t.Error("the run fails on a file in no part")
	}
	assertEmpty(t, "the temporary directory", tmp)
	assertEmpty(t, "the shared build cache", cache)
}

func TestMutationInAFilePartRunsOnlyThatFile(t *testing.T) {
	r := newMutationRepo(t)
	r.change()
	write(t, r.dir, "cmd/tool/other.go", "package main\n\n// small tells whether n is small.\nfunc small(n int) bool { return n < 5 }\n")
	write(t, r.dir, "cmd/tool/other_test.go", "package main\n\nimport \"testing\"\n\n"+
		"func TestSmall(t *testing.T) {\n\tif !small(1) || small(10) {\n\t\tt.Fatal(\"small\")\n\t}\n}\n")
	tmp, cache := isolated(t)
	lines, failed, err := runPartsIn(t, r.dir, "main", io.Discard, "cmd/tool/other.go")
	if err != nil {
		t.Fatal(err)
	}
	head := r.git(r.dir, "rev-parse", "HEAD")
	want := []string{
		"alive      cmd/tool/other.go:4:35 conditionals boundary",
		// main.go of the same package, with its alive mutant, and lib are in no part
		"no part    cmd/tool/main.go",
		"no part    lib/lib.go",
		"mutation: 1 killed, 1 alive, 0 timed out, 0 not covered, 2 changed files in no part against main on " +
			head + " with uncommitted changes",
	}
	if !reflect.DeepEqual(lines, want) {
		t.Errorf("got\n%s\nwant\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
	if !failed {
		t.Error("an alive mutant of the part passes")
	}
	assertEmpty(t, "the temporary directory", tmp)
	assertEmpty(t, "the shared build cache", cache)
}

func TestMutationInPartsRunsNoToolWithoutAPartToRun(t *testing.T) {
	r := newMutationRepo(t)
	r.change()
	tmp, cache := isolated(t)
	// A part that does not exist: refused before lib runs.
	if _, _, err := runPartsIn(t, r.dir, "main", io.Discard, "lib", "cmd/nothing"); err == nil ||
		err.Error() != "the part cmd/nothing does not exist" {
		t.Errorf("err %v; want the missing part named", err)
	}
	// user holds only a test, lib/lib_test.go is one: no part changes a
	// production file, and cmd/tool/main.go and lib/lib.go are in no part.
	lines, failed, err := runPartsIn(t, r.dir, "main", io.Discard, "user", "lib/lib_test.go")
	head := r.git(r.dir, "rev-parse", "HEAD")
	want := []string{"no part    cmd/tool/main.go", "no part    lib/lib.go",
		"mutation: no mutants in the lines changed, 2 changed files in no part against main on " + head + " with uncommitted changes"}
	if err != nil || failed || !reflect.DeepEqual(lines, want) {
		t.Errorf("got %q, failed %v, err %v\nwant %q", lines, failed, err, want)
	}
	assertEmpty(t, "the temporary directory", tmp) // nothing staged, no tool run
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

func TestMutationInPartsInterruptedLeavesNothingBehind(t *testing.T) {
	r := newMutationRepo(t)
	// A slow unit suite, so the tool is still running when the run is cancelled.
	write(t, r.dir, "lib/slow_test.go", "package lib\n\nimport (\n\t\"testing\"\n\t\"time\"\n)\n\n"+
		"func TestSlow(t *testing.T) { time.Sleep(30 * time.Second) }\n")
	r.change()
	tmp, cache := isolated(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := runMutationParts(ctx, r.dir, "main", []string{"user", "lib"}, io.Discard, io.Discard)
		done <- err
	}()
	deadline := time.Now().Add(time.Minute)
	for {
		if dirs, _ := filepath.Glob(filepath.Join(tmp, "hq-mutation-*", "part-*", "cache")); len(dirs) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the part lib did not start within a minute")
		}
		time.Sleep(100 * time.Millisecond)
	}
	time.Sleep(time.Second) // the tool's first run of the tests is under way
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, errInterrupted) {
			t.Errorf("err %v, want %v", err, errInterrupted)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the run did not stop within 20 s of being cancelled")
	}
	assertEmpty(t, "the temporary directory", tmp)
	assertEmpty(t, "the shared build cache", cache)
}

// A file of the merge base is deleted; one new file is its exact copy (the
// rename git finds over the whole tree) and another new file, in another
// part, is the same text with one function more: the whole tree counts
// every line of the second as new, and so does its part.
func TestAPartJudgesACopyOfAMovedFileAsTheWholeTreeDoes(t *testing.T) {
	r := newMutationRepo(t)
	old := "package shape\n\n" +
		"// Area gives the area.\nfunc Area(w, h int) int { return w * h }\n\n" +
		"// Perimeter gives the perimeter.\nfunc Perimeter(w, h int) int { return 2 * (w + h) }\n\n" +
		"// Bigger tells whether a is bigger.\nfunc Bigger(a, b int) bool { return a > b }\n"
	r.git(r.dir, "checkout", "-q", "main")
	r.commit("old/old.go", old, "Old")
	r.git(r.dir, "checkout", "-q", "-B", "work", "main")
	if err := os.Remove(filepath.Join(r.dir, "old/old.go")); err != nil {
		t.Fatal(err)
	}
	write(t, r.dir, "p1/b.go", old)
	write(t, r.dir, "p2/c.go", old+"\n// Twice doubles x.\nfunc Twice(x int) int { return x * 2 }\n")
	isolated(t)
	whole, _, err := runMutationIn(t, r.dir, "main")
	if err != nil {
		t.Fatal(err)
	}
	parts, _, err := runPartsIn(t, r.dir, "main", io.Discard, "p1", "p2")
	if err != nil {
		t.Fatal(err)
	}
	w, p := whole[len(whole)-1], parts[len(parts)-1]
	if strings.Replace(p, ", 0 changed files in no part", "", 1) != w {
		t.Errorf("the counts differ:\nwhole tree: %s\nin parts:   %s", w, p)
	}
}
