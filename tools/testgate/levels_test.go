package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

// script writes an executable sh script with body into dir and returns its
// path.
func script(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// readLines reads the lines of a file a fake wrote.
func readLines(t *testing.T, file string) []string {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

func TestCoverArgsBuildWithCoverageOnlyForBuildAndRun(t *testing.T) {
	flags := []string{"-cover", "-covermode=set", "-coverpkg=example.com/m/..."}
	for _, c := range []struct {
		args, want []string
	}{
		{[]string{"build", "-o", "/t/hq", "./cmd/hq"}, slices.Concat([]string{"build"}, flags, []string{"-o", "/t/hq", "./cmd/hq"})},
		{[]string{"run", "./tools/testgate", "skips"}, slices.Concat([]string{"run"}, flags, []string{"./tools/testgate", "skips"})},
		{[]string{"test", "-count=1", "./..."}, []string{"test", "-count=1", "./..."}},
		{[]string{"env", "GOCACHE"}, []string{"env", "GOCACHE"}},
		{nil, nil},
	} {
		if got := coverArgs(c.args, "example.com/m"); !reflect.DeepEqual(got, c.want) {
			t.Errorf("coverArgs(%q) = %q, want %q", c.args, got, c.want)
		}
	}
}

func TestWithinTellsATreeFromWhatLiesOutsideIt(t *testing.T) {
	root := t.TempDir()
	top := filepath.Join(root, "src")
	for _, d := range []string{"src/lib/sub", "srcx", "other"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(top, "lib"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		dir  string
		want bool
	}{
		{top, true},
		{filepath.Join(top, "lib/sub"), true},
		{filepath.Join(root, "link"), true}, // a symlink into the tree
		{filepath.Join(root, "srcx"), false},
		{filepath.Join(root, "other"), false},
		{root, false},
		{filepath.Join(top, "missing"), false},
	} {
		if got := within(top, c.dir); got != c.want {
			t.Errorf("within(src, %s) = %v, want %v", c.dir, got, c.want)
		}
	}
	if within(filepath.Join(root, "missing"), top) {
		t.Error("a tree that does not exist holds a directory")
	}
}

func TestCoverGoAddsCoverageToABuildInTheTreeOnly(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	if err := os.MkdirAll(filepath.Join(src, "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	called := filepath.Join(dir, "called")
	realGo := script(t, dir, "go", `echo "$*" >> `+shellQuote(called)+"\nexit 3\n")
	t.Chdir(filepath.Join(src, "lib"))
	if code := coverGo([]string{realGo, src, "example.com/m", "build", "-o", "x", "."}); code != 3 {
		t.Errorf("exit %d, want the real go's 3", code)
	}
	t.Chdir(dir)
	coverGo([]string{realGo, src, "example.com/m", "build", "-o", "x", "."})
	want := []string{"build -cover -covermode=set -coverpkg=example.com/m/... -o x .", "build -o x ."}
	if got := readLines(t, called); !reflect.DeepEqual(got, want) {
		t.Errorf("the real go got %q, want %q", got, want)
	}
	if code := coverGo([]string{realGo, src}); code != 2 {
		t.Errorf("without the module: exit %d, want 2", code)
	}
}

func TestTestExecRunsTheTestBinaryWithTheRunsCoverageAndGo(t *testing.T) {
	dir := t.TempDir()
	seen := filepath.Join(dir, "seen")
	bin := script(t, dir, "x.test", `{ echo "$GOCOVERDIR"; echo "$`+envCoverDir+`"; echo "${PATH%%:*}"; echo "$*"; } > `+shellQuote(seen)+"\nexit 7\n")
	t.Setenv("GOCOVERDIR", "/t/go-test-own")
	if code := testExec([]string{"/t/counters", "/t/cover-go", bin, "-test.v=true", "-test.run=X"}); code != 7 {
		t.Errorf("exit %d, want the test binary's 7", code)
	}
	want := []string{"/t/counters", "/t/counters", "/t/cover-go", "-test.v=true -test.run=X"}
	if got := readLines(t, seen); !reflect.DeepEqual(got, want) {
		t.Errorf("the test binary saw %q, want %q", got, want)
	}
	if code := testExec([]string{"/t/counters", "/t/cover-go"}); code != 2 {
		t.Errorf("without a test binary: exit %d, want 2", code)
	}
	if code := testExec([]string{"/t/counters", "/t/cover-go", filepath.Join(dir, "missing")}); code != 2 {
		t.Errorf("a test binary that does not exist: exit %d, want 2", code)
	}
}

func TestTestExecPassesASignalOnToTheTestBinary(t *testing.T) {
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	bin := script(t, dir, "x.test", "trap 'exit 5' HUP\ntouch "+shellQuote(ready)+"\n"+
		"i=0\nwhile [ $i -lt 100 ]; do sleep 0.1; i=$((i+1)); done\nexit 9\n")
	done := make(chan int, 1)
	go func() { done <- testExec([]string{dir, dir, bin}) }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the test binary did not start within 10 s")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := syscall.Kill(os.Getpid(), syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-done:
		if code != 5 {
			t.Errorf("exit %d, want 5, the test binary's on SIGHUP", code)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the test exec did not end within 20 s of the signal")
	}
}

func TestCoverGoScriptStartsTheRoleWithWhatItHolds(t *testing.T) {
	dir := t.TempDir()
	seen := filepath.Join(dir, "seen")
	self := script(t, dir, "the gate's", `for a in "$@"; do echo "$a"; done > `+shellQuote(seen)+"\n")
	goScript := filepath.Join(dir, "go")
	if err := os.WriteFile(goScript, []byte(coverGoScript(self, "/usr/lib/go/bin/go", "/t/it's src", "example.com/m")), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(goScript, "build", "-o", "a b", ".")
	cmd.Env = []string{"PATH=/nowhere"} // it needs nothing of the environment
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	want := []string{roleCoverGo, "/usr/lib/go/bin/go", "/t/it's src", "example.com/m", "build", "-o", "a b", "."}
	if got := readLines(t, seen); !reflect.DeepEqual(got, want) {
		t.Errorf("the role got %q, want %q", got, want)
	}
}

func TestTestExecFlagQuotesEachPath(t *testing.T) {
	got := testExecFlag("/t/test gate", "/t/c", "/t/it's")
	want := `'/t/test gate' ` + roleTestExec + ` '/t/c' '/t/it'\''s'`
	if got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestModulePathReadsGoMod(t *testing.T) {
	for _, c := range []struct {
		gomod, want string
	}{
		{"module example.com/m\n\ngo 1.26\n", "example.com/m"},
		{"// the module\nmodule\t\"github.com/o/r\"\n", "github.com/o/r"},
		{"  module example.com/x // the comment goes\n", "example.com/x"},
	} {
		dir := t.TempDir()
		write(t, dir, "go.mod", c.gomod)
		if got, err := modulePath(dir); err != nil || got != c.want {
			t.Errorf("%q: got %q, %v; want %q", c.gomod, got, err, c.want)
		}
	}
	for _, gomod := range []string{"go 1.26\n", "modules example.com/m\n", "module\n"} {
		dir := t.TempDir()
		write(t, dir, "go.mod", gomod)
		if got, err := modulePath(dir); err == nil {
			t.Errorf("%q: got %q, want an error", gomod, got)
		}
	}
	if _, err := modulePath(t.TempDir()); err == nil {
		t.Error("a tree without go.mod names a module")
	}
}

func TestWithPathPutsADirectoryFirst(t *testing.T) {
	got := withPath([]string{"HOME=/h", "PATH=/usr/bin:/bin", "X=1"}, "/t/cover-go")
	if want := []string{"HOME=/h", "X=1", "PATH=/t/cover-go:/usr/bin:/bin"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
	if got := withPath([]string{"HOME=/h"}, "/t/cover-go"); !reflect.DeepEqual(got, []string{"HOME=/h", "PATH=/t/cover-go"}) {
		t.Errorf("without PATH: got %q", got)
	}
}

func TestLevelArgsRunTheLevelWithCoverageThroughTheTestExec(t *testing.T) {
	setup := coverSetup{self: "/t/gate", bin: "/t/run/cover-go", module: "example.com/m"}
	got := levelArgs(levelIntegration, setup, coverDir("/t/run/level"))
	want := []string{"test", "-json", "-count=1", "-cover", "-covermode=set", "-coverpkg=./...",
		"-coverprofile=/t/run/level/profile", "-exec", "'/t/gate' " + roleTestExec + " '/t/run/level/counters' '/t/run/cover-go'",
		"-tags", "integration", "./..."}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q\nwant %q", got, want)
	}
	if !slices.Equal(levelE2E.args, []string{"-tags", "e2e", "./e2e/..."}) || levelE2E.dir != "e2e" || levelIntegration.dir != "." {
		t.Errorf("the levels are not the full build's: %v, %v", levelIntegration, levelE2E)
	}
}

func TestLevelEnvKeepsTheLevelsTestsAwayFromTheUsersTmux(t *testing.T) {
	got := levelEnv([]string{"PATH=/usr/bin", "TMUX=/tmp/tmux-1000/default,1,0", "TMUX_PANE=%1",
		"TMUX_TMPDIR=/home/u/t", "GOCOVERDIR=/home/u/cov", "HOME=/h"}, "/t/run")
	want := []string{"PATH=/usr/bin", "HOME=/h",
		"GOCACHE=/t/run/cache", "GOBIN=/t/run/bin", "TMPDIR=/t/run/tmp", "GOFLAGS=", "NO_COLOR=1", "TMUX_TMPDIR=/t/run"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestFailedOutputLeavesOutThePackagesThatPassed(t *testing.T) {
	out := `{"Action":"start","Package":"example.com/m/cmd/prog"}
{"Action":"output","Package":"example.com/m/cmd/prog","Output":"\texample.com/m/cmd/prog\t\tcoverage: 0.0% of statements\n"}
{"Action":"skip","Package":"example.com/m/cmd/prog"}
{"Action":"start","Package":"example.com/m/user"}
{"Action":"run","Package":"example.com/m/user","Test":"TestDouble"}
{"Action":"pass","Package":"example.com/m/user","Test":"TestDouble"}
{"Action":"output","Package":"example.com/m/user","Output":"ok  \texample.com/m/user\t0.01s\tcoverage: 50.0% of statements in ./...\n"}
{"Action":"pass","Package":"example.com/m/user"}
{"Action":"start","Package":"example.com/m/lib"}
{"Action":"run","Package":"example.com/m/lib","Test":"TestFails"}
{"Action":"output","Package":"example.com/m/lib","Test":"TestFails","Output":"=== RUN   TestFails\n"}
{"Action":"output","Package":"example.com/m/lib","Test":"TestFails","Output":"    fail_test.go:5: it fails\n"}
{"Action":"output","Package":"example.com/m/lib","Test":"TestFails","Output":"--- FAIL: TestFails (0.00s)\n"}
{"Action":"fail","Package":"example.com/m/lib","Test":"TestFails"}
{"Action":"output","Package":"example.com/m/lib","Output":"FAIL\texample.com/m/lib\t0.01s\n"}
{"Action":"fail","Package":"example.com/m/lib"}
`
	got := failedOutput([]byte(out))
	want := "--- FAIL: TestFails (0.00s)\n    fail_test.go:5: it fails\nFAIL\texample.com/m/lib\t0.01s\nFAIL"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	if got := failedOutput([]byte("# example.com/m/lib\nlib/lib.go:3:1: syntax error\n")); got != "# example.com/m/lib\nlib/lib.go:3:1: syntax error" {
		t.Errorf("lines that are no events: got %q, want them as they are", got)
	}
}

func TestHandOnCopiesThePrograms(t *testing.T) {
	from, to := t.TempDir(), t.TempDir()
	write(t, from, "covmeta.1", "meta")
	write(t, from, "covcounters.1.2.3", "counters")
	if err := handOn(from, ""); err != nil {
		t.Fatal(err)
	}
	if err := handOn(from, to); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"covmeta.1": "meta", "covcounters.1.2.3": "counters"} {
		if got, err := os.ReadFile(filepath.Join(to, name)); err != nil || string(got) != body {
			t.Errorf("%s: got %q, %v; want %q", name, got, err, body)
		}
	}
	if err := handOn(filepath.Join(from, "missing"), to); err == nil {
		t.Error("a directory that does not exist is handed on without an error")
	}
}

func TestCoverDirReadsTheProfileTheTestsWrote(t *testing.T) {
	d := coverDir(t.TempDir())
	c := ranBlocks{}
	if err := d.read(t.Context(), nil, c); err == nil || !strings.Contains(err.Error(), "wrote no coverage profile") {
		t.Errorf("err %v; want one saying the tests wrote no profile", err)
	}
	if err := d.make(); err != nil {
		t.Fatal(err)
	}
	write(t, string(d), "profile", "mode: set\nm/a.go:1.1,2.2 1 1\nm/a.go:3.1,4.2 1 0\n")
	t.Setenv(envCoverDir, "")
	if err := d.read(t.Context(), nil, c); err != nil {
		t.Fatal(err)
	}
	want := ranBlocks{{"m/a.go", srcPos{1, 1}, srcPos{2, 2}}: true, {"m/a.go", srcPos{3, 1}, srcPos{4, 2}}: false}
	if !reflect.DeepEqual(c, want) {
		t.Errorf("got %v, want %v", c, want)
	}
	write(t, string(d), "profile", "mode: set\nnonsense\n")
	if err := d.read(t.Context(), nil, ranBlocks{}); err == nil {
		t.Error("a profile it cannot read is read without an error")
	}
}

func TestRunRoleRunsTheRoleItWasStartedIn(t *testing.T) {
	if code, ok := runRole([]string{"/t/gate", roleCoverGo}); !ok || code != 2 {
		t.Errorf("the run's go without its arguments: %d, %v; want 2, a role", code, ok)
	}
	if code, ok := runRole([]string{"/t/gate", roleTestExec, "/t/c"}); !ok || code != 2 {
		t.Errorf("the test exec without its arguments: %d, %v; want 2, a role", code, ok)
	}
	realGo := script(t, t.TempDir(), "go", "exit 4\n")
	t.Setenv(envRealGo, realGo)
	t.Setenv(envCover, "")
	if code, ok := runRole([]string{"/t/run/mutation-go/go", "version"}); !ok || code != 4 {
		t.Errorf("the go of a mutation run: %d, %v; want the real go's 4, a role", code, ok)
	}
	for _, args := range [][]string{{"/t/gate", "mutation", "origin/dev"}, {"/t/gate"}, nil} {
		if _, ok := runRole(args); ok {
			t.Errorf("%q: started in a role", args)
		}
	}
}

func TestToolCoverageRunIsTheToolsFirstRun(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	d := coverDir("/t/run/cover")
	args := []string{"test", "-coverpkg", "./...", "-cover", "-coverprofile", "/t/g/coverage", "./..."}
	got, profile, ok := toolCoverageRun(args, d, "/t/run/cover-go")
	want := []string{"test", "-exec", testExecFlag(self, "/t/run/cover/counters", "/t/run/cover-go"),
		"-coverpkg", "./...", "-cover", "-coverprofile", "/t/g/coverage", "./..."}
	if !ok || profile != "/t/g/coverage" || !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, %q, %v\nwant %q, /t/g/coverage", got, profile, ok, want)
	}
	for _, args := range [][]string{
		{"test", "-timeout", "32s", "-failfast", "./..."},
		{"test", "./...", "-coverprofile"},
		{"build", "-coverprofile", "x"},
		nil,
	} {
		if _, _, ok := toolCoverageRun(args, d, "/t/run/cover-go"); ok {
			t.Errorf("%q is taken for the tool's first run", args)
		}
	}
}

func TestCoverageRunKeepsTheProfileOfARunThatPassed(t *testing.T) {
	dir := t.TempDir()
	d := coverDir(filepath.Join(dir, "cover"))
	if err := d.make(); err != nil {
		t.Fatal(err)
	}
	profile, seen := filepath.Join(dir, "coverage"), filepath.Join(dir, "seen")
	realGo := script(t, dir, "go", `echo "$*" > `+shellQuote(seen)+"\n"+
		`echo "${`+envCover+`:-none} ${`+envCoverGo+`:-none}" >> `+shellQuote(seen)+"\n"+
		"echo 'mode: set' > "+shellQuote(profile)+"\nexit ${EXIT:-0}\n")
	t.Setenv(envCover, string(d))
	t.Setenv(envCoverGo, "/t/cover-go")
	if code := coverageRun(realGo, []string{"test", "./..."}, profile, d); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if got := readLines(t, seen); !reflect.DeepEqual(got, []string{"test ./...", "none none"}) {
		t.Errorf("the real go saw %q, want its arguments and none of the gate's variables", got)
	}
	if got, err := os.ReadFile(d.profile()); err != nil || string(got) != "mode: set\n" {
		t.Errorf("the kept profile: %q, %v", got, err)
	}
	if err := os.Remove(d.profile()); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EXIT", "1")
	if code := coverageRun(realGo, []string{"test", "./..."}, profile, d); code != 1 {
		t.Errorf("exit %d, want the real go's 1", code)
	}
	if _, err := os.Stat(d.profile()); err == nil {
		t.Error("the profile of a run that failed is kept")
	}
	t.Setenv("EXIT", "0")
	blocked := coverDir(filepath.Join(profile, "cover")) // under a file: nowhere to keep it
	if code := coverageRun(realGo, []string{"test", "./..."}, profile, blocked); code != 2 {
		t.Errorf("a profile that cannot be kept: exit %d, want 2", code)
	}
}
