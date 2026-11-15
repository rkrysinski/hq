package agent

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/journal"
	"github.com/rkrysinski/hq/internal/journal/journaltest"
	"github.com/rkrysinski/hq/internal/state"
	"github.com/rkrysinski/hq/internal/tmux"
)

// journaledStore is a Store with the agents' journals (Journals), as every
// hq command's look has it.
type journaledStore struct {
	*fakeStore
	*journaltest.Memory
}

// evWindow is the home window of agent a1 (hex, as hq issues ids, so its
// journal takes it), started long before the look, in sandbox claude-app.
func evWindow() tmux.Window {
	w := lookWindow("1")
	w.Options["id"], w.Options["sandbox"], w.Options["repo"] = "a1", "claude-app", "/w/app"
	return w
}

// reported leaves a report of agent a1's hook in its journal and returns
// its number, the basis of what a look appends after it.
func reported(t *testing.T, m *journaltest.Memory, name, event string) int64 {
	t.Helper()
	m.Leave("/w/app", "a1", name, journaltest.Hook("main", map[string]any{"hook_event_name": event}), time.Unix(900, 0))
	if err := m.Ingest("/w/app", "a1"); err != nil {
		t.Fatal(err)
	}
	return journal.Basis(events(t, m))
}

func events(t *testing.T, m *journaltest.Memory) []state.Event {
	t.Helper()
	j, err := m.Read("a1")
	if err != nil {
		t.Fatal(err)
	}
	return j.Events
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

// kept carries what a look stored on the agent's window over to w, as tmux
// keeps it for the next look.
func kept(w tmux.Window, s *fakeStore) tmux.Window {
	for k, v := range s.options[w.ID] {
		w.Options[k] = v
	}
	return w
}

// Each kind of evidence a look sees on the screen is appended once, as an
// event of the screen at the look's time, based on the last report of the
// agent's hook in its journal (#195). A decision that takes time (a turn
// rewound, a turn Claude owed that does not come) is a second event,
// written by the look that decides RestDelay later and dated from the
// screen first seen at rest.
func TestTheLookAppendsWhatTheScreenShowsOnce(t *testing.T) {
	now := time.Unix(1000, 0)
	asked := now.Add(-time.Minute)
	ready := "● Waiting for it.\n" + lookRule + "\n❯ \n" + lookRule + "\n  footer\n"
	ownDialog := "● Waiting for it.\n" + strings.Repeat("▔", 20) + "\n   Change effort level?\n   Your next response will be slower\n\n   ❯ 1. Yes, switch to max\n     2. No, go back\n"
	putBack := "✻ Baked for 2s · done\n" + lookRule + "\n❯ Write a poem\n" + lookRule + "\n  ⏵⏵ bypass permissions on\n"
	owedRest := "● The second finished.\n" + lookRule + "\n❯ and now\n" + lookRule + "\n  footer\n"
	type look struct {
		after  time.Duration
		screen string
		want   string // the kinds the look appends, by spaces
	}
	for _, tc := range []struct {
		name   string
		report state.Report
		looks  []look
		check  func(t *testing.T, es []state.Event)
	}{
		{"a turn the user ended, with its line", state.Report{State: state.Working, Since: asked}, []look{{0, declinedScreen(), state.KindTurnEnded}, {time.Second, declinedScreen(), ""}},
			func(t *testing.T, es []state.Event) {
				if es[0].Facts.Text != declinedLine {
					t.Errorf("line %q", es[0].Facts.Text)
				}
			}},
		{"a turn rewound", state.Report{State: state.Working, Since: asked, Prompt: "Write a poem"}, []look{{0, putBack, state.KindAtRest}, {time.Second, putBack, ""}, {RestDelay, putBack, state.KindRewound}, {RestDelay + time.Second, putBack, ""}},
			func(t *testing.T, es []state.Event) {
				if es[0].Facts.Screen == "" || es[1].Facts.Screen != es[0].Facts.Screen || !es[1].Facts.From.Equal(now) || !es[0].Facts.From.IsZero() {
					t.Errorf("seen at rest %+v, decided %+v", es[0].Facts, es[1].Facts)
				}
			}},
		{"a tool the user cut short", state.Report{State: state.Working, Since: asked, Prompt: "Run the ticks.", Acted: true}, []look{{0, capture(t, "fs-esc-tool"), state.KindToolCut}, {RestDelay, capture(t, "fs-esc-tool"), state.KindRewound}}, nil},
		{"a turn Claude owed that does not come", state.Report{State: state.Working, Since: asked.Add(-time.Minute), Latest: asked, Background: true, Owed: true, Last: "The second finished."}, []look{{0, owedRest, state.KindAtRest}, {RestDelay, owedRest, state.KindTurnNotTaken}},
			func(t *testing.T, es []state.Event) {
				if !es[1].Facts.From.Equal(now) {
					t.Errorf("decided %+v", es[1].Facts)
				}
			}},
		{"a dialog of Claude's own, seen and gone", state.Report{State: state.Done, Since: asked}, []look{{0, ownDialog, state.KindOwnDialog}, {time.Second, ownDialog, ""}, {2 * time.Second, ready, state.KindOwnDialogGone}, {3 * time.Second, ready, ""}, {4 * time.Second, ownDialog, state.KindOwnDialog}},
			func(t *testing.T, es []state.Event) {
				if es[0].Facts.Screen == "" || es[2].Facts.Screen != es[0].Facts.Screen || es[0].Facts.Text != "Change effort level?" {
					t.Errorf("dialogs %+v %+v", es[0].Facts, es[2].Facts)
				}
			}},
		{"Claude at work after a dialog its hooks reported", state.Report{State: state.NeedsInput, Since: asked, Last: "Claude Code needs your input", LastStop: "Hi."}, []look{{0, capture(t, "fs-elicit-answered"), state.KindAtWork}, {time.Second, capture(t, "fs-elicit-answered"), ""}}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := journaltest.NewMemory()
			basis := reported(t, m, "000000001.1", "UserPromptSubmit")
			w := evWindow()
			for i, l := range tc.looks {
				s := &journaledStore{&fakeStore{screens: map[string]string{"%1": l.screen}}, m}
				at := now.Add(l.after)
				before := len(events(t, m))
				Look([]tmux.Window{w}, reportsOf(map[string]state.Report{"a1": tc.report}), nil, Sandboxes{}, at, s)
				w = kept(w, s.fakeStore)
				got := events(t, m)[before:]
				if k := journaltest.Kinds(got); k != l.want {
					t.Fatalf("look %d: appended %q, want %q", i, k, l.want)
				}
				for _, e := range got {
					if e.Source != state.SourceScreen || !e.Time.Equal(at) || e.Basis != basis || e.Aside != "" {
						t.Errorf("look %d: %+v", i, e)
					}
				}
			}
			if tc.check != nil {
				tc.check(t, since(events(t, m), basis))
			}
		})
	}
}

