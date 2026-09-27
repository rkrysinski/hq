// Package proc runs external programs as argument lists, never through a
// shell (design §7.3).
package proc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Runner runs a program and returns its standard output.
type Runner interface {
	Run(name string, args ...string) ([]byte, error)
}

// Exec runs programs with os/exec.
type Exec struct {
	// Timeout, when set, ends a program that runs longer (Run only).
	Timeout time.Duration
}

// Run runs name with args. A non-zero exit becomes an error carrying the
// program's first line of stderr.
func (e Exec) Run(name string, args ...string) ([]byte, error) {
	ctx := context.Background()
	if e.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, e.Timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = time.Second
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return out, &Error{Name: name, Msg: fmt.Sprintf("no answer within %s", e.Timeout)}
	}
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

// Interactive runs a program on hq's own terminal (stdin, stdout, stderr)
// and waits for it, without a TMUX variable so tmux never refuses to attach.
func (Exec) Interactive(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "TMUX=") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	return cmd.Run()
}
