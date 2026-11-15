package agent

import (
	"sync"
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/journal"
	"github.com/rkrysinski/hq/internal/journal/journaltest"
	"github.com/rkrysinski/hq/internal/state"
	"github.com/rkrysinski/hq/internal/tmux"
)

// evWindow is the home window of agent a1, started long before the look,
// in sandbox claude-app.
func evWindow() tmux.Window {
	w := lookWindow("1")
	w.Options["sandbox"] = "claude-app"
	return w
}

// basisOf is the number of the last event of agent a1's hook in its
// journal, the basis of what a look appends after it.
func basisOf(t *testing.T, s *memStore) int64 {
	t.Helper()
	if err := s.Ingest("/w/app", "a1"); err != nil {
		t.Fatal(err)
	}
	return journal.Basis(s.journalOf(t, "a1"))
}

// since are the events after number n.
func since(es []state.Event, n int64) []state.Event {
	var out []state.Event
	for _, e := range es {
		if e.Number > n {
			out = append(out, e)
		}
	}
	return out
}

// The pane's death is tmux's event, dated by tmux, appended once; sbx's
// answer that ends an agent is sbx's, with the moment sbx was asked. An
// answer sbx did not give (Sandboxes{}: an sbx too old, failed or slow)
// ends nobody and is no event, nor is one a report of the hook came after.
func TestTheLookAppendsThePanesDeathAndTheSandboxNotRunning(t *testing.T) {
	now := time.Unix(1000, 0)
	died := now.Add(-3 * time.Second)
	rs := map[string]state.Report{"a1": {State: state.Done, Since: now.Add(-time.Minute), Last: "Done."}}
	s := newStore(nil)
	s.report("a1", rs["a1"])
	basis := basisOf(t, s)
	w := evWindow()
	w.PaneDead, w.DeadAt = true, died
	for i := range 3 {
		if a := s.look([]tmux.Window{w}, rs, Sandboxes{}, now.Add(time.Duration(i)*time.Second))[0]; a.State != state.Ended || !a.Since.Equal(died) || a.Last != "Done." {
			t.Fatalf("dead: %+v", a)
		}
	}
	es := since(s.journalOf(t, "a1"), basis)
	if len(es) != 1 || es[0].Source != state.SourceTmux || es[0].Kind != state.KindPaneDied || !es[0].Facts.From.Equal(died) || !es[0].Time.Equal(now) || es[0].Basis != basis {
		t.Fatalf("the pane's death: %+v", es)
	}

	s = newStore(nil)
	s.report("a1", rs["a1"])
	basis = basisOf(t, s)
	asked := now.Add(-time.Second)
	for _, sbxs := range []Sandboxes{{}, {Running: map[string]bool{"claude-app": true}, Asked: asked}} {
		if a := s.look([]tmux.Window{evWindow()}, rs, sbxs, now)[0]; a.State != state.Done {
			t.Fatalf("%+v: %+v", sbxs, a)
		}
	}
	stale := newStore(nil)
	stale.report("a1", state.Report{State: state.Done, Since: asked.Add(time.Millisecond)})
	if a := stale.look([]tmux.Window{evWindow()}, nil, Sandboxes{Running: map[string]bool{}, Asked: asked}, now)[0]; a.State != state.Done || ofSourceKinds(stale.journalOf(t, "a1"), state.SourceSbx) != "" {
		t.Fatalf("a report after the answer: %+v", a)
	}
	if es := since(s.journalOf(t, "a1"), basis); len(es) != 0 {
		t.Fatalf("no answer, or a running sandbox: %+v", es)
	}
	stopped := Sandboxes{Running: map[string]bool{"claude-lib": true}, Asked: asked}
	for range 2 {
		if a := s.look([]tmux.Window{evWindow()}, rs, stopped, now)[0]; a.State != state.Ended || !stopped.Ends(a) {
			t.Fatalf("stopped: %+v", a)
		}
	}
	es = since(s.journalOf(t, "a1"), basis)
	if len(es) != 1 || es[0].Source != state.SourceSbx || es[0].Kind != state.KindNotRunning || !es[0].Facts.Asked.Equal(asked) || es[0].Basis != basis {
		t.Fatalf("the sandbox not running: %+v", es)
	}
}

// The user ended the turn while a subagent ran; Claude woke the agent
// since: the look appends the wake-up once, dated from its report, and the
// agent works since then.
func TestTheLookAppendsAWakeUpOnce(t *testing.T) {
	now := time.Unix(1000, 0)
	prompted, ended, woken := now.Add(-5*time.Minute), now.Add(-4*time.Minute), now.Add(-time.Minute)
	s := newStore(map[string]string{"%1": declinedScreen()})
	ws := []tmux.Window{evWindow()}
	hook := func(name string, at time.Time, p map[string]any) {
		s.Leave("/w/app", "a1", name, journaltest.Hook("main", p), at)
	}
	hook("000000001.prompt.1", prompted, map[string]any{"hook_event_name": "UserPromptSubmit", "prompt_id": "t1", "prompt": "Run the slow tests in a subagent."})
	hook("000000002.answer.1", prompted.Add(time.Second), map[string]any{"hook_event_name": "PostToolUse", "prompt_id": "t1", "tool_name": "Agent", "tool_response": map[string]any{"status": "async_launched", "agentId": "sub1"}})
	if a := Look(ws, Sandboxes{}, ended, s, nil, nil)[0]; a.State != state.Done || a.Last != declinedLine {
		t.Fatalf("ended by the user: %+v", a)
	}
	seen := s.journalOf(t, "a1")
	basis := seen[len(seen)-1].Number
	s.screens["%1"] = capture(t, "fs-thinking")
	hook("000000003.prompt.1", woken, map[string]any{"hook_event_name": "UserPromptSubmit", "prompt_id": "t2", "prompt": "<task-notification>\n<task-id>sub1</task-id>\n<status>completed</status>\n</task-notification>"})
	for i := range 3 {
		if a := Look(ws, Sandboxes{}, now.Add(time.Duration(i)*time.Second), s, nil, nil)[0]; a.State != state.Working || !a.Since.Equal(woken) {
			t.Fatalf("woken: %+v", a)
		}
	}
	var woke []state.Event
	for _, e := range since(s.journalOf(t, "a1"), basis) {
		if e.Source == state.SourceScreen {
			woke = append(woke, e)
		}
	}
	if len(woke) != 1 || woke[0].Kind != state.KindWokeUp || !woke[0].Facts.From.Equal(woken) || !woke[0].Time.Equal(now) {
		t.Fatalf("woken: %s %+v", journaltest.Kinds(since(s.journalOf(t, "a1"), basis)), woke)
	}
}