// Claude at work on the prompt it took from its queue after an Esc, and
// its wake-up after a turn the user ended, are events of the screen too.
func TestTheLookAppendsAQueuedPromptTakenAndAWakeUp(t *testing.T) {
	now := time.Unix(1000, 0)
	asked := now.Add(-time.Minute)
	const queued = "Run exactly this bash command in the foreground and show me its output: for i in $(seq 25); do echo tock $i; sleep 1; done"
	m := journaltest.NewMemory()
	basis := reported(t, m, "000000001.1", "UserPromptSubmit")
	rs := map[string]state.Report{"a1": {State: state.Working, Since: asked, Latest: asked, Last: "earlier", Prompt: queued, Queued: true}}
	take := func(string, string) error {
		rs["a1"] = state.Report{State: state.Working, Since: now, Latest: now, Last: "earlier", Prompt: queued}
		return nil
	}
	s := &journaledStore{&fakeStore{screens: map[string]string{"%1": capture(t, "fs-queued-taken-thinking")}}, m}
	Look([]tmux.Window{evWindow()}, reportsOf(rs), take, Sandboxes{}, now, s)
	es := since(events(t, m), basis)
	if len(es) != 1 || es[0].Kind != state.KindTookQueued || es[0].Source != state.SourceScreen || es[0].Basis != basis || !es[0].Time.Equal(now) {
		t.Fatalf("taken: %+v", es)
	}

	// The user ended the turn while a subagent ran; Claude woke the agent
	// since: the wake-up, dated from its report.
	m = journaltest.NewMemory()
	basis = reported(t, m, "000000001.1", "UserPromptSubmit")
	prompted, ended, woken := now.Add(-5*time.Minute), now.Add(-4*time.Minute), now.Add(-time.Minute)
	w := evWindow()
	w.Options["turnend"] = nanos(prompted) + " " + nanos(ended) + " " + declinedLine
	rs = map[string]state.Report{"a1": {State: state.Working, Since: prompted, Latest: woken, Woken: true}}
	for range 2 {
		s = &journaledStore{&fakeStore{}, m}
		Look([]tmux.Window{w}, reportsOf(rs), nil, Sandboxes{}, now, s)
		w = kept(w, s.fakeStore)
	}
	es = since(events(t, m), basis)
	if len(es) != 1 || es[0].Kind != state.KindWokeUp || es[0].Source != state.SourceScreen || !es[0].Facts.From.Equal(woken) || !es[0].Time.Equal(now) || es[0].Basis != basis {
		t.Fatalf("woken: %+v", es)
	}
}

