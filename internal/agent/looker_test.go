package agent

import (
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/journal"
	"github.com/rkrysinski/hq/internal/journal/journaltest"
	"github.com/rkrysinski/hq/internal/sbx"
	"github.com/rkrysinski/hq/internal/state"
	"github.com/rkrysinski/hq/internal/tmux"
	"github.com/rkrysinski/hq/internal/version"
)

// The rules of the look and the look loop, one test each, at the module's
// interface (agent.Looker, design §3.8, §7.2): the commands' tests test
// what a command does with what the module gives.

// lookerStore is the journals' store of a Looker's tests (memStore), with
// what its reads of agent id's journal give (through, by id, the counter of
// the latest), a hook run as a look reads one (onRead, e.g. another process
// numbering a change meanwhile), journals that cannot be read, a counter
// that cannot be read on its own, and how many times the counter was read
// on its own.
type lookerStore struct {
	*memStore
	mu          sync.Mutex
	through     map[string]int64
	onRead      func(id string)
	unreadable  map[string]bool
	noCounter   bool
	counterRead int
}

func (s *lookerStore) ReadFrom(id string, from int64) (journal.Journal, error) {
	if s.onRead != nil {
		s.onRead(id)
	}
	if s.unreadable[id] {
		return journal.Journal{}, errors.New("unreadable")
	}
	j, err := s.memStore.ReadFrom(id, from)
	s.mu.Lock()
	s.through[id] = j.Through
	s.mu.Unlock()
	return j, err
}

func (s *lookerStore) Through() (int64, error) {
	s.mu.Lock()
	s.counterRead++
	s.mu.Unlock()
	if s.noCounter {
		return 0, errors.New("the state directory is locked")
	}
	return s.memStore.Through()
}

// readThrough is the counter of the latest read of agent id's journal.
func (s *lookerStore) readThrough(id string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.through[id]
}

// lookerFakes are a Looker's dependencies as its tests fake them: the home
// windows tmux lists, the journals' store with the screens tmux captures,
// sbx's answer and a clock that Sleep alone moves on. onAsk runs as sbx is
// asked, before it answers, onSleep as Sleep starts, before it moves the
// clock on, and onList as tmux is asked for the windows, before it lists
// them.
type lookerFakes struct {
	mu         sync.Mutex
	store      *lookerStore
	windows    []tmux.Window
	windowsErr error
	listed     int // times tmux listed the windows
	sandboxes  []sbx.Sandbox
	sbxErr     error           // sbx's answer instead of sandboxes
	asked      []time.Duration // when sbx was asked, from start
	answered   int             // questions sbx answered
	onAsk      func()
	start, now time.Time
	slept      []time.Duration
	onSleep    func()
	onList     func()
	folds      *Folds
}

// newLookerFakes has no agent yet, the sandbox claude-app running.
func newLookerFakes() *lookerFakes {
	start := time.Unix(10_000, 0)
	return &lookerFakes{
		store:     &lookerStore{memStore: newStore(map[string]string{}), through: map[string]int64{}, unreadable: map[string]bool{}},
		sandboxes: []sbx.Sandbox{{Name: "claude-app", Status: "running"}},
		start:     start, now: start, folds: NewFolds(),
	}
}

