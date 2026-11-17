//go:build integration

package proc

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestRunReturnsStdoutAndFirstStderrLineOnFailure(t *testing.T) {
	out, err := Exec{}.Run("sh", "-c", `echo out; echo "first" >&2; echo "second" >&2; exit 3`)
	var pe *Error
	if !errors.As(err, &pe) || pe.Msg != "first" || pe.Stderr != "first\nsecond" || pe.NotFound || string(out) != "out\n" {
		t.Fatalf("%q %v", out, err)
	}
	if pe.Error() != "sh: first" {
		t.Fatalf("%q", pe.Error())
	}
}

func TestRunWithStderrFileGivesTheProgramNoPipeAndStillReportsItsFailure(t *testing.T) {
	// The program exits 7 when its stderr is a pipe.
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	const prog = `[ -p /dev/stderr ] && exit 7; echo out; echo "first" >&2; echo "second" >&2; exit 3`
	out, err := Exec{StderrFile: true}.Run("sh", "-c", prog)
	var pe *Error
	if !errors.As(err, &pe) || pe.Msg != "first" || pe.NotFound || string(out) != "out\n" {
		t.Fatalf("%q %v", out, err)
	}
	if _, err := (Exec{}).Run("sh", "-c", prog); !errors.As(err, &pe) || pe.Msg != "exit status 7" {
		t.Fatalf("without StderrFile stderr is a pipe: %v", err)
	}
	if out, err := (Exec{StderrFile: true}).Run("sh", "-c", `echo noise >&2; echo ok`); err != nil || string(out) != "ok\n" {
		t.Fatalf("%q %v", out, err)
	}
	if _, err := (Exec{StderrFile: true}).Run("hq-no-such-program"); !errors.As(err, &pe) || !pe.NotFound {
		t.Fatalf("%v", err)
	}
	if left, _ := os.ReadDir(tmp); len(left) != 0 {
		t.Fatalf("temporary files left: %q", left)
	}
}

func TestRunReportsMissingProgram(t *testing.T) {
	_, err := Exec{}.Run("hq-no-such-program")
	var pe *Error
	if !errors.As(err, &pe) || !pe.NotFound {
		t.Fatalf("%v", err)
	}
}

func TestRunEndsAProgramThatOutlivesTheTimeout(t *testing.T) {
	start := time.Now()
	_, err := Exec{Timeout: 200 * time.Millisecond}.Run("sleep", "5")
	var pe *Error
	if !errors.As(err, &pe) || pe.Msg != "no answer within 200ms" || time.Since(start) > 2*time.Second {
		t.Fatalf("%v after %s", err, time.Since(start))
	}
}

func TestRunSucceeds(t *testing.T) {
	if out, err := (Exec{}).Run("echo", "a b"); err != nil || string(out) != "a b\n" {
		t.Fatalf("%q %v", out, err)
	}
}

func TestInteractiveRunsWithoutTMUXVariable(t *testing.T) {
	t.Setenv("TMUX", "/tmp/x,1,0")
	if err := (Exec{}).Interactive("sh", "-c", `test -z "${TMUX:-}"`); err != nil {
		t.Fatalf("TMUX leaked into the attached program: %v", err)
	}
}

func TestRunRunsInDir(t *testing.T) {
	dir := t.TempDir()
	out, err := Exec{Dir: dir}.Run("pwd", "-P")
	want, _ := filepath.EvalSymlinks(dir)
	if err != nil || strings.TrimSpace(string(out)) != want {
		t.Fatalf("%q %v, want %q", out, err, want)
	}
}

func TestForegroundReturnsTheProgramsStatusAsAShellDoes(t *testing.T) {
	for script, want := range map[string]int{"exit 0": 0, "exit 3": 3, "kill -KILL $$": 128 + 9} {
		if code, err := (Exec{}).Foreground("sh", "-c", script); err != nil || code != want {
			t.Errorf("%s: %d %v, want %d", script, code, err, want)
		}
	}
	if _, err := (Exec{}).Foreground("hq-no-such-program"); err == nil {
		t.Error("a program that cannot start is no error")
	}
}

func TestForegroundPassesATerminationRequestOnToTheProgram(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "ready")
	go func() {
		for i := 0; i < 100; i++ {
			if _, err := os.Stat(ready); err == nil {
				_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
	}()
	// hq itself goes on: the program decides how it ends.
	code, err := (Exec{}).Foreground("sh", "-c", `trap 'exit 7' TERM; touch "$1"; while :; do sleep 0.1; done`, "sh", ready)
	if err != nil || code != 7 {
		t.Fatalf("%d %v, want 7", code, err)
	}
}

// detachedProgram waits for the file "$1.go", which the test creates only
// after Detach has returned, then writes its session to "$1". When the file
// never comes (a Detach that waits for its program), it gives up after
// about 10 s and writes "waited" instead, so the test fails, not hangs.
const detachedProgram = `i=0
while [ ! -e "$1.go" ]; do
	i=$((i + 1))
	if [ "$i" -gt 200 ]; then echo waited > "$1.tmp"; mv "$1.tmp" "$1"; exit 0; fi
	sleep 0.05
done
ps -o sid= -p $$ > "$1.tmp"; mv "$1.tmp" "$1"`

// A detached program runs on after Detach returns, in a session of its own.
// No clock: the program goes on only once the test has seen Detach return,
// so a Detach that waited for it would see it give up (#232).
func TestDetachReturnsAtOnceAndTheProgramRunsOn(t *testing.T) {
	done := filepath.Join(t.TempDir(), "done")
	if err := (Exec{}).Detach("sh", "-c", detachedProgram, "sh", done); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(done+".go", nil, 0o644); err != nil {
		t.Fatal(err)
	}
	var data []byte
	for i := 0; i < 400 && data == nil; i++ { // about 20 s, twice the program's own bound
		if b, err := os.ReadFile(done); err == nil {
			data = b
		} else {
			time.Sleep(50 * time.Millisecond)
		}
	}
	if data == nil {
		t.Fatal("the detached program did not finish")
	}
	sid := strings.TrimSpace(string(data))
	if sid == "waited" {
		t.Fatal("Detach waited for its program: the program ran out of patience before Detach returned")
	}
	own, _ := unix.Getsid(0)
	if sid == "" || sid == strconv.Itoa(own) {
		t.Fatalf("the program's session %q is hq's (%d)", sid, own)
	}
	if err := (Exec{}).Detach("hq-no-such-program"); err == nil {
		t.Fatal("a missing program must be an error")
	}
}
