//go:build integration

package proc

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestRunReturnsStdoutAndFirstStderrLineOnFailure(t *testing.T) {
	out, err := Exec{}.Run("sh", "-c", `echo out; echo "first" >&2; echo "second" >&2; exit 3`)
	var pe *Error
	if !errors.As(err, &pe) || pe.Msg != "first" || pe.NotFound || string(out) != "out\n" {
		t.Fatalf("%q %v", out, err)
	}
	if pe.Error() != "sh: first" {
		t.Fatalf("%q", pe.Error())
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