// A hundred looks at an agent whose screen stays the same add no event
// after the first, with Folds kept or each look folding anew: the journal
// holds it.
func TestAHundredLooksAtTheSameScreenAddOneEvent(t *testing.T) {
	now := time.Unix(1000, 0)
	s := newStore(map[string]string{"%1": declinedScreen()})
	s.report("a1", state.Report{State: state.Working, Since: now.Add(-time.Minute)})
	basis := basisOf(t, s)
	folds := NewFolds()
	for i := range 100 {
		f := folds
		if i >= 50 {
			f = nil
		}
		Look([]tmux.Window{evWindow()}, Sandboxes{}, now.Add(time.Duration(i)*10*time.Millisecond), s, nil, f)
	}
	if es := since(s.journalOf(t, "a1"), basis); len(es) != 1 || es[0].Kind != state.KindTurnEnded || es[0].Basis != basis {
		t.Fatalf("%d events: %s", len(es), journaltest.Kinds(es))
	}
}

// racingStore lets n looks read the journal before any of them appends:
// all see the same evidence missing, and all append it.
type racingStore struct {
	*memStore
	read *sync.WaitGroup
	once *sync.Once
}

func (s racingStore) ReadFrom(id string, from int64) (journal.Journal, error) {
	defer s.once.Do(s.read.Done)
	return s.memStore.ReadFrom(id, from)
}

func (s racingStore) Append(id string, events ...state.Event) ([]state.Event, error) {
	s.read.Wait()
	return s.memStore.Append(id, events...)
}

// Of eight looks racing to append the same evidence, in eight hq
// processes, one event stands and the others are set aside as duplicates;
// all eight show the same state.
func TestOfEightLooksRacingOneEventStands(t *testing.T) {
	now := time.Unix(1000, 0)
	s := newStore(map[string]string{"%1": declinedScreen()})
	s.report("a1", state.Report{State: state.Working, Since: now.Add(-time.Minute)})
	basis := basisOf(t, s)
	var read, done sync.WaitGroup
	read.Add(8)
	shown := make([]Agent, 8)
	for i := range 8 {
		done.Go(func() {
			r := racingStore{s, &read, &sync.Once{}}
			shown[i] = Look([]tmux.Window{evWindow()}, Sandboxes{}, now.Add(time.Duration(i)*time.Millisecond), r, nil, nil)[0]
		})
	}
	done.Wait()
	es := since(s.journalOf(t, "a1"), basis)
	var stand, dup int
	for _, e := range es {
		switch e.Aside {
		case "":
			stand++
		case state.AsideDuplicate:
			dup++
		}
	}
	if len(es) != 8 || stand != 1 || dup != 7 || es[0].Aside != "" {
		t.Fatalf("%d stand, %d duplicates: %+v", stand, dup, es)
	}
	for _, a := range shown {
		if a.State != state.Done || !a.Since.Equal(es[0].Time) {
			t.Errorf("shows %s since %v, the event stands since %v", a.State, a.Since, es[0].Time)
		}
	}
}

// overtakingStore has the hook's next report ingested between the look's
// reading of the journal and its append, as another hq process does.
type overtakingStore struct {
	*memStore
	t *testing.T
}

func (s overtakingStore) Append(id string, events ...state.Event) ([]state.Event, error) {
	s.Leave("/w/app", "a1", "000000099.answer.1", journaltest.Hook("main", map[string]any{"hook_event_name": "PostToolUse", "tool_name": "Bash"}), time.Unix(999, 0))
	if err := s.Ingest("/w/app", "a1"); err != nil {
		s.t.Fatal(err)
	}
	return s.memStore.Append(id, events...)
}

// Evidence whose basis a later report of the hook overtook before it was
// appended is set aside with that reason, and counts for nothing.
func TestEvidenceALaterReportOvertookIsSetAside(t *testing.T) {
	now := time.Unix(1000, 0)
	s := newStore(map[string]string{"%1": declinedScreen()})
	s.report("a1", state.Report{State: state.Working, Since: now.Add(-time.Minute)})
	basis := basisOf(t, s)
	a := Look([]tmux.Window{evWindow()}, Sandboxes{}, now, overtakingStore{s, t}, nil, nil)[0]
	es := since(s.journalOf(t, "a1"), basis)
	if len(es) != 2 || es[0].Kind != "PostToolUse" || es[1].Kind != state.KindTurnEnded || es[1].Aside != state.AsideOvertaken || es[1].Basis != basis {
		t.Fatalf("%+v", es)
	}
	if a.State != state.Working {
		t.Fatalf("shows %+v", a)
	}
}
