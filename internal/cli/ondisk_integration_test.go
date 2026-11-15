//go:build integration

package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/update"
)

func TestAFileKeepsItsStampUntilItIsReplacedOrWritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hq")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	first, err := stampFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := stampFile(path); again != first {
		t.Fatalf("an unchanged file changed its stamp: %+v, %+v", first, again)
	}
	// hq update renames a new file over it: the same bytes, and even the
	// same time, are another file.
	if err := update.Replace(path, []byte("#!/bin/sh\necho 1\n")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, time.Unix(0, first.modified), time.Unix(0, first.modified)); err != nil {
		t.Fatal(err)
	}
	replaced, err := stampFile(path)
	if err != nil || replaced == first {
		t.Fatalf("a file renamed over it kept the stamp: %+v %v", replaced, err)
	}
	// Written anew in place, by an installer that copies over it.
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho 2\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, time.Unix(0, replaced.modified+int64(time.Second)), time.Unix(0, replaced.modified+int64(time.Second))); err != nil {
		t.Fatal(err)
	}
	if written, _ := stampFile(path); written == replaced {
		t.Fatalf("a file written in place kept the stamp: %+v", written)
	}
	if _, err := stampFile(filepath.Join(t.TempDir(), "none")); err == nil {
		t.Fatal("a missing file has a stamp")
	}
}

func TestGenerationOfAsksTheHqAtAPath(t *testing.T) {
	dir := t.TempDir()
	script := func(name, body string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		return path
	}
	answers := script("answers", `[ "$1" = __generation ] && echo 7 || exit 1`)
	before := script("before", `echo "hq: unknown command '$1' (see hq help)" >&2; exit 1`)
	odd := script("odd", `echo seven`)
	slow := script("slow", `sleep 5`)
	for _, tc := range []struct {
		name, exe string
		gen       int
		fails     bool
	}{
		{"an hq that answers", answers, 7, false},
		{"an hq from before generations", before, 0, false},
		{"an answer that is no generation", odd, 0, false},
		{"an hq that does not answer in time", slow, 0, true},
		{"no hq there", filepath.Join(dir, "none"), 0, true},
	} {
		gen, err := generationOf(tc.exe, 300*time.Millisecond)
		if gen != tc.gen || (err != nil) != tc.fails {
			t.Errorf("%s: %d, %v; want %d, failing %v", tc.name, gen, err, tc.gen, tc.fails)
		}
	}
}
