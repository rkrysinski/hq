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
	"os/signal"
	"strings"
	"syscall"
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
	// Dir, when set, is the directory the program runs in (Run only).
	Dir string
}

// Run runs name with args. A non-zero exit becomes an error carrying the
// program's first line of stderr, and all of it for a caller that knows the
// program's way of reporting better.
func (e Exec) Run(name string, args ...string) ([]byte, error) {
	ctx := context.Background()
	if e.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, e.Timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = e.Dir
	cmd.WaitDelay = time.Second
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return out, &Error{Name: name, Msg: fmt.Sprintf("no answer within %s", e.Timeout)}
	}
	if err != nil {
		all := strings.TrimSpace(stderr.String())
		msg := all
		if i := strings.IndexByte(msg, '\n'); i >= 0 {
			msg = strings.TrimSpace(msg[:i])
		}
		if msg == "" {
			msg = err.Error()
		}
		return out, &Error{Name: name, Msg: msg, Stderr: all, NotFound: errors.Is(err, exec.ErrNotFound)}
	}
	return out, nil
}

// Error is a failed run.
type Error struct {
	Name     string
	Msg      string
	Stderr   string // everything the program wrote to stderr, trimmed
	NotFound bool   // the program is not installed
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

// Foreground runs a program on hq's own terminal (stdin, stdout, stderr) and
// waits for it, the way a shell runs a command: keys that interrupt or quit
// reach the program from the terminal and leave hq be, and a request to
// terminate hq is passed on to the program. It returns the program's exit
// status, 128 plus the signal's number when a signal ended it; err is set
// only when the program could not start.
func (Exec) Foreground(name string, args ...string) (int, error) {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTERM)
	defer signal.Stop(sigs)
	cmd := exec.Command(name, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			select {
			case s := <-sigs:
				if s == syscall.SIGTERM {
					_ = cmd.Process.Signal(s)
				}
			case <-done:
				return
			}
		}
	}()
	_ = cmd.Wait()
	return ExitStatus(cmd.ProcessState), nil
}

// ExitStatus is a finished program's exit status as a shell reports it:
// 128 plus the signal's number when a signal ended it.
func ExitStatus(ps *os.ProcessState) int {
	if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	return ps.ExitCode()
}

// Detach starts a program that outlives hq and returns at once: in a session
// of its own, so the terminal's keys and hangup never reach it, with no
// terminal (stdin, stdout and stderr are /dev/null), never waited for.
func (Exec) Detach(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
