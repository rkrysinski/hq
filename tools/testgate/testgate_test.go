package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCountsTestsPerLevelByBuildTag(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a/a_test.go", "package a\nimport \"testing\"\nfunc TestOne(t *testing.T) {}\nfunc TestTwo(t *testing.T) {}\nfunc helper(t *testing.T) {}\nfunc TestMain(m *testing.M) {}\n")
	write(t, root, "a/a_integration_test.go", "//go:build integration\n\npackage a\nimport \"testing\"\nfunc TestReal(t *testing.T) {}\n")
	write(t, root, "e2e/journey_test.go", "//go:build e2e\n\npackage e2e\nimport \"testing\"\nfunc TestJourney(t *testing.T) {}\n")
	c, err := countTests(root)
	if err != nil {
		t.Fatal(err)
	}
	if c.unit != 2 || c.integration != 1 || c.e2e != 1 {
		t.Fatalf("got unit %d integration %d e2e %d", c.unit, c.integration, c.e2e)
	}
}

func TestEndToEndOutsideE2EDirIsAProblem(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a/x_test.go", "//go:build e2e\n\npackage a\nimport \"testing\"\nfunc TestJourney(t *testing.T) {}\n")
	c, _ := countTests(root)
	if p := c.problems(0.5); len(p) == 0 || !strings.Contains(p[0], "e2e/") {
		t.Fatalf("problems %v", p)
	}
}

func TestShapeRules(t *testing.T) {
	for _, tc := range []struct {
		name string
		c    counts
		ok   bool
	}{
		{"unit only", counts{unit: 3}, true},
		{"healthy pyramid", counts{unit: 10, integration: 4, e2e: 1}, true},
		{"too many e2e", counts{unit: 10, integration: 1, e2e: 1}, false},
		{"unit share too low", counts{unit: 1, integration: 3}, false},
	} {
		if got := len(tc.c.problems(0.5)) == 0; got != tc.ok {
			t.Errorf("%s: ok %v, want %v (%v)", tc.name, got, tc.ok, tc.c.problems(0.5))
		}
	}
}

func TestCoverageMergesRepeatedBlocks(t *testing.T) {
	profile := "mode: set\nx.go:1.1,2.2 3 0\ny.go:1.1,2.2 1 1\nx.go:1.1,2.2 3 1\nz.go:1.1,2.2 4 0\n"
	pct, err := coverage(strings.NewReader(profile))
	if err != nil {
		t.Fatal(err)
	}
	if pct != 50 {
		t.Fatalf("got %.1f, want 50", pct)
	}
}
