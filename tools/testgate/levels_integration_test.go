//go:build integration

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
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
	if err := handOn(from, filepath.Join(from, "covmeta.1")); err == nil {
		t.Error("files handed on to a file are copied without an error")
	}
}

func TestCoverDirReadsTheProfileTheTestsWrote(t *testing.T) {
	d := coverDir(t.TempDir())
	c := ranBlocks{}
	if err := d.read(t.Context(), nil, c); err == nil || !strings.Contains(err.Error(), "wrote no coverage profile") {
		t.Errorf("err %v; want one saying the tests wrote no profile", err)
	}
	d.make()
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
	// What a program wrote that go tool covdata cannot read.
	write(t, string(d), "profile", "mode: set\n")
	write(t, d.counters(), "covmeta.0123456789abcdef0123456789abcdef", "not coverage")
	if err := d.read(t.Context(), os.Environ(), ranBlocks{}); err == nil || !strings.Contains(err.Error(), "go tool covdata") {
		t.Errorf("err %v; want go tool covdata's", err)
	}
}

func TestRunRoleRunsTheGoOfAMutationRun(t *testing.T) {
	realGo := script(t, t.TempDir(), "go", "exit 4\n")
	t.Setenv(envRealGo, realGo)
	t.Setenv(envCover, "")
	if code, ok := runRole([]string{"/t/run/mutation-go/go", "version"}); !ok || code != 4 {
		t.Errorf("the go of a mutation run: %d, %v; want the real go's 4, a role", code, ok)
	}
	// The tool's first run of the unit tests, through the go of a mutation
	// run: with each test binary run through the test exec, its profile
	// kept for the gate.
	dir := t.TempDir()
	d := coverDir(filepath.Join(dir, "cover"))
	d.make()
	profile, seen := filepath.Join(dir, "coverage"), filepath.Join(dir, "seen")
	t.Setenv(envRealGo, script(t, dir, "go", `echo "$*" > `+shellQuote(seen)+"\necho 'mode: set' > "+shellQuote(profile)+"\n"))
	t.Setenv(envCover, string(d))
	t.Setenv(envCoverGo, "/t/cover-go")
	if code, ok := runRole([]string{"/t/run/mutation-go/go", "test", "-cover", "-coverprofile", profile, "./..."}); !ok || code != 0 {
		t.Errorf("the tool's first run: %d, %v; want 0, a role", code, ok)
	}
	if got := readLines(t, seen); len(got) != 1 || !strings.HasPrefix(got[0], "test -exec ") || !strings.Contains(got[0], roleTestExec) {
		t.Errorf("the real go saw %q, want go test with -exec through the test exec", got)
	}
	if _, err := os.Stat(d.profile()); err != nil {
		t.Errorf("the profile is not kept: %v", err)
	}
}

func TestCoverageRunKeepsTheProfileOfARunThatPassed(t *testing.T) {
	dir := t.TempDir()
	d := coverDir(filepath.Join(dir, "cover"))
	d.make()
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
func TestModulePathReadsTheTreesGoMod(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "go.mod", "module example.com/m\n\ngo 1.26\n")
	if got, err := modulePath(dir); err != nil || got != "example.com/m" {
		t.Errorf("got %q, %v; want example.com/m", got, err)
	}
	if _, err := modulePath(t.TempDir()); err == nil {
		t.Error("a tree without go.mod names a module")
	}
}

func TestEndTmuxServersEndsTheServersOfTheSocketsInTheDirectory(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("no tmux")
	}
	// A short directory: a socket's path has a limit a TempDir can pass.
	dir, err := os.MkdirTemp("/tmp", "m")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket := filepath.Join(dir, "tmux-1", "s")
	if out, err := exec.Command("tmux", "-u", "-S", socket, "new-session", "-d", "sleep 60").CombinedOutput(); err != nil {
		t.Fatalf("tmux: %v\n%s", err, out)
	}
	t.Cleanup(func() { exec.Command("tmux", "-S", socket, "kill-server").Run() })
	write(t, dir, "tmux-1/notes", "not a socket\n")
	endTmuxServers(dir)
	if err := exec.Command("tmux", "-S", socket, "has-session").Run(); err == nil {
		t.Error("the server of the socket still runs")
	}
	if _, err := os.Stat(filepath.Join(dir, "tmux-1", "notes")); err != nil {
		t.Errorf("the file beside the socket: %v", err)
	}
}

// A tree whose lines cannot be judged ends the run before the tool is
// installed, with nothing left behind.
func TestMutationEndsBeforeTheToolOnATreeItCannotJudge(t *testing.T) {
	gitOnly := t.TempDir()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git")
	}
	if err := os.Symlink(git, filepath.Join(gitOnly, "git")); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, want string
		change     func(t *testing.T, r *mutationRepo)
	}{
		{"no go in PATH", `"go"`, func(t *testing.T, r *mutationRepo) {
			t.Setenv("PATH", gitOnly)
		}},
		{"no go.mod", "go.mod", func(t *testing.T, r *mutationRepo) {
			if err := os.Remove(filepath.Join(r.dir, "go.mod")); err != nil {
				t.Fatal(err)
			}
		}},
		{"a changed file that does not parse, which no build takes", "the changed file cmd/tool/old.go", func(t *testing.T, r *mutationRepo) {
			write(t, r.dir, "cmd/tool/old.go", "//go:build ignore\n\npackage main\n\nfunc old( {\n")
		}},
		{"a file it cannot read", "staging the tree", func(t *testing.T, r *mutationRepo) {
			if os.Geteuid() == 0 {
				t.Skip("root reads every file")
			}
			if err := os.Chmod(filepath.Join(r.dir, "lib/lib.go"), 0); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.Chmod(filepath.Join(r.dir, "lib/lib.go"), 0o644) })
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newMutationRepo(t)
			r.change()
			tmp, cache := isolated(t)
			c.change(t, r)
			lines, failed, err := runMutationIn(t, r.dir, "main")
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err %v; want one naming %s", err, c.want)
			}
			if failed || !reflect.DeepEqual(lines, []string{""}) {
				t.Errorf("printed %q, failed %v; want nothing", lines, failed)
			}
			assertEmpty(t, "the temporary directory", tmp)
			assertEmpty(t, "the shared build cache", cache)
		})
	}
}
