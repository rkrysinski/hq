//go:build integration

package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/agent"
	"github.com/rkrysinski/hq/internal/state"
	"github.com/rkrysinski/hq/internal/testutil"
)

// process is hq as another process runs it: the same tmux server, sbx and
// state directory, journals folded by its own looks.
func (h *realHQ) process() func(args ...string) (int, string, string) {
	d := h.d
	d.folds = agent.NewFolds()
	return func(args ...string) (int, string, string) {
		var out, errOut bytes.Buffer
		code := mainWith(args, Env{Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errOut}, d)
		return code, out.String(), errOut.String()
	}
}

// The acceptance run of #166: 1,000 state changes across five agents, left
// by their hooks as fast as a busy host makes them, while hq ls --json and
// two hq wait loops look at once, each in a process of its own, each loop
// passing on the cursor it got. No loop is returned the same change of an
// agent twice, every row a call returns is numbered above the cursor that
// call was given, and each loop is returned every agent's last change.
func TestCursorsOfLoopsLookingAtOnceMissNoChangeAndRepeatNone(t *testing.T) {
	testutil.FakeClaude(t)
	h := newRealHQ(t)
	h.cwd = testutil.GitRepo(t, "app")
	names := []string{"a1", "a2", "a3", "a4", "a5"}
	type hook struct{ id, session string }
	hooks := map[string]hook{}
	for _, n := range names {
		if code, _, errOut := h.run("new", n, "hello"); code != 0 {
			t.Fatalf("new %s: %s", n, errOut)
		}
	}
	for _, n := range names {
		h.waitReport(n, state.Done, "Done: hello")
		id := h.agentID(n)
		r, ok := h.d.readState(h.cwd, id)
		if !ok || r.SessionID == "" {
			t.Fatalf("%s's session: %+v", n, r)
		}
		hooks[n] = hook{id, r.SessionID}
	}

	// The hook's side: each event file written under another name and
	// renamed into place, named after the hook's own files.
	count := 0
	leave := func(n, kind string, payload map[string]any) {
		hk := hooks[n]
		payload["session_id"], payload["hook_event_name"], payload["cwd"] = hk.session, kind, h.cwd
		data, _ := json.Marshal(payload)
		dir := state.EventsDir(h.cwd, hk.id)
		count++
		tmp := filepath.Join(dir, fmt.Sprintf("tmp-%d", count))
		if err := os.WriteFile(tmp, append([]byte("branch main\n"), data...), 0o600); err != nil {
			t.Error(err)
			return
		}
		if err := os.Rename(tmp, filepath.Join(dir, fmt.Sprintf("%09d.%s.1", 100_000_000+count, strings.ToLower(kind)))); err != nil {
			t.Error(err)
		}
	}

	type loop struct {
		seen  map[string]map[int64]bool
		calls int
	}
	var driven atomic.Bool
	var drivenAt atomic.Int64
	var wg sync.WaitGroup
	loops := make([]*loop, 2)
	for i := range loops {
		run := h.process()
		_, out, _ := run("ls", "--json")
		var ls waitOutput
		if err := json.Unmarshal([]byte(out), &ls); err != nil {
			t.Fatalf("ls: %v %s", err, out)
		}
		l := &loop{seen: map[string]map[int64]bool{}}
		loops[i] = l
		wg.Add(1)
		go func(cursor string) {
			defer wg.Done()
			for deadline := time.Now().Add(2 * time.Minute); time.Now().Before(deadline); {
				started := time.Now()
				code, out, errOut := run("wait", "--json", "--since", cursor, "--timeout", "2s")
				var w waitOutput
				if code != 0 || json.Unmarshal([]byte(out), &w) != nil {
					t.Errorf("wait: exit %d %s%s", code, out, errOut)
					return
				}
				l.calls++
				given := cursorNumber(t, cursor)
				for _, r := range w.Agents {
					if r.StateSeq == nil {
						t.Errorf("a row with no number: %+v", r)
						continue
					}
					seq := *r.StateSeq
					if seq <= given {
						t.Errorf("%s's change %d returned from cursor %d", r.Name, seq, given)
					}
					if l.seen[r.Name] == nil {
						l.seen[r.Name] = map[int64]bool{}
					}
					if l.seen[r.Name][seq] {
						t.Errorf("%s's change %d returned twice", r.Name, seq)
					}
					l.seen[r.Name][seq] = true
				}
				if cursorNumber(t, w.NextSince) < given {
					t.Errorf("cursor went back from %d to %s", given, w.NextSince)
				}
				cursor = w.NextSince
				if len(w.Agents) == 0 && driven.Load() && started.UnixNano() > drivenAt.Load() {
					return
				}
			}
			t.Error("the loop never went quiet")
		}(ls.NextSince)
	}
	// hq ls --json in a process of its own, looking and ingesting too.
	stop := make(chan struct{})
	lsDone := make(chan struct{})
	go func() {
		defer close(lsDone)
		run := h.process()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if code, _, errOut := run("ls", "--json"); code != 0 {
				t.Errorf("ls: %s", errOut)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()

	// 1,000 changes: each agent in turn starts a turn or ends it, as done
	// or with a question; each ends with its turn ended.
	rng := rand.New(rand.NewSource(166))
	working := map[string]bool{}
	for i := range 1000 {
		n := names[i%len(names)]
		turn := fmt.Sprintf("t%d", i/2)
		if working[n] {
			reply := fmt.Sprintf("Done %d.", i)
			if rng.Intn(2) == 0 {
				reply = fmt.Sprintf("Shall I go on with %d?", i)
			}
			leave(n, "Stop", map[string]any{"prompt_id": turn, "last_assistant_message": reply, "stop_hook_active": false, "background_tasks": []any{}})
		} else {
			leave(n, "UserPromptSubmit", map[string]any{"prompt_id": turn, "prompt": "go on"})
		}
		working[n] = !working[n]
		time.Sleep(time.Duration(rng.Intn(6)) * time.Millisecond)
	}
	drivenAt.Store(time.Now().UnixNano())
	driven.Store(true)
	wg.Wait()
	close(stop)
	<-lsDone

	// Every agent's last change, into done or question, reached each loop.
	var last waitOutput
	_, out, _ := h.process()("ls", "--json")
	if err := json.Unmarshal([]byte(out), &last); err != nil || len(last.Agents) != len(names) {
		t.Fatalf("ls: %v %s", err, out)
	}
	for _, r := range last.Agents {
		if r.State != state.Done && r.State != state.Question || r.StateSeq == nil {
			t.Fatalf("%s at the end: %s %v", r.Name, r.State, r.StateSeq)
		}
		for i, l := range loops {
			if !l.seen[r.Name][*r.StateSeq] {
				t.Errorf("loop %d was never returned %s's last change %d (%d calls)", i, r.Name, *r.StateSeq, l.calls)
			}
		}
	}
	n, _ := h.d.journal.Through()
	t.Logf("%d events numbered; loops returned %d and %d changes in %d and %d calls", n, total(loops[0].seen), total(loops[1].seen), loops[0].calls, loops[1].calls)
}

// total counts the changes returned.
func total(seen map[string]map[int64]bool) int {
	n := 0
	for _, s := range seen {
		n += len(s)
	}
	return n
}