// looker is the Looker of these fakes.
func (f *lookerFakes) looker() Looker {
	return Looker{
		Sources: lookerSources{f.store, f},
		Sandboxes: func() ([]sbx.Sandbox, error) {
			f.mu.Lock()
			f.asked = append(f.asked, f.now.Sub(f.start))
			onAsk := f.onAsk
			f.mu.Unlock()
			if onAsk != nil {
				onAsk()
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			f.answered++
			if f.sbxErr != nil {
				return nil, f.sbxErr
			}
			return slices.Clone(f.sandboxes), nil
		},
		Now: func() time.Time {
			f.mu.Lock()
			defer f.mu.Unlock()
			return f.now
		},
		Sleep: func(d time.Duration) {
			f.mu.Lock()
			onSleep := f.onSleep
			f.mu.Unlock()
			if onSleep != nil {
				onSleep()
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			f.now = f.now.Add(d)
			f.slept = append(f.slept, d)
		},
		Folds: f.folds,
	}
}

// lookerSources are a Looker's sources as its tests fake them: the
// journals' store with the home windows tmux lists.
type lookerSources struct {
	*lookerStore
	f *lookerFakes
}

func (s lookerSources) Windows() ([]tmux.Window, error) {
	s.f.mu.Lock()
	onList := s.f.onList
	s.f.mu.Unlock()
	if onList != nil {
		onList()
	}
	s.f.mu.Lock()
	defer s.f.mu.Unlock()
	s.f.listed++
	return slices.Clone(s.f.windows), s.f.windowsErr
}

// agent adds the home window of agent an of /w/app, in the sandbox
// claude-app, its hook having reported r.
func (f *lookerFakes) agent(n string, r journaltest.HookState) {
	w := lookWindow(n)
	w.Options["sandbox"] = "claude-app"
	f.windows = append(f.windows, w)
	f.store.report("a"+n, r)
}

// done adds agent an, done an hour before the fakes' start.
func (f *lookerFakes) done(n string) {
	f.agent(n, journaltest.HookState{State: state.Done, Since: f.start.Add(-time.Hour), Last: "hi"})
}

// outdated adds agent an, started by the hq of the generation before.
func (f *lookerFakes) outdated(n string) {
	f.done(n)
	f.windows[len(f.windows)-1].Options[GenerationOption] = strconv.Itoa(version.Generation - 1)
}

// at is how long the clock has moved on since the start.
func (f *lookerFakes) at() time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now.Sub(f.start)
}

// look is a look in variant v, which the test needs to succeed.
func (f *lookerFakes) look(t *testing.T, v Variant) Seen {
	t.Helper()
	s, err := f.looker().Look(v)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// numberMeanwhile has another process number a change: the launch of an
// agent this look does not show.
func (f *lookerFakes) numberMeanwhile() {
	_, _ = f.store.Append("a9", state.Event{Source: state.SourceHQ, Kind: state.KindLaunched, Time: f.start, Facts: state.Facts{Repo: "/w/other"}})
}

// counter is what the journals' counter holds now.
func (f *lookerFakes) counter(t *testing.T) int64 {
	t.Helper()
	n, err := f.store.memStore.Through()
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// named is the agent of as named name.
func named(t *testing.T, as []Agent, name string) Agent {
	t.Helper()
	a, ok := Find(as, name)
	if !ok {
		t.Fatalf("no %s among %s", name, names(as))
	}
	return a
}

// A look leaves out an agent killed while it looked: hq kill kills the home
// window, then removes the journal, between the look's listing of the
// windows and its read of the journal (#239). An agent whose journal holds
// nothing yet while its window stands stays, and so does every agent when
// tmux cannot list the windows again to tell.
func TestALookLeavesOutAnAgentKilledWhileItLooked(t *testing.T) {
	killing := func(f *lookerFakes, also func()) {
		f.store.onRead = func(id string) {
			if id != "a1" || f.store.gone["@1"] {
				return
			}
			f.mu.Lock()
			f.windows = slices.DeleteFunc(f.windows, func(w tmux.Window) bool { return w.ID == "@1" })
			f.mu.Unlock()
			f.store.gone["@1"] = true
			_ = f.store.Remove("/w/app", "a1")
			also()
		}
	}
	f := newLookerFakes()
	f.done("1")
	f.done("2")
	f.look(t, Shown)
	killing(f, func() {})
	if s := f.look(t, Shown); names(s.Agents) != "a2" {
		t.Fatalf("killed meanwhile: %s", names(s.Agents))
	}

	f = newLookerFakes()
	f.windows = []tmux.Window{lookWindow("1")}
	if s := f.look(t, Shown); names(s.Agents) != "a1" || s.Agents[0].State != state.Starting {
		t.Fatalf("no journal yet, the window standing: %+v", s.Agents)
	}

	f = newLookerFakes()
	f.done("1")
	f.done("2")
	f.look(t, Shown)
	killing(f, func() { f.mu.Lock(); f.windowsErr = errors.New("tmux gone"); f.mu.Unlock() })
	if s := f.look(t, Shown); names(s.Agents) != "a1 a2" {
		t.Fatalf("tmux cannot tell: %s", names(s.Agents))
	}
}

// A look reads the journals' counter before it lists the windows
// (Seen.Counter): a change another process numbers while tmux lists them
// is after it. A counter that cannot be read is not read. The looks of
// hq update and of the chords and docking (Updating, Docking) read no
// counter, as those commands read none before the Looker; the others read
// it once a look.
func TestALookReadsTheCounterBeforeItListsTheWindows(t *testing.T) {
	f := newLookerFakes()
	f.done("1")
	f.look(t, Shown)
	before := f.counter(t)
	f.onList = f.numberMeanwhile
	s, err := f.looker().Look(Shown)
	f.onList = nil
	if c := s.Counter(); err != nil || c.N != before || !c.Read || f.counter(t) != before+1 {
		t.Fatalf("counter %+v, want %d, the counter before the windows (%d now): %v", c, before, f.counter(t), err)
	}

	f.store.noCounter = true
	if c := f.look(t, Shown).Counter(); c.Read {
		t.Fatalf("a counter that cannot be read read: %+v", c)
	}

	for _, tc := range []struct {
		name  string
		v     Variant
		reads bool
	}{
		{"Shown", Shown, true},
		{"Explained", Explained, true},
		{"Acting", Acting, true},
		{"Updating", Updating, false},
		{"Docking", Docking, false},
	} {
		f := newLookerFakes()
		f.done("1")
		f.look(t, tc.v)
		f.store.counterRead = 0
		c := f.look(t, tc.v).Counter()
		if read := f.store.counterRead == 1 && c.Read && c.N == f.counter(t); read != tc.reads || !read && (f.store.counterRead != 0 || c != Counter{}) {
			t.Errorf("%s: the counter read %d times, the look's %+v (the counter %d), want it read: %v", tc.name, f.store.counterRead, c, f.counter(t), tc.reads)
		}
	}
}

// A look notes the moment it asks sbx before it asks (#48): a report the
// agent makes while sbx answers is newer than the answer, which does not
// end the agent then, though it lists its sandbox as stopped; a report
// older than that moment is ended by it. The look loop's first look
// (Looker.Loop) and the list's asking (Looker.AskSbx) note it the same.
func TestALookNotesTheMomentItAsksSbxBeforeItAsks(t *testing.T) {
	answering := func() *lookerFakes {
		f := newLookerFakes()
		f.sandboxes = []sbx.Sandbox{{Name: "claude-app", Status: "stopped"}}
		f.agent("1", journaltest.HookState{State: state.Working, Since: f.start.Add(-time.Minute), Last: "on it"})
		f.done("2")
		f.onAsk = func() {
			f.mu.Lock()
			f.now = f.now.Add(time.Second)
			f.mu.Unlock()
			f.store.report("a1", journaltest.HookState{State: state.Done, Since: f.start.Add(500 * time.Millisecond), Last: "Done: hello"})
		}
		return f
	}
	check := func(entry string, as []Agent) {
		t.Helper()
		if a := named(t, as, "a1"); a.State != state.Done {
			t.Errorf("%s: a1 %s, want done: it reported while sbx answered", entry, a.State)
		}
		if a := named(t, as, "a2"); a.State != state.Ended {
			t.Errorf("%s: a2 %s, want ended: it reported before sbx was asked", entry, a.State)
		}
	}
	f := answering()
	check("Look", f.look(t, Shown).Agents)

	f = answering()
	var first []Agent
	if err := f.looker().Loop(func(s Seen, _ bool) (bool, error) { first = s.Agents; return true, nil }); err != nil {
		t.Fatal(err)
	}
	check("Loop", first)

	f = answering()
	if running, err := f.looker().AskSbx(); err != nil || !running.Asked.Equal(f.start) {
		t.Fatalf("AskSbx: asked at %v, want %v: %v", running.Asked, f.start, err)
	}
}

// Each kind of look takes an sbx too old for hq, one that fails and one
// that does not answer in time as its variant says (design §3.6, *The
// oldest sbx*): the looks of hq ls, hq read, hq send and hq answer (Shown,
// Acting) fail on one too old and take the others as no answer; hq
// explain's (Explained) takes all three as no answer; hq update's
// (Updating) fails on each; the chords' and docking's (Docking) do not ask
// sbx; the look loop (Looker.Loop) takes each as no answer. A look that
// fails gives sbx's own error (SbxError); no answer ends nobody, where an
// answer listing the sandbox stopped would end the agent.
func TestEachKindOfLookTakesAnSbxThatCannotAnswerAsItsVariantSays(t *testing.T) {
	tooOld := &sbx.TooOldError{Version: "0.47.0"}
	failing := errors.New("sbx: exit status 1")
	slow := errors.New("sbx: no answer within 5s")
	answering := func(sbxErr error) *lookerFakes {
		f := newLookerFakes()
		f.sandboxes = []sbx.Sandbox{{Name: "claude-app", Status: "stopped"}}
		f.done("1")
		f.sbxErr = sbxErr
		return f
	}
	noAnswer := func(t *testing.T, what string, as []Agent, err error) {
		t.Helper()
		if err != nil || names(as) != "a1" || named(t, as, "a1").State == state.Ended {
			t.Errorf("%s: %s, %v; want a1 not ended and no error", what, names(as), err)
		}
	}
	if s := answering(nil).look(t, Shown); named(t, s.Agents, "a1").State != state.Ended {
		t.Fatalf("sbx answering: a1 %s, want ended by the sandbox stopped", named(t, s.Agents, "a1").State)
	}
	for _, tc := range []struct {
		name  string
		v     Variant
		fails []error
		asks  bool
	}{
		{"Shown", Shown, []error{tooOld}, true},
		{"Acting", Acting, []error{tooOld}, true},
		{"Explained", Explained, nil, true},
		{"Updating", Updating, []error{tooOld, failing, slow}, true},
		{"Docking", Docking, nil, false},
	} {
		for _, sbxErr := range []error{tooOld, failing, slow} {
			what := tc.name + ", " + sbxErr.Error()
			f := answering(sbxErr)
			s, err := f.looker().Look(tc.v)
			if asked := len(f.asked) > 0; asked != tc.asks {
				t.Errorf("%s: sbx asked: %v, want %v", what, asked, tc.asks)
			}
			if !slices.Contains(tc.fails, sbxErr) {
				noAnswer(t, what, s.Agents, err)
				continue
			}
			var e *SbxError
			if !errors.As(err, &e) || !errors.Is(err, sbxErr) || s.Agents != nil {
				t.Errorf("%s: %s, %v; want the look failed with sbx's error", what, names(s.Agents), err)
			}
		}
	}
	for _, sbxErr := range []error{tooOld, failing, slow} {
		f := answering(sbxErr)
		var first []Agent
		err := f.looker().Loop(func(s Seen, _ bool) (bool, error) { first = s.Agents; return true, nil })
		noAnswer(t, "Loop, "+sbxErr.Error(), first, err)
	}
}

// rewound is the screen of a turn the user rewound: the prompt sent is
// back in the box, under the end of the turn.
func rewound(sent string) string {
	rule := strings.Repeat("─", 30)
	return "✻ Baked for 2s · done\n" + rule + "\n❯ " + sent + "\n" + rule + "\n  ⏵⏵ bypass permissions on\n"
}

// A look in a variant that takes the second look (Shown, Explained) looks
// again RestDelay later when a working agent's screen is at rest, and gives
// what that look saw: here a rewound turn, done since its screen was first
// seen at rest. The other variants, and a look at no screen at rest, look
// once.
func TestALookLooksAgainAtAScreenAtRestInTheVariantsThatTakeIt(t *testing.T) {
	const sent = "Write a poem about the sea."
	for _, tc := range []struct {
		name  string
		v     Variant
		again bool
	}{
		{"Shown", Shown, true},
		{"Explained", Explained, true},
		{"Acting", Acting, false},
		{"Updating", Updating, false},
		{"Docking", Docking, false},
	} {
		f := newLookerFakes()
		f.agent("1", journaltest.HookState{State: state.Working, Since: f.start.Add(-30 * time.Second), Last: "earlier reply", Prompt: sent})
		f.store.screens["%1"] = rewound(sent)
		a := named(t, f.look(t, tc.v).Agents, "a1")
		switch {
		case tc.again && (!slices.Equal(f.slept, []time.Duration{RestDelay}) || a.State != state.Done || a.Last != "Interrupted" || !a.Since.Equal(f.start)):
			t.Errorf("%s: slept %v; a1 %s since %v %q, want done since %v", tc.name, f.slept, a.State, a.Since, a.Last, f.start)
		case !tc.again && (len(f.slept) != 0 || a.State != state.Working):
			t.Errorf("%s: slept %v; a1 %s, want one look", tc.name, f.slept, a.State)
		}
	}
	f := newLookerFakes()
	f.done("1")
	if f.look(t, Shown); len(f.slept) != 0 {
		t.Fatalf("no screen at rest: slept %v", f.slept)
	}
}

// ownDialog is the screen of a dialog of Claude Code's own after a turn,
// asking question n.
func ownDialog(n int) string {
	return "● hi\n" + strings.Repeat("▔", 40) + "\n   Change effort level " + strconv.Itoa(n) + "?\n\n   ❯ 1. Yes, switch to max\n     2. No, go back\n"
}

// The look's cursor (Looker.Cursor, ADR 0019) stands for no change the look
// did not see: the lowest counter of its reads of the agents' journals,
// once the windows listed again show that no agent appeared meanwhile; the
// counter before it listed the windows when its reads are not past it
// (with no listing again), when an agent appeared or tmux cannot list them
// again, and when a journal could not be read. An outdated agent, whose
// journal is not read, does not count. Cover looks again, up to
// CoverLooks looks in all, until the cursor stands for every agent the
// look shows (hq ls --json, #166), and fails as a look again fails.
func TestTheLooksCursorStandsForWhatItSawAndCoversEveryAgentItShows(t *testing.T) {
	// a1 and a2 settled by a first look; then a2 is read past the counter,
	// another process numbering a change at each read of it.
	racing := func(also func(f *lookerFakes)) (*lookerFakes, Seen) {
		f := newLookerFakes()
		f.done("1")
		f.done("2")
		also(f)
		f.look(t, Shown)
		f.store.onRead = func(id string) {
			if id == "a2" {
				f.numberMeanwhile()
			}
		}
		s := f.look(t, Shown)
		f.store.onRead = nil
		f.listed = 0
		return f, s
	}

	f := newLookerFakes()
	f.done("1")
	f.look(t, Shown)
	f.listed = 0
	s := f.look(t, Shown)
	if n, cursor := s.Counter().N, f.looker().Cursor(s); cursor != n || f.listed != 1 {
		t.Fatalf("quiet: cursor %d, counter %d, windows listed %d times", cursor, n, f.listed)
	}

	f, s = racing(func(*lookerFakes) {})
	lowest, highest := f.store.readThrough("a1"), f.store.readThrough("a2")
	if n, cursor := s.Counter().N, f.looker().Cursor(s); cursor != lowest || lowest <= n || lowest >= highest || f.listed != 1 {
		t.Fatalf("read past the counter: cursor %d, want %d, the lowest read (highest %d, counter %d); windows listed again %d times", cursor, lowest, highest, n, f.listed)
	}
	f, s = racing(func(f *lookerFakes) { f.outdated("3") })
	if cursor := f.looker().Cursor(s); cursor != f.store.readThrough("a1") {
		t.Fatalf("an outdated agent aside: cursor %d, want %d", cursor, f.store.readThrough("a1"))
	}
	for name, meanwhile := range map[string]func(f *lookerFakes){
		"an agent appeared":           func(f *lookerFakes) { f.windows = append(f.windows, lookWindow("4")) },
		"tmux cannot list them again": func(f *lookerFakes) { f.windowsErr = errors.New("tmux gone") },
	} {
		f, s := racing(func(*lookerFakes) {})
		meanwhile(f)
		if n, cursor := s.Counter().N, f.looker().Cursor(s); cursor != n {
			t.Errorf("%s: cursor %d, want %d, the counter before the windows", name, cursor, n)
		}
	}
	f, s = racing(func(f *lookerFakes) { f.done("3"); f.store.unreadable["a3"] = true })
	if n, cursor := s.Counter().N, f.looker().Cursor(s); cursor != n || f.listed != 0 {
		t.Fatalf("a journal unread: cursor %d, want %d; windows listed again %d times", cursor, n, f.listed)
	}

	// b's screen shows a dialog of Claude's own, which a look appends for b
	// after it read a: the look shows b at a change above its cursor.
	dialogOnA2 := func(alternate bool) *lookerFakes {
		f := newLookerFakes()
		f.done("1")
		f.done("2")
		rule := strings.Repeat("─", 40)
		f.store.screens["%1"] = "● hi\n" + rule + "\n❯ \n" + rule + "\n  footer\n"
		f.store.screens["%2"] = ownDialog(0)
		if alternate {
			f.onAsk = func() {
				if len(f.asked) > 2*CoverLooks {
					t.Fatalf("still looking at look %d", len(f.asked))
				}
				f.store.screens["%2"] = ownDialog(len(f.asked))
			}
		}
		return f
	}
	f = dialogOnA2(false)
	covered, cursor, err := f.looker().Cover(f.look(t, Shown))
	if last := f.looker().Cursor(covered); err != nil || len(f.asked) != 2 || cursor != last {
		t.Fatalf("covering: %d looks, cursor %d (the last look's %d): %v", len(f.asked), cursor, last, err)
	}
	for _, a := range covered.Agents {
		if seq, ok := a.StateSeq(); !ok || seq > cursor {
			t.Errorf("%s at %d, above the cursor %d", a.Name, seq, cursor)
		}
	}
	f = dialogOnA2(true)
	if covered, _, err := f.looker().Cover(f.look(t, Shown)); err != nil || len(f.asked) != CoverLooks || len(covered.Agents) != 2 {
		t.Fatalf("changing at every look: %d looks, want %d; %s: %v", len(f.asked), CoverLooks, names(covered.Agents), err)
	}
	f = dialogOnA2(false)
	s = f.look(t, Shown)
	f.windowsErr = errors.New("tmux gone")
	var tmuxErr *TmuxError
	if _, _, err := f.looker().Cover(s); !errors.As(err, &tmuxErr) {
		t.Fatalf("a look again failing: %v", err)
	}
}

// An agent's own cursor (Seen.CursorOf, hq read) is the counter of the
// look's read of its journal, whatever the look read of the others after
// it; for an agent whose journal the look did not read (an outdated one),
// the counter before the look listed the windows.
func TestAnAgentsCursorIsTheCounterOfTheLooksReadOfItsJournal(t *testing.T) {
	f := newLookerFakes()
	f.done("1")
	f.done("2")
	f.outdated("3")
	f.look(t, Shown)
	f.store.onRead = func(id string) {
		if id == "a2" {
			f.numberMeanwhile()
		}
	}
	s := f.look(t, Shown)
	n := s.Counter().N
	for name, want := range map[string]int64{"a1": f.store.readThrough("a1"), "a2": f.store.readThrough("a2"), "a3": n} {
		if got := s.CursorOf(named(t, s.Agents, name)); got != want {
			t.Errorf("%s: cursor %d, want %d", name, got, want)
		}
	}
	if s.CursorOf(named(t, s.Agents, "a1")) == s.CursorOf(named(t, s.Agents, "a2")) {
		t.Fatal("a1 and a2 read at the same counter: the test tells nothing")
	}
}

// The counter for a change an hq command is about to cause in an agent
// (Looker.CounterBefore: a message, an answer) is what the counter holds
// then, not what the look before saw: a change another process numbers in
// between comes before it. When the counter cannot be read then, it is the
// counter of the look's read of the agent's journal.
func TestTheCounterBeforeAnActIsTheCounterThen(t *testing.T) {
	f := newLookerFakes()
	f.done("1")
	a := named(t, f.look(t, Acting).Agents, "a1")
	f.numberMeanwhile()
	if got, want := f.looker().CounterBefore(a), f.counter(t); got != want || got <= f.store.readThrough("a1") {
		t.Fatalf("counter before %d, want %d, the counter now (the look read at %d)", got, want, f.store.readThrough("a1"))
	}
	f.store.noCounter = true
	if got, want := f.looker().CounterBefore(a), f.store.readThrough("a1"); got != want {
		t.Fatalf("no counter: %d, want %d, the look's read", got, want)
	}
}

// A look gives the agent of a name or its refusal, in today's words: none
// of that name (Seen.Named), an agent killed meanwhile included, and, for a
// command that acts on the agent's state, an outdated one (Seen.Usable,
// Agent.Refusal).
func TestALookGivesTheAgentNamedOrItsRefusal(t *testing.T) {
	f := newLookerFakes()
	f.done("1")
	f.outdated("2")
	f.done("3")
	f.look(t, Shown)
	f.windows = f.windows[:2]
	s := f.look(t, Shown)
	var none *NotFoundError
	var old *OutdatedError
	for _, name := range []string{"x", "a3"} {
		_, named := s.Named(name)
		_, usable := s.Usable(name)
		for _, err := range []error{named, usable} {
			if !errors.As(err, &none) || err.Error() != "no agent '"+name+"' (see hq ls)" {
				t.Errorf("%s: %v", name, err)
			}
		}
	}
	if a, err := s.Named("a2"); err != nil || a.Name != "a2" || a.State != state.Outdated {
		t.Errorf("named outdated: %+v %v", a, err)
	}
	if _, err := s.Usable("a2"); !errors.As(err, &old) || err.Error() != "a2 was started by an older hq: kill it and start it again" {
		t.Errorf("usable outdated: %v", err)
	}
	if a, err := s.Usable("a1"); err != nil || a.Name != "a1" {
		t.Errorf("usable: %+v %v", a, err)
	}
	if err := named(t, s.Agents, "a1").Refusal(); err != nil {
		t.Errorf("refusal of a1: %v", err)
	}
	if err := named(t, s.Agents, "a2").Refusal(); !errors.As(err, &old) {
		t.Errorf("refusal of a2: %v", err)
	}
}

// The look loop (Looker.Loop) looks every LoopEvery until its step is done.
// It asks sbx before the first look, which waits for the answer, and then
// in the background at most every SbxEvery, each look taking the latest
// answer it has: a sandbox stopped from the start ends its agent at the
// first look; one that stops after the first question ends its agent with
// the answer to the question a second later, at a look before the next
// question. A look that fails ends the loop with its error, and so does a
// step.
func TestTheLookLoopLooksEvery250msAndAsksSbxInTheBackgroundOnceASecond(t *testing.T) {
	f := newLookerFakes()
	f.agent("1", journaltest.HookState{State: state.Working, Since: f.start.Add(-time.Minute), Last: "on it"})
	f.agent("2", journaltest.HookState{State: state.Working, Since: f.start.Add(-time.Minute), Last: "on it"})
	f.windows[0].Options["sandbox"] = "claude-x"
	f.windows[1].Options["sandbox"] = "claude-y"
	f.sandboxes = []sbx.Sandbox{{Name: "claude-x", Status: "stopped"}, {Name: "claude-y", Status: "running"}}
	// The clock moves on only once the question a look at a whole second
	// asks in the background is asked, so it is asked at that second, and
	// once every question asked is answered, with a moment for the answer
	// to reach the loop; the waits share one bound.
	bound := time.Now().Add(5 * time.Second)
	var missed []time.Duration
	f.onSleep = func() {
		f.mu.Lock()
		at := f.now.Sub(f.start)
		if at >= 500*time.Millisecond {
			f.sandboxes[1].Status = "stopped"
		}
		f.mu.Unlock()
		want := 0
		if at >= time.Second && at%time.Second == 0 {
			want = 1 + int(at/time.Second)
		}
		for ; ; time.Sleep(time.Millisecond) {
			f.mu.Lock()
			asked, answered := len(f.asked), f.answered
			f.mu.Unlock()
			if asked >= want && answered == asked {
				break
			}
			if time.Now().After(bound) {
				missed = append(missed, at)
				return
			}
		}
		time.Sleep(time.Millisecond)
	}
	type step struct {
		at     time.Duration
		first  bool
		a1, a2 string
	}
	var steps []step
	err := f.looker().Loop(func(s Seen, first bool) (bool, error) {
		at := f.at()
		steps = append(steps, step{at, first, named(t, s.Agents, "a1").State, named(t, s.Agents, "a2").State})
		return at >= 3*time.Second, nil
	})
	f.mu.Lock()
	asked, slept := slices.Clone(f.asked), slices.Clone(f.slept)
	f.mu.Unlock()
	if err != nil || len(missed) != 0 {
		t.Fatalf("%v; sbx not asked or not answered by the look at %v within 5 s, asked at %v", err, missed, asked)
	}
	if len(steps) != 13 || slices.ContainsFunc(slept, func(d time.Duration) bool { return d != LoopEvery }) {
		t.Fatalf("%d looks, slept %v, want 13 looks %v apart", len(steps), slept, LoopEvery)
	}
	if len(asked) < 3 || asked[0] != 0 || asked[1] != time.Second || asked[2] != 2*time.Second {
		t.Fatalf("asked sbx at %v, want at 0s, 1s and 2s", asked)
	}
	ended := -1
	for i, s := range steps {
		if s.at != time.Duration(i)*LoopEvery || s.first != (i == 0) || s.a1 != state.Ended {
			t.Fatalf("look %d: %+v, want at %v, a1 ended from the first look", i, s, time.Duration(i)*LoopEvery)
		}
		if ended < 0 && s.a2 == state.Ended {
			ended = i
		}
	}
	if ended < 0 || steps[ended].at <= time.Second || steps[ended].at >= 2*time.Second {
		t.Fatalf("a2 ended at look %d, want after 1s and before 2s: %+v", ended, steps)
	}

	// A step that fails ends the loop at that look, though it is not
	// done; a look that fails ends it before any step. Each step below is
	// done at its third look, so that a loop that went on ends all the same.
	failing := errors.New("the step fails")
	ran := 0
	if err := f.looker().Loop(func(Seen, bool) (bool, error) { ran++; return ran == 3, failing }); err != failing || ran != 1 {
		t.Fatalf("a step failing: %v after %d steps, want it after 1", err, ran)
	}
	f.mu.Lock()
	f.windowsErr = errors.New("tmux gone")
	f.mu.Unlock()
	var tmuxErr *TmuxError
	ran = 0
	if err := f.looker().Loop(func(Seen, bool) (bool, error) { ran++; return ran == 3, nil }); !errors.As(err, &tmuxErr) || ran != 0 {
		t.Fatalf("a look failing: %v after %d steps, want it before any", err, ran)
	}
}
