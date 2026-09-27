// Package proc runs external programs as argument lists, never through a
// shell (design §7.3).
package proc

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Runner runs a program and returns its standard output.
type Runner interface {
	Run(name string, args ...string) ([]byte, error)
}

// Exec runs programs with os/exec.
type Exec struct{}

// Run runs name with args. A non-zero exit becomes an error carrying the
// program's first line of stderr.
func (Exec) Run(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if i := strings.IndexByte(msg, '\n'); i >= 0 {
			msg = msg[:i]
		}
		if msg == "" {
			msg = err.Error()
		}
		return out, &Error{Name: name, Msg: msg, NotFound: errors.Is(err, exec.ErrNotFound)}
	}
	return out, nil
}

// Error is a failed run.
type Error struct {
	Name     string
	Msg      string
	NotFound bool // the program is not installed
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Name, e.Msg) }
