package cli

import (
	"errors"
	"testing"

	"github.com/rkrysinski/hq/internal/version"
)

func TestAnHqOnDiskLeftAsItWasIsNeverAsked(t *testing.T) {
	f := newFakes()
	w := watchOnDisk(f.deps())
	for range 5 {
		if w.otherGeneration() {
			t.Fatal("hq left as it was taken for another generation")
		}
	}
	if f.generationAsks != 0 {
		t.Fatalf("the hq on disk was asked %d times; no process is started while it is unchanged", f.generationAsks)
	}
}

func TestAnHqOfTheSameGenerationOnDiskIsAskedOnce(t *testing.T) {
	f := newFakes()
	w := watchOnDisk(f.deps())
	f.disk.ino++
	for range 3 {
		if w.otherGeneration() {
			t.Fatal("an update of the same generation taken for another")
		}
	}
	if f.generationAsks != 1 {
		t.Fatalf("asked %d times, want once for the one change", f.generationAsks)
	}
	// Another update of the same generation is asked about once again.
	f.disk.modified++
	w.otherGeneration()
	if f.generationAsks != 2 {
		t.Fatalf("asked %d times after a second change, want 2", f.generationAsks)
	}
}

func TestAnHqOfAnotherGenerationOnDiskStaysSo(t *testing.T) {
	for name, gen := range map[string]int{"newer": version.Generation + 1, "older": version.Generation - 1, "from before generations": 0} {
		t.Run(name, func(t *testing.T) {
			f := newFakes()
			w := watchOnDisk(f.deps())
			f.disk.size++
			f.diskGeneration = gen
			if !w.otherGeneration() {
				t.Fatal("an update to another generation not noticed")
			}
			// Put back as it was, it is still another hq than this one
			// was: once noticed, it stays.
			f.disk.size--
			f.diskGeneration = version.Generation
			if !w.otherGeneration() || f.generationAsks != 1 {
				t.Fatalf("forgotten, or asked again (%d asks)", f.generationAsks)
			}
		})
	}
}

func TestAnHqOnDiskThatCannotBeAskedIsAskedAgainLater(t *testing.T) {
	f := newFakes()
	w := watchOnDisk(f.deps())
	f.disk.ino++
	f.diskGeneration = version.Generation + 1
	f.generationErr = errors.New("no answer within 5s")
	if w.otherGeneration() {
		t.Fatal("an hq that did not answer taken for another generation")
	}
	f.generationErr = nil
	if !w.otherGeneration() || f.generationAsks != 2 {
		t.Fatalf("not asked again (%d asks)", f.generationAsks)
	}
}

func TestAMissingHqOnDiskSaysNothingYet(t *testing.T) {
	f := newFakes()
	w := watchOnDisk(f.deps())
	f.diskErr = errors.New("no such file")
	if w.otherGeneration() || f.generationAsks != 0 {
		t.Fatalf("a missing hq was asked (%d asks)", f.generationAsks)
	}
	// Back, of another generation: noticed then.
	f.diskErr, f.disk.ino, f.diskGeneration = nil, 9, version.Generation+1
	if !w.otherGeneration() {
		t.Fatal("the hq put back not noticed")
	}
}

func TestWithoutItsOwnPathHqWatchesNothing(t *testing.T) {
	f := newFakes()
	d := f.deps()
	d.executable = func() (string, error) { return "", errors.New("no /proc") }
	w := watchOnDisk(d)
	f.disk.ino++
	f.diskGeneration = version.Generation + 1
	if w.otherGeneration() || f.generationAsks != 0 {
		t.Fatalf("watched without a path (%d asks)", f.generationAsks)
	}
}
