package main

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// gremlinsReport is the report the pinned go-gremlins wrote with --output for
// the module of the integration test, copied from a real run.
const gremlinsReport = `{"go_module":"example.com/m","files":[` +
	`{"file_name":"cmd/tool/main.go","mutations":[` +
	`{"type":"ARITHMETIC_BASE","status":"NOT COVERED","line":8,"column":35},` +
	`{"type":"CONDITIONALS_BOUNDARY","status":"LIVED","line":6,"column":33},` +
	`{"type":"CONDITIONALS_NEGATION","status":"KILLED","line":6,"column":33}]},` +
	`{"file_name":"lib/lib.go","mutations":[` +
	`{"type":"ARITHMETIC_BASE","status":"SKIPPED","line":4,"column":33},` +
	`{"type":"ARITHMETIC_BASE","status":"KILLED","line":7,"column":35}]}],` +
	`"test_efficacy":66.66666666666666,"mutations_coverage":75,"mutants_total":3,"mutants_killed":2,` +
	`"mutants_lived":1,"mutants_not_viable":0,"mutants_not_covered":1,"elapsed_time":0.198675042,` +
	`"mutator_statistics":{"arithmetic_base":3,"conditionals_negation":1,"conditionals_boundary":1}}`

const head = "0123456789abcdef0123456789abcdef01234567"

func TestParseGremlinsReport(t *testing.T) {
	ms, err := parseGremlinsReport(strings.NewReader(gremlinsReport))
	if err != nil {
		t.Fatal(err)
	}
	want := []mutant{
		{"cmd/tool/main.go", 8, 35, "ARITHMETIC_BASE", "NOT COVERED"},
		{"cmd/tool/main.go", 6, 33, "CONDITIONALS_BOUNDARY", "LIVED"},
		{"cmd/tool/main.go", 6, 33, "CONDITIONALS_NEGATION", "KILLED"},
		{"lib/lib.go", 4, 33, "ARITHMETIC_BASE", "SKIPPED"},
		{"lib/lib.go", 7, 35, "ARITHMETIC_BASE", "KILLED"},
	}
	if !reflect.DeepEqual(ms, want) {
		t.Errorf("got %v\nwant %v", ms, want)
	}
	if _, err := parseGremlinsReport(strings.NewReader("Killed: 3")); err == nil {
		t.Error("a report that is not JSON is read without an error")
	}
}

func TestMutationReportNamesEveryMutantButTheKilledInOrder(t *testing.T) {
	ms := []mutant{
		{"b.go", 3, 1, "ARITHMETIC_BASE", "TIMED OUT"},
		{"a.go", 9, 2, "CONDITIONALS_NEGATION", "LIVED"},
		{"a.go", 9, 2, "CONDITIONALS_BOUNDARY", "LIVED"},
		{"a.go", 10, 1, "INVERT_NEGATIVES", "KILLED"},
		{"a.go", 9, 1, "INCREMENT_DECREMENT", "LIVED"},
		{"a.go", 12, 4, "ARITHMETIC_BASE", "NOT VIABLE"},
		{"a.go", 2, 5, "ARITHMETIC_BASE", "LIVED"},
		{"c.go", 1, 1, "ARITHMETIC_BASE", "NOT COVERED"},
		{"d.go", 1, 1, "ARITHMETIC_BASE", "SKIPPED"},
	}
	lines, failed, err := mutationReport(ms, nil, "origin/dev", head)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"alive      a.go:2:5 arithmetic base",
		"alive      a.go:9:1 increment decrement",
		"alive      a.go:9:2 conditionals boundary",
		"alive      a.go:9:2 conditionals negation",
		"timed out  b.go:3:1 arithmetic base",
		"not covered c.go:1:1 arithmetic base",
		"mutation: 2 killed, 4 alive, 1 timed out, 1 not covered, 0 not run against origin/dev on " + head,
	}
	if !reflect.DeepEqual(lines, want) {
		t.Errorf("got\n%s\nwant\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
	if !failed {
		t.Error("a run with alive mutants passes")
	}
}

func TestMutationReportFailsOnATimeoutAlone(t *testing.T) {
	_, failed, err := mutationReport([]mutant{{"a.go", 1, 1, "ARITHMETIC_BASE", "TIMED OUT"}}, nil, "origin/dev", head)
	if err != nil || !failed {
		t.Errorf("failed %v, err %v; want a failed run", failed, err)
	}
}

func TestMutationReportPassesWithKilledAndNotCovered(t *testing.T) {
	ms := []mutant{
		{"a.go", 1, 1, "ARITHMETIC_BASE", "KILLED"},
		{"a.go", 2, 1, "ARITHMETIC_BASE", "NOT COVERED"},
		{"a.go", 3, 1, "ARITHMETIC_BASE", "SKIPPED"},
	}
	lines, failed, err := mutationReport(ms, nil, "release-v1.2.0", head+" with uncommitted changes")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"not covered a.go:2:1 arithmetic base",
		"mutation: 1 killed, 0 alive, 0 timed out, 1 not covered, 0 not run against release-v1.2.0 on " + head + " with uncommitted changes",
	}
	if !reflect.DeepEqual(lines, want) || failed {
		t.Errorf("got %q, failed %v; want %q, passed", lines, failed, want)
	}
}

func TestMutationReportWithoutMutantsInTheChangedLines(t *testing.T) {
	want := []string{"mutation: no mutants in the lines changed, 0 not run against origin/dev on " + head}
	for _, ms := range [][]mutant{nil, {{"a.go", 3, 1, "ARITHMETIC_BASE", "SKIPPED"}}} {
		lines, failed, err := mutationReport(ms, nil, "origin/dev", head)
		if err != nil || failed || !reflect.DeepEqual(lines, want) {
			t.Errorf("%v: got %q, failed %v, err %v; want %q", ms, lines, failed, err, want)
		}
	}
}