// The pane's death is tmux's event, dated by tmux; sbx's answer that ends
// an agent is sbx's, with the moment sbx was asked. An answer sbx did not
// give (Sandboxes{}: an sbx too old, failed or slow) ends nobody and is no
// event, nor is one a report of the hook came after.
func TestTheLookAppendsThePanesDeathAndTheSandboxNotRunning(t *testing.T) {
	now := time.Unix(1000, 0)
	died := now.Add(-3 * time.Second)
	m := journaltest.NewMemory()
	basis := reported(t, m, "000000001.1", "Stop")
	rs := map[string]state.Report{"a1": {State: state.Done, Since: now.Add(-time.Minute)}}
	w := evWindow()
	w.PaneDead, w.DeadAt = true, died
	for i := range 3 {
		Look([]tmux.Window{w}, reportsOf(rs), nil, Sandboxes{}, now.Add(time.Duration(i)*time.Second), &journaledStore{&fakeStore{}, m})
	}
	es := since(events(t, m), basis)
	if len(es) != 1 || es[0].Source != state.SourceTmux || es[0].Kind != state.KindPaneDied || !es[0].Facts.From.Equal(died) || !es[0].Time.Equal(now) || es[0].Basis != basis {
		t.Fatalf("the pane's death: %+v", es)
	}

	m = journaltest.NewMemory()
	basis = reported(t, m, "000000001.1", "Stop")
	asked := now.Add(-time.Second)
	for _, sbxs := range []Sandboxes{{}, {Running: map[string]bool{"claude-app": true}, Asked: asked}} {
		Look([]tmux.Window{evWindow()}, reportsOf(rs), nil, sbxs, now, &journaledStore{&fakeStore{}, m})
	}
	stale := map[string]state.Report{"a1": {State: state.Done, Since: asked.Add(time.Millisecond)}}
	Look([]tmux.Window{evWindow()}, reportsOf(stale), nil, Sandboxes{Running: map[string]bool{}, Asked: asked}, now, &journaledStore{&fakeStore{}, m})
	if es := since(events(t, m), basis); len(es) != 0 {
		t.Fatalf("no answer, a running sandbox, or one a report overtook: %+v", es)
	}
	stopped := Sandboxes{Running: map[string]bool{"claude-lib": true}, Asked: asked}
	for range 2 {
		Look([]tmux.Window{evWindow()}, reportsOf(rs), nil, stopped, now, &journaledStore{&fakeStore{}, m})
	}
	es = since(events(t, m), basis)
	if len(es) != 1 || es[0].Source != state.SourceSbx || es[0].Kind != state.KindNotRunning || !es[0].Facts.Asked.Equal(asked) || es[0].Basis != basis {
		t.Fatalf("the sandbox not running: %+v", es)
	}
}

// A hundred looks at an agent whose screen stays the same add no event
// after the first: the journal holds it, whether or not the window still
// holds its record.
func TestAHundredLooksAtTheSameScreenAddOneEvent(t *testing.T) {
	now := time.Unix(1000, 0)
	m := journaltest.NewMemory()
	basis := reported(t, m, "000000001.1", "UserPromptSubmit")
	rs := map[string]state.Report{"a1": {State: state.Working, Since: now.Add(-time.Minute)}}
	w := evWindow()
	for i := range 100 {
		s := &journaledStore{&fakeStore{screens: map[string]string{"%1": declinedScreen()}}, m}
		Look([]tmux.Window{w}, reportsOf(rs), nil, Sandboxes{}, now.Add(time.Duration(i)*10*time.Millisecond), s)
		if i < 50 {
			// The first half as tmux keeps the record; the second as if
			// the window had lost it.
			w = kept(w, s.fakeStore)
		} else {
			w = evWindow()
		}
	}
	if es := since(events(t, m), basis); len(es) != 1 || es[0].Kind != state.KindTurnEnded {
		t.Fatalf("%d events: %s", len(es), journaltest.Kinds(es))
	}
}

