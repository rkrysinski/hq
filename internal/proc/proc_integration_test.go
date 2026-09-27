//go:build integration

package proc

import (
	"errors"
	"testing"
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

func TestRunSucceeds(t *testing.T) {
	if out, err := (Exec{}).Run("echo", "a b"); err != nil || string(out) != "a b\n" {
		t.Fatalf("%q %v", out, err)
	}
}