func TestMutationReportFailsOnALineNotRunAlone(t *testing.T) {
	for _, c := range []struct {
		ms   []mutant
		want []string
	}{
		{nil, []string{"not run    a.go:7", "mutation: no mutants in the lines changed, 1 not run against origin/dev on " + head}},
		{[]mutant{{"a.go", 7, 3, "ARITHMETIC_BASE", "KILLED"}, {"a.go", 2, 1, "ARITHMETIC_BASE", "NOT COVERED"}}, []string{
			"not covered a.go:2:1 arithmetic base",
			"not run    a.go:7",
			"mutation: 1 killed, 0 alive, 0 timed out, 1 not covered, 1 not run against origin/dev on " + head}},
	} {
		lines, failed, err := mutationReport(c.ms, []lineRef{{"a.go", 7}}, "origin/dev", head)
		if err != nil || !failed || !reflect.DeepEqual(lines, c.want) {
			t.Errorf("got %q, failed %v, err %v\nwant %q, failed", lines, failed, err, c.want)
		}
	}
}

func TestMutationReportRefusesAnUnknownStatus(t *testing.T) {
	_, _, err := mutationReport([]mutant{{"a.go", 1, 1, "ARITHMETIC_BASE", "RUNNABLE"}}, nil, "origin/dev", head)
	if err == nil || !strings.Contains(err.Error(), `"RUNNABLE"`) {
		t.Errorf("err %v; want one naming the status", err)
	}
}

func TestHasMutableGo(t *testing.T) {
	for _, c := range []struct {
		paths []string
		want  bool
	}{
		{nil, false},
		{[]string{""}, false},
		{[]string{"README.md", "docs/hq/testing.md", "scripts/check.sh", ".github/workflows/tests.yml"}, false},
		{[]string{"internal/cli/start_test.go", "internal/cli/testdata/x.txt", "go.mod", "go.sum", "VERSION"}, false},
		{[]string{"README.md", "internal/cli/start.go"}, true},
		{[]string{"cmd/hq/main.go"}, true},
	} {
		if got := hasMutableGo(c.paths); got != c.want {
			t.Errorf("hasMutableGo(%q) = %v, want %v", c.paths, got, c.want)
		}
	}
}

func TestMutationEnvKeepsEverythingOfTheRunInItsDirectory(t *testing.T) {
	env := mutationEnv([]string{
		"PATH=/usr/bin", "HOME=/home/u", "GOPROXY=https://proxy.example",
		"GOCACHE=/home/u/.cache/go-build", "GOFLAGS=-tags=integration", "GOBIN=/home/u/bin",
		"TMPDIR=/var/tmp", "GOTMPDIR=/var/tmp/go", "NO_COLOR=",
		"GIT_DIR=/w/app/.git", "GIT_INDEX_FILE=/w/app/.git/index", "GREMLINS_UNLEASH_WORKERS=1",
		"GOCOVERDIR=/home/u/cov", "HQ_MUTATION_COVERDIR=/t/outer/counters",
	}, "/t/run")
	want := []string{
		"PATH=/usr/bin", "HOME=/home/u", "GOPROXY=https://proxy.example",
		"GOCACHE=/t/run/cache", "GOBIN=/t/run/bin", "TMPDIR=/t/run/tmp", "GOFLAGS=", "NO_COLOR=1",
	}
	if !reflect.DeepEqual(env, want) {
		t.Errorf("got %q\nwant %q", env, want)
	}
}

func TestGremlinsArgs(t *testing.T) {
	args := gremlinsArgs("/t/g.yaml", "/t/r.json", "abc123")
	want := []string{"unleash", "--config", "/t/g.yaml", "--integration", "--coverpkg", "./...",
		"--diff", "abc123", "--timeout-coefficient", "10", "--output", "/t/r.json"}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("got %q\nwant %q", args, want)
	}
}

func TestGremlinsConfigEnablesTheFiveDefaultKinds(t *testing.T) {
	var on []string
	for _, l := range strings.Split(gremlinsConfig, "\n") {
		if name, ok := strings.CutSuffix(strings.TrimSpace(l), ": {enabled: true}"); ok {
			on = append(on, name)
		}
	}
	want := []string{"arithmetic-base", "conditionals-boundary", "conditionals-negation", "increment-decrement", "invert-negatives"}
	if !slices.Equal(on, want) {
		t.Errorf("enabled %q, want %q", on, want)
	}
}

func TestCopyEntry(t *testing.T) {
	from, to := t.TempDir(), t.TempDir()
	write(t, from, "a/run.sh", "#!/bin/sh\n")
	if err := os.Chmod(filepath.Join(from, "a/run.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("run.sh", filepath.Join(from, "a/link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(from, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"a/run.sh", "a/link", "gone.go", "sub"} {
		if err := copyEntry(filepath.Join(from, rel), filepath.Join(to, rel)); err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
	}
	info, err := os.Stat(filepath.Join(to, "a/run.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Errorf("mode %v, want 0755", info.Mode().Perm())
	}
	if body, _ := os.ReadFile(filepath.Join(to, "a/run.sh")); string(body) != "#!/bin/sh\n" {
		t.Errorf("content %q", body)
	}
	if target, err := os.Readlink(filepath.Join(to, "a/link")); err != nil || target != "run.sh" {
		t.Errorf("link %q, %v; want a symlink to run.sh", target, err)
	}
	for _, rel := range []string{"gone.go", "sub"} {
		if _, err := os.Lstat(filepath.Join(to, rel)); err == nil {
			t.Errorf("%s was copied", rel)
		}
	}
}
