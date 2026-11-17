//go:build integration

package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	// fakeClaudeChild has this test binary run as a package's tests that
	// build the fake claude.
	fakeClaudeChild = "HQ_TESTUTIL_FAKE_CLAUDE_CHILD"
	// withoutMain has TestMain run the tests without Main.
	withoutMain = "HQ_TESTUTIL_WITHOUT_MAIN"
)

// TestMain runs testutil's own tests through Main, as a package whose tests
// call FakeClaude does; with HQ_TESTUTIL_WITHOUT_MAIN set, as one that
// forgot to.
func TestMain(m *testing.M) {
	if os.Getenv(withoutMain) != "" {
		os.Exit(m.Run())
	}
	os.Exit(Main(m))
}

// A test binary removes the fake claude it built once its tests end, so a
// test run leaves no copy of it in the temporary directory; a binary whose
// TestMain does not run its tests through Main gets none (#226).
func TestTheFakeClaudeGoesWithTheTestsThatBuiltIt(t *testing.T) {
	if os.Getenv(fakeClaudeChild) != "" {
		FakeClaude(t)
		bin := os.Getenv("SBX_STUB_CLAUDE")
		if _, err := os.Stat(bin); err != nil {
			t.Fatal(err)
		}
		t.Logf("fake claude at %s", bin)
		return
	}
	// child runs this test as the child, with a temporary directory of its
	// own, and returns that directory and the child's output.
	child := func(t *testing.T, env ...string) (string, string, error) {
		tmp := t.TempDir()
		cmd := exec.Command(os.Args[0], "-test.run=^TestTheFakeClaudeGoesWithTheTestsThatBuiltIt$", "-test.v")
		cmd.Env = append(os.Environ(), append([]string{fakeClaudeChild + "=1", "TMPDIR=" + tmp}, env...)...)
		out, err := cmd.CombinedOutput()
		return tmp, string(out), err
	}
	wantEmpty := func(t *testing.T, tmp string) {
		t.Helper()
		entries, err := os.ReadDir(tmp)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			t.Errorf("left in the temporary directory: %s", e.Name())
		}
	}

	t.Run("run through Main", func(t *testing.T) {
		tmp, out, err := child(t)
		if err != nil {
			t.Fatalf("the child failed: %v\n%s", err, out)
		}
		_, bin, ok := strings.Cut(out, "fake claude at ")
		bin, _, _ = strings.Cut(bin, "\n")
		if !ok || !strings.HasPrefix(bin, tmp+string(filepath.Separator)) {
			t.Fatalf("no fake claude built in the child's temporary directory %s:\n%s", tmp, out)
		}
		wantEmpty(t, tmp)
	})

	t.Run("run without Main", func(t *testing.T) {
		tmp, out, err := child(t, withoutMain+"=1")
		if err == nil || !strings.Contains(out, "testutil.Main") {
			t.Fatalf("FakeClaude did not refuse, naming testutil.Main: %v\n%s", err, out)
		}
		wantEmpty(t, tmp)
	})
}
