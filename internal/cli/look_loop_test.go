package cli

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/sbx"
	"github.com/rkrysinski/hq/internal/state"
)

// hq wait asks sbx before its first look, which waits for the answer, and
// after it in the background once a second, each look taking the latest
// answer: a sandbox that stops after the first question ends its agents
// with the answer to the question a second after it, whatever goroutine
// asks.
func TestWaitAsksSbxBeforeItsFirstLookThenInTheBackgroundOnceASecond(t *testing.T) {
	f := newWaitFakes()
	start := f.now
	f.after(500*time.Millisecond, func() {
		f.sbx.sandboxes = []sbx.Sandbox{{Name: "claude-x", Status: "stopped"}}
	})
	var (
		mu    sync.Mutex
		asked []time.Duration // from start, on f's clock
	)
	d := f.deps()
	d.pollSandboxes = func() ([]sbx.Sandbox, error) {
		mu.Lock()
		defer mu.Unlock()
		asked = append(asked, f.now.Sub(start))
		return f.sbx.List()
	}
	// A look at a whole second asks sbx in the background: the clock moves
	// on once the question was asked, so the look after it can take the
	// answer; the waits share one bound. Every look also waits a moment
	// before the clock moves on, so a question asked in the background is
	// noted at about the look that asked it.
	bound := time.Now().Add(5 * time.Second)
	var missed []time.Duration
	sleep := d.sleep
	d.sleep = func(dur time.Duration) {
		mu.Lock()
		at := f.now.Sub(start)
		mu.Unlock()
		if at >= time.Second && at%time.Second == 0 {
			want := 1 + int(at/time.Second)
			for {
				mu.Lock()
				n := len(asked)
				mu.Unlock()
				if n >= want {
					break
				}
				if time.Now().After(bound) {
					missed = append(missed, at)
					break
				}
				time.Sleep(time.Millisecond)
			}
		}
		time.Sleep(time.Millisecond)
		mu.Lock()
		defer mu.Unlock()
		sleep(dur)
	}
	var out, errOut bytes.Buffer
	code := mainWith([]string{"wait", "--timeout", "3s", "--json"}, Env{Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errOut}, d)
	if code != 0 {
		t.Fatalf("exit %d %q", code, errOut.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(missed) != 0 {
		t.Fatalf("sbx not asked by the look at %v within 5 s; asked at %v", missed, asked)
	}
	if len(asked) < 2 || asked[0] != 0 || asked[1] != time.Second {
		t.Fatalf("asked sbx at %v, want at 0s and 1s", asked)
	}
	took := f.now.Sub(start)
	for _, at := range asked {
		if at%time.Second != 0 || at > took {
			t.Fatalf("asked sbx at %v, each at a whole second up to the last look at %v", asked, took)
		}
	}
	w := waitJSON(t, out.String())
	if len(w.Agents) != 2 || slices.ContainsFunc(w.Agents, func(r lsRow) bool { return r.State != state.Ended }) {
		t.Fatalf("want a and b ended, got %s", out.String())
	}
	if took > 2*time.Second {
		t.Fatalf("returned after %v", took)
	}
}

// hq wait fails as hq ls does when tmux cannot list the windows for a look
// of its look loop.
func TestWaitFailsWhenTmuxCannotListTheWindows(t *testing.T) {
	f := newWaitFakes()
	f.tmux.windowsErr = errors.New("tmux gone")
	if code, out, errOut := f.run("wait", "--timeout", "1s"); code != ExitEnvironment || out != "" || errOut != "hq: tmux gone\n" {
		t.Fatalf("exit %d %q %q", code, out, errOut)
	}
}