// racingStore lets n looks read the journal before any of them appends:
// all see the same evidence missing, and all append it.
type racingStore struct {
	journaledStore
	read *sync.WaitGroup
}

func (s racingStore) Read(id string) (journal.Journal, error) {
	defer s.read.Done()
	return s.Memory.Read(id)
}

func (s racingStore) Append(id string, events ...state.Event) ([]state.Event, error) {
	s.read.Wait()
	return s.Memory.Append(id, events...)
}

// Of eight looks racing to append the same evidence, in eight hq
// processes, one event stands and the others are set aside as duplicates.
func TestOfEightLooksRacingOneEventStands(t *testing.T) {
	now := time.Unix(1000, 0)
	m := journaltest.NewMemory()
	basis := reported(t, m, "000000001.1", "UserPromptSubmit")
	rs := map[string]state.Report{"a1": {State: state.Working, Since: now.Add(-time.Minute)}}
	var read, done sync.WaitGroup
	read.Add(8)
	for i := range 8 {
		done.Go(func() {
			s := racingStore{journaledStore{&fakeStore{screens: map[string]string{"%1": declinedScreen()}}, m}, &read}
			Look([]tmux.Window{evWindow()}, reportsOf(rs), nil, Sandboxes{}, now.Add(time.Duration(i)*time.Millisecond), s)
		})
	}
	done.Wait()
	es := since(events(t, m), basis)
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
}

// overtakingStore has the hook's next report ingested between the look's
// reading of the journal and its append, as another hq process does.
type overtakingStore struct {
	journaledStore
	t *testing.T
}

func (s overtakingStore) Append(id string, events ...state.Event) ([]state.Event, error) {
	reported(s.t, s.Memory, "000000002.1", "PostToolUse")
	return s.Memory.Append(id, events...)
}

// Evidence whose basis a later report of the hook overtook before it was
// appended is set aside with that reason.
func TestEvidenceALaterReportOvertookIsSetAside(t *testing.T) {
	now := time.Unix(1000, 0)
	m := journaltest.NewMemory()
	basis := reported(t, m, "000000001.1", "UserPromptSubmit")
	rs := map[string]state.Report{"a1": {State: state.Working, Since: now.Add(-time.Minute)}}
	s := overtakingStore{journaledStore{&fakeStore{screens: map[string]string{"%1": declinedScreen()}}, m}, t}
	Look([]tmux.Window{evWindow()}, reportsOf(rs), nil, Sandboxes{}, now, s)
	es := since(events(t, m), basis)
	if len(es) != 2 || es[0].Kind != "PostToolUse" || es[1].Kind != state.KindTurnEnded || es[1].Aside != state.AsideOvertaken || es[1].Basis != basis {
		t.Fatalf("%+v", es)
	}
}

// A look without the journals, or one whose journal cannot be read, still
// settles the screens; an outdated agent's journal is not read at all.
func TestALookWithoutAJournalAppendsNothing(t *testing.T) {
	now := time.Unix(1000, 0)
	rs := map[string]state.Report{"a1": {State: state.Working, Since: now.Add(-time.Minute)}, "id1": {State: state.Working, Since: now.Add(-time.Minute)}}
	m := journaltest.NewMemory()
	// lookWindow's id is no id hq issues: its journal cannot be read.
	ws := []tmux.Window{lookWindow("1"), evWindow()}
	ws[1].Options[GenerationOption] = "1"
	as := Look(ws, reportsOf(rs), nil, Sandboxes{}, now, &journaledStore{&fakeStore{screens: map[string]string{"%1": declinedScreen()}}, m})
	if as[0].State != state.Done || len(m.Agents()) != 0 {
		t.Fatalf("%+v, journals %v", as[0], m.Agents())
	}
	if a := Look([]tmux.Window{evWindow()}, reportsOf(rs), nil, Sandboxes{}, now, &fakeStore{screens: map[string]string{"%1": declinedScreen()}})[0]; a.State != state.Done {
		t.Fatalf("without journals: %+v", a)
	}
}
