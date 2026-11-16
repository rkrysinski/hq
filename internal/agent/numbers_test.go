package agent

import (
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/state"
	"github.com/rkrysinski/hq/internal/tmux"
)

// seqs are an agent's two numbers as a test compares them: 0 for none.
func seqs(a Agent) (stateSeq, completionSeq int64) {
	stateSeq, _ = a.StateSeq()
	completionSeq, _ = a.CompletionSeq()
	return stateSeq, completionSeq
}

// latest is the number of the last event of agent id's journal.
func (s *memStore) latest(t *testing.T, id string) int64 {
	t.Helper()
	es := s.journalOf(t, id)
	return es[len(es)-1].Number
}

// An agent carries the number of the event at which its state last
// changed, and the number of the one that ended its last completed turn
// (ADR 0019): a report that leaves its state as it was moves neither, and
// a turn that starts moves only the first.
func TestAnAgentCarriesTheNumbersOfItsLastChangeAndOfItsLastCompletedTurn(t *testing.T) {
	ws := []tmux.Window{lookWindow("1")}
	s := newStore(nil)
	look := func(r state.Report) Agent {
		t.Helper()
		as := s.look(ws, map[string]state.Report{"a1": r}, Sandboxes{}, time.Unix(1000, 0))
		return as[0]
	}

	a := look(state.Report{State: state.Working, Since: time.Unix(200, 0), Prompt: "Run the tests"})
	prompt := s.latest(t, "a1")
	if st, c := seqs(a); st != prompt || c != 0 || a.State != state.Working {
		t.Fatalf("working: state_seq %d completion_seq %d (prompt %d) %s", st, c, prompt, a.State)
	}
	if _, ok := a.CompletionSeq(); ok {
		t.Fatal("a completion before the first completed turn")
	}

	a = look(state.Report{State: state.Done, Since: time.Unix(300, 0), Last: "Done: tests pass"})
	stop := s.latest(t, "a1")
	if st, c := seqs(a); st != stop || c != stop || stop <= prompt {
		t.Fatalf("done: state_seq %d completion_seq %d (stop %d)", st, c, stop)
	}
	// A look more, with nothing new: nothing moves.
	if st, c := seqs(look(state.Report{State: state.Done, Since: time.Unix(300, 0), Last: "Done: tests pass"})); st != stop || c != stop {
		t.Fatalf("again: state_seq %d completion_seq %d", st, c)
	}

	a = look(state.Report{State: state.Working, Since: time.Unix(400, 0), Prompt: "Now the docs"})
	next := s.latest(t, "a1")
	if st, c := seqs(a); st != next || c != stop {
		t.Fatalf("working again: state_seq %d completion_seq %d (prompt %d, stop %d)", st, c, next, stop)
	}
	if n, _ := s.Through(); a.Through() != n || a.Through() < next {
		t.Fatalf("through %d, counter %d", a.Through(), n)
	}
}

// An agent with no event behind its state has no numbers (#166, the
// operator's decision 3A): one starting before its journal holds any
// event, one ended whose journal cannot be read, and an outdated one,
// whose journal hq does not read. Only the first was read, so only it
// gives the look a counter.
func TestAnAgentWithNoEventBehindItsStateHasNoNumbers(t *testing.T) {
	ws := []tmux.Window{lookWindow("1")}
	s := newStore(nil)
	_, _ = s.Append("a9", state.Event{Source: state.SourceHQ, Kind: state.KindLaunched, Time: time.Unix(100, 0), Facts: state.Facts{Repo: "/w/other"}})
	a := Look(ws, Sandboxes{}, time.Unix(1000, 0), s, nil)[0]
	if _, ok := a.StateSeq(); ok || a.State != state.Starting || a.Through() != 1 {
		t.Fatalf("starting with no journal: %+v through %d", a, a.Through())
	}

	ws[0].PaneDead = true
	a = Look(ws, Sandboxes{}, time.Unix(1000, 0), failingStore{newStore(nil)}, nil)[0]
	if _, ok := a.StateSeq(); ok || a.State != state.Ended || a.Through() != 0 {
		t.Fatalf("unread: %+v", a)
	}
	if _, ok := a.CompletionSeq(); ok {
		t.Fatal("unread: a completion")
	}

	ws[0].Options[GenerationOption] = "2"
	a = Look(ws, Sandboxes{}, time.Unix(1000, 0), s, nil)[0]
	if _, ok := a.StateSeq(); ok || a.State != state.Outdated || a.Through() != 0 {
		t.Fatalf("outdated: %+v", a)
	}
}

