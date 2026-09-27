//go:build integration

package proc

import (
	"errors"
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