// An agent gone from tmux while a command waited on it is ended now, with
// what it last showed and no numbers: its journal went with it.
func TestAGoneAgentIsEndedWithNoNumbers(t *testing.T) {
	ws := []tmux.Window{lookWindow("1")}
	s := newStore(nil)
	a := s.look(ws, map[string]state.Report{"a1": {State: state.Done, Since: time.Unix(300, 0), Last: "Done: tests pass"}}, Sandboxes{}, time.Unix(1000, 0))[0]
	g := a.Gone(time.Unix(2000, 0))
	if _, ok := g.StateSeq(); ok || g.State != state.Ended || !g.Since.Equal(time.Unix(2000, 0)) || g.Last != "Done: tests pass" {
		t.Fatalf("gone: %+v", g)
	}
	if _, ok := g.CompletionSeq(); ok {
		t.Fatal("gone: a completion")
	}
	a.Last = ""
	if g := a.Gone(time.Unix(2000, 0)); g.Last != EndedLast {
		t.Fatalf("gone without a message: %q", g.Last)
	}
}

// The look saw every change numbered up to the lowest counter of its
// reads: an agent read early may have changed since, numbered below the
// counter of an agent read later. An outdated agent is not read and does
// not count; an agent whose journal could not be read, or no agent read at
// all, leaves the look nothing to vouch for.
func TestTheLookSawEveryChangeUpToTheLowestCounterOfItsReads(t *testing.T) {
	read := func(through int64) Agent { return Agent{State: state.Done, through: through} }
	outdated := Agent{State: state.Outdated}
	for _, tc := range []struct {
		as   []Agent
		n    int64
		ok   bool
		name string
	}{
		{[]Agent{read(7), read(5), read(9)}, 5, true, "the lowest"},
		{[]Agent{outdated, read(4)}, 4, true, "an outdated one aside"},
		{[]Agent{read(4), {State: state.Ended}}, 0, false, "one unread"},
		{[]Agent{outdated}, 0, false, "none read"},
		{nil, 0, false, "no agent"},
	} {
		if n, ok := SeenThrough(tc.as); n != tc.n || ok != tc.ok {
			t.Errorf("%s: %d %v", tc.name, n, ok)
		}
	}
}

// An agent hq has just launched is starting from its launch, the event its
// state rests on, before any report: its state number is the launch's
// (#166, the operator's decision 3A gives none only to an agent with no
// event behind its state).
func TestAnAgentJustLaunchedHasTheLaunchsNumber(t *testing.T) {
	ws := []tmux.Window{lookWindow("1")}
	s := newStore(nil)
	_, _ = s.Append("a1", state.Event{Source: state.SourceHQ, Kind: state.KindLaunched, Time: time.Unix(100, 0), Facts: state.Facts{Repo: "/w/app"}})
	launched := s.latest(t, "a1")
	a := Look(ws, Sandboxes{}, time.Unix(1000, 0), s, nil)[0]
	if st, ok := a.StateSeq(); !ok || st != launched || a.State != state.Starting {
		t.Fatalf("just launched: %s, state_seq %d %v, want %d", a.State, st, ok, launched)
	}
	if _, ok := a.CompletionSeq(); ok {
		t.Fatal("just launched: a completion")
	}
	// Its first report moves it.
	a = s.look(ws, map[string]state.Report{"a1": {State: state.Working, Since: time.Unix(200, 0), Prompt: "go"}}, Sandboxes{}, time.Unix(1000, 0))[0]
	if st, _ := a.StateSeq(); st != s.latest(t, "a1") || st <= launched {
		t.Fatalf("after its first report: state_seq %d", st)
	}
}
