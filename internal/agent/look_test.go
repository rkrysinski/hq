package agent

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/journal"
	"github.com/rkrysinski/hq/internal/journal/journaltest"
	"github.com/rkrysinski/hq/internal/state"
	"github.com/rkrysinski/hq/internal/tmux"
	"github.com/rkrysinski/hq/internal/version"
)

// memStore is a Store over the journals' fake (journaltest.Memory), with
// the screens it shows, by pane, and the reports each agent's hook made
// last (report).
type memStore struct {
	*journaltest.Memory
	screens    map[string]string
	captureErr error // the capture fails
	captures   []string
	reports    map[string]state.Report
}

func newStore(screens map[string]string) *memStore {
	return &memStore{Memory: journaltest.NewMemory(), screens: screens, reports: map[string]state.Report{}}
}

func (s *memStore) Screens(panes []string) (map[string]string, error) {
	s.captures = append(s.captures, strings.Join(panes, " "))
	if s.captureErr != nil {
		return nil, s.captureErr
	}
	out := map[string]string{}
	for _, p := range panes {
		if sc, ok := s.screens[p]; ok {
			out[p] = sc
		}
	}
	return out, nil
}

// report has the hook of agent id of /w/app report r, as Claude's payloads
// (journaltest.Memory.Report), unless it reported r last.
func (s *memStore) report(id string, r state.Report) {
	if old, ok := s.reports[id]; ok && reflect.DeepEqual(old, r) {
		return
	}
	s.reports[id] = r
	s.Memory.Report("/w/app", id, r, state.Detail{})
}

// look has the agents' hooks report rs, then looks at the agents of ws at
// now, with sbx's answer sbxs.
func (s *memStore) look(ws []tmux.Window, rs map[string]state.Report, sbxs Sandboxes, now time.Time) []Agent {
	for id, r := range rs {
		s.report(id, r)
	}
	return Look(ws, sbxs, now, s, nil)
}

// journalOf is agent id's journal.
func (s *memStore) journalOf(t *testing.T, id string) []state.Event {
	t.Helper()
	j, err := s.Read(id)
	if err != nil {
		t.Fatal(err)
	}
	return j.Events
}

// lookRule draws the rules around Claude's prompt box.
var lookRule = strings.Repeat("─", 20)

// declinedScreen is a turn the user ended at a question: Claude's line
// above the empty prompt box.
const declinedLine = "User declined to answer questions"

func declinedScreen() string {
	return "● " + declinedLine + "\n" + lookRule + "\n❯ \n" + lookRule + "\n  footer\n"
}

// lookWindow is the home window of agent an of /w/app (an id shaped as hq
// issues them, so its journal takes it), started long before the look.
func lookWindow(n string) tmux.Window {
	return tmux.Window{ID: "@" + n, Pane: "%" + n, Name: "a" + n, Options: map[string]string{GenerationOption: gen, "id": "a" + n, "name": "a" + n, "repo": "/w/app", "started": "100"}}
}

// capture is one of the screens in state's testdata, real Claude Code
// screens as tmux captures them.
func capture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("../state/testdata/screen-" + name + ".txt")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// replayStore is a Store holding one agent's journal as a fixture journal
// gives it: its events, numbered as there, and the reply of its latest
// turn end, with the screen the step shows.
type replayStore struct {
	events []state.Event
	reply  state.Reply
	screen string
}

func (s *replayStore) Ingest(string, string) error { return nil }

func (s *replayStore) Append(_ string, es ...state.Event) ([]state.Event, error) {
	out := journal.StandingOf(s.events).SetAside(es)
	for i := range out {
		out[i].Number = int64(len(s.events)) + int64(i) + 1
	}
	s.events = append(s.events, out...)
	return out, nil
}

func (s *replayStore) Read(id string) (journal.Journal, error) { return s.ReadFrom(id, 0) }

func (s *replayStore) ReadFrom(_ string, from int64) (journal.Journal, error) {
	if from > int64(len(s.events)) {
		from = 0
	}
	j := journal.Journal{Events: slices.Clone(s.events[from:]), From: from, Next: int64(len(s.events))}
	if slices.ContainsFunc(j.Events, func(e state.Event) bool { return e.Number == s.reply.Number }) {
		j.Reply = s.reply
	}
	return j, nil
}

func (s *replayStore) Remove(string, string) error { return nil }

func (s *replayStore) IDs() ([]string, error) { return nil, nil }

func (s *replayStore) Through() (int64, error) { return int64(len(s.events)), nil }

func (s *replayStore) Screens(panes []string) (map[string]string, error) {
	out := map[string]string{}
	if s.screen != "" {
		for _, p := range panes {
			out[p] = s.screen
		}
	}
	return out, nil
}

// styles are the escapes of a capture taken with its styles, which the
// look reads without them.
var styles = regexp.MustCompile("\x1b\\[[0-9;:]*m")

// fixture is a fixture journal of state's testdata (state.JournalFixture),
// as far as the look's test reads it: what hq did (HQ), what the step left
// in the journal (Events, Reply), what the pane, sbx and the screen showed,
// and what the look showed after it (Look).
type fixture struct {
	Steps []struct {
		HQ      string        `json:"hq"`
		Hook    string        `json:"hook"`
		Payload string        `json:"payload"`
		Screen  string        `json:"screen"`
		Pane    string        `json:"pane"`
		Sbx     string        `json:"sbx"`
		Asked   int           `json:"asked"`
		Events  []state.Event `json:"events"`
		Reply   *state.Reply  `json:"reply"`
		Look    *shown        `json:"look"`
	} `json:"steps"`
}

// shown is what the look shows of an agent, as a fixture journal holds it
// (state.OldLook).
type shown struct {
	State       string       `json:"state"`
	Since       time.Time    `json:"since"`
	Last        string       `json:"last,omitempty"`
	OwnTasks    []state.Task `json:"own_tasks,omitempty"`
	Dialog      string       `json:"dialog,omitempty"`
	DialogSince time.Time    `json:"dialog_since,omitzero"`
}

// The look appends, at every step of every fixture journal, the events the
// fixture's look appended for the same journal, screen, pane and sbx
// answer, and shows the agent as the fixture says the look did, folding on
// from the look before (Folds).
func TestTheLookAppendsAndShowsWhatEveryFixtureJournalSays(t *testing.T) {
	paths, err := filepath.Glob("../state/testdata/journals/*.json")
	if err != nil || len(paths) == 0 {
		t.Fatalf("no fixture journals: %v", err)
	}
	first := time.Date(2026, 1, 1, 0, 0, 1, 0, time.UTC)
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var fx fixture
		if err := json.Unmarshal(data, &fx); err != nil {
			t.Fatal(err)
		}
		name := filepath.Base(path)
		w := tmux.Window{ID: "@1", Pane: "%1", Name: "agent-17", Options: map[string]string{"id": "0a1b2c3d4e5f", "name": "agent-17", "repo": "/w/app", "sandbox": "claude-app", GenerationOption: gen}}
		s := &replayStore{}
		folds := NewFolds()
		var sbxs Sandboxes
		for i, step := range fx.Steps {
			when := first.Add(time.Duration(i) * time.Second)
			switch step.HQ {
			case "launch", "restore":
				w.Options["started"], w.Options["new"] = Stamp(when), "1"
				if step.HQ == "restore" {
					w.Options["new"] = ""
				}
				delete(w.Options, "ending")
				w.PaneDead, w.DeadAt = false, time.Time{}
			case "relaunch":
				w.Options["ending"], w.Options["started"], w.Options["new"] = "", Stamp(when), ""
				w.PaneDead, w.DeadAt = false, time.Time{}
			case "take down":
				w.Options["ending"] = "1"
			}
			var looked []state.Event
			for _, e := range step.Events {
				if e.Source == state.SourceHook || e.Source == state.SourceHQ {
					if e.Number != int64(len(s.events))+1 {
						t.Fatalf("%s, step %d: event %d out of turn", name, i, e.Number)
					}
					s.events = append(s.events, e)
				} else {
					looked = append(looked, e)
				}
			}
			if step.Reply != nil {
				s.reply = *step.Reply
			}
			switch step.Pane {
			case "dead":
				w.PaneDead, w.DeadAt = true, when
			case "dead-undated":
				w.PaneDead, w.DeadAt = true, time.Time{}
			}
			switch step.Sbx {
			case "stopped":
				sbxs = Sandboxes{Running: map[string]bool{}, Asked: when.Add(time.Duration(step.Asked) * time.Second)}
			case "running":
				sbxs = Sandboxes{Running: map[string]bool{"claude-app": true}, Asked: when.Add(time.Duration(step.Asked) * time.Second)}
			}
			if step.Screen != "" {
				s.screen = styles.ReplaceAllString(capture(t, step.Screen), "")
			}
			before := len(s.events)
			a := Look([]tmux.Window{w}, sbxs, when, s, folds)[0]
			got, want := s.events[before:], looked
			for j := range got {
				got[j].Time = got[j].Time.UTC()
			}
			if g, w := mustJSON(t, got), mustJSON(t, want); len(got)+len(want) > 0 && g != w {
				t.Errorf("%s, step %d (%s%s %s %s): appended\n%s\nwant\n%s", name, i, step.HQ, step.Hook, step.Payload, step.Screen, g, w)
			}
			l := &shown{State: a.State, Since: a.Since.UTC(), Last: a.Last, OwnTasks: a.OwnTasks}
			if d := a.Dialog(); d.Open() {
				l.Dialog = map[Whose]string{HooksDialog: state.DialogHooks, SubagentDialog: state.DialogSubagent, OwnDialog: state.DialogOwn}[d.Whose]
				l.DialogSince = d.Since.UTC()
			}
			if g, w := mustJSON(t, l), mustJSON(t, step.Look); g != w {
				t.Errorf("%s, step %d: shows\n%s\nwant\n%s", name, i, g, w)
			}
			s.events = append(s.events[:before], looked...)
		}
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestTheLookCapturesEveryScreenThatMayTellInOneCall(t *testing.T) {
	now := time.Unix(1000, 0)
	ws := []tmux.Window{lookWindow("1"), lookWindow("2"), lookWindow("3"), lookWindow("4")}
	ws[3].PaneDead, ws[3].DeadAt = true, now.Add(-time.Minute)
	rs := map[string]state.Report{
		"a1": {State: state.Working, Since: now.Add(-time.Minute)},            // the user may have ended its turn
		"a2": {State: state.Done, Since: now.Add(-10 * time.Minute)},          // Claude may show a dialog of its own
		"a3": {State: state.Working, Since: now.Add(-100 * time.Millisecond)}, // too fresh for the screen to overrule
		"a4": {State: state.Done, Since: now.Add(-10 * time.Minute)},          // ended
	}
	s := newStore(map[string]string{"%1": declinedScreen(), "%3": declinedScreen()})
	as := s.look(ws, rs, Sandboxes{}, now)
	if got := strings.Join(s.captures, "; "); got != "%1 %2" {
		t.Fatalf("captures: %s", got)
	}
	if as[0].State != state.Done || as[0].Last != declinedLine || !as[0].Since.Equal(now) {
		t.Fatalf("no screen to tell: %+v", as[0])
	}
	if as[2].State != state.Working || as[3].State != state.Ended {
		t.Fatalf("the others: %+v %+v", as[2], as[3])
	}

	// No screen may tell: no capture at all.
	s = newStore(nil)
	s.look(ws[2:], rs, Sandboxes{}, now)
	if len(s.captures) != 0 {
		t.Fatalf("captures with no screen that may tell: %v", s.captures)
	}
}

// What the journal holds of an agent's screen stands when a look cannot
// capture the screens, and that look appends nothing of them.
func TestWhatTheJournalHoldsStandsWhenTheLookCannotCaptureTheScreens(t *testing.T) {
	now := time.Unix(1000, 0)
	asked := now.Add(-time.Minute)
	ws := []tmux.Window{lookWindow("1"), lookWindow("2")}
	rs := map[string]state.Report{
		"a1": {State: state.Working, Since: asked, Last: "Red or blue?"},
		"a2": {State: state.Working, Since: asked, Last: "Thinking."},
	}
	s := newStore(map[string]string{"%1": declinedScreen()})
	s.look(ws[:1], rs, Sandboxes{}, now.Add(-30*time.Second))
	s.captureErr = errors.New("tmux: no server")
	as := s.look(ws, rs, Sandboxes{}, now)
	if as[0].State != state.Done || as[0].Last != declinedLine || !as[0].Since.Equal(now.Add(-30*time.Second)) {
		t.Fatalf("held: %+v", as[0])
	}
	if as[1].State != state.Working || as[1].Last != "Thinking." || !as[1].Since.Equal(asked) {
		t.Fatalf("the hooks' state: %+v", as[1])
	}
	if got := ofSourceKinds(s.journalOf(t, "a2"), state.SourceScreen); got != "" {
		t.Fatalf("appended %s", got)
	}
}

// ofSourceKinds are the kinds of the events of source among es.
func ofSourceKinds(es []state.Event, source string) string {
	var out []state.Event
	for _, e := range es {
		if e.Source == source {
			out = append(out, e)
		}
	}
	return journaltest.Kinds(out)
}

// The look reads no outdated agent's journal, ingests nothing of it,
// captures no screen of it and appends nothing, its pane alive or dead
// (#194).
func TestTheLookLeavesAnOutdatedAgentAlone(t *testing.T) {
	ws := []tmux.Window{
		{ID: "@1", Pane: "%1", Name: "a", Options: map[string]string{"id": "a1", "repo": "/w/app", "started": "100"}},
		{ID: "@2", Pane: "%2", Name: "b", PaneDead: true, Options: map[string]string{"id": "b2", "repo": "/w/app", "started": "100", GenerationOption: "2"}},
	}
	s := newStore(map[string]string{"%1": "❯ \n", "%2": "❯ \n"})
	s.report("a1", state.Report{State: state.Working, Since: time.Unix(200, 0)})
	as := Look(ws, Sandboxes{Running: map[string]bool{}, Asked: time.Unix(300, 0)}, time.Unix(1000, 0), s, nil)
	for _, a := range as {
		if a.State != state.Outdated || a.Since != a.Started {
			t.Errorf("%s: %s since %v", a.Name, a.State, a.Since)
		}
	}
	if ids, _ := s.IDs(); len(s.captures) != 0 || len(ids) != 0 || len(s.Left("/w/app", "a1")) == 0 {
		t.Fatalf("captures %v, journals %v, left %v", s.captures, ids, s.Left("/w/app", "a1"))
	}
}

// failingStore is a store whose journals can neither be read nor written.
type failingStore struct{ *memStore }

var errJournal = errors.New("no state directory")

func (failingStore) ReadFrom(string, int64) (journal.Journal, error) {
	return journal.Journal{}, errJournal
}

func (failingStore) Append(string, ...state.Event) ([]state.Event, error) { return nil, errJournal }

// An agent whose journal cannot be read is what its window says: starting,
// or ended when its pane is dead; no screen of it is captured.
func TestAnAgentWhoseJournalCannotBeReadIsWhatItsWindowSays(t *testing.T) {
	ws := []tmux.Window{lookWindow("1"), lookWindow("2")}
	ws[1].PaneDead = true
	s := failingStore{newStore(map[string]string{"%1": declinedScreen()})}
	as := Look(ws, Sandboxes{}, time.Unix(1000, 0), s, nil)
	if as[0].State != state.Starting || as[1].State != state.Ended || as[1].Last != EndedLast || len(s.captures) != 0 {
		t.Fatalf("%+v %+v, captures %v", as[0], as[1], s.captures)
	}
}

// countingStore counts the events its journals give.
type countingStore struct {
	*memStore
	read int
}

func (s *countingStore) ReadFrom(id string, from int64) (journal.Journal, error) {
	j, err := s.memStore.ReadFrom(id, from)
	s.read += len(j.Events)
	return j, err
}

// A long-running program's looks fold only what is new since the look
// before (Folds) and show what folding the journal from its start shows;
// a journal removed meanwhile is folded anew.
func TestALongRunningLookFoldsOnlyWhatIsNew(t *testing.T) {
	now := time.Unix(1000, 0)
	s := &countingStore{memStore: newStore(nil)}
	ws := []tmux.Window{lookWindow("1")}
	folds := NewFolds()
	s.report("a1", state.Report{State: state.Working, Since: now.Add(-time.Minute), Last: "Earlier."})
	Look(ws, Sandboxes{}, now, s, folds)
	if s.read != 2 {
		t.Fatalf("the first look read %d events", s.read)
	}
	s.read = 0
	Look(ws, Sandboxes{}, now.Add(time.Second), s, folds)
	if s.read != 0 {
		t.Fatalf("a look with nothing new read %d events", s.read)
	}
	s.report("a1", state.Report{State: state.Done, Since: now.Add(2 * time.Second), Last: "Done."})
	a := Look(ws, Sandboxes{}, now.Add(3*time.Second), s, folds)[0]
	if s.read != 1 || a.State != state.Done || a.Last != "Done." || !a.Since.Equal(now.Add(2*time.Second)) {
		t.Fatalf("read %d events: %+v", s.read, a)
	}
	if fresh := Look(ws, Sandboxes{}, now.Add(3*time.Second), s, nil)[0]; !reflect.DeepEqual(fresh.reduced, a.reduced) {
		t.Fatalf("folded on %+v\nfrom the start %+v", a.reduced, fresh.reduced)
	}
	if err := s.Remove("/w/app", "a1"); err != nil {
		t.Fatal(err)
	}
	s.reports = map[string]state.Report{}
	s.report("a1", state.Report{State: state.Working, Since: now.Add(4 * time.Second)})
	if a := Look(ws, Sandboxes{}, now.Add(5*time.Second), s, folds)[0]; a.State != state.Working || a.Last != "" {
		t.Fatalf("after the removal: %+v", a)
	}
}

// FromJournals gives the agents their state from their journals, the hooks'
// event files ingested first, and captures no screen: of what tmux shows it
// appends only the death of a pane, as tmux dates it (here not at all:
// since the moment of the look), once, so the ended state rests on an event
// (story 43 of #165). A lost agent, which has no home window, gets none.
func TestFromJournalsAppendsOnlyAPanesDeathAndCapturesNoScreen(t *testing.T) {
	now := time.Unix(1000, 0)
	ws := []tmux.Window{lookWindow("1")}
	ws[0].PaneDead = true
	s := newStore(map[string]string{"%1": declinedScreen()})
	s.report("a1", state.Report{State: state.Working, Since: now.Add(-time.Minute), Branch: "feat/1", Cwd: "/w/app/wt"})
	for range 2 {
		a := FromJournals(ws, s, now, nil)[0]
		j := s.journalOf(t, "a1")
		if a.State != state.Ended || !a.Since.Equal(now) || a.Branch != "feat/1" || a.Worktree != "/w/app/wt" || len(s.captures) != 0 ||
			journaltest.Kinds(j) != "UserPromptSubmit PaneDied" || a.Entered() != j[1].Time {
			t.Fatalf("%+v, captures %v, journal %s", a, s.captures, journaltest.Kinds(j))
		}
	}
	s.report("b1", state.Report{State: state.Working, Since: now.Add(-time.Minute), Cwd: "/w/app/wt"})
	lost := []Agent{{ID: "b1", Name: "b", RepoPath: "/w/app", Generation: version.Generation, State: state.Starting}}
	ReadStates(lost, s, now, nil)
	if lost[0].State != state.Working || lost[0].Worktree != "/w/app/wt" || journaltest.Kinds(s.journalOf(t, "b1")) != "UserPromptSubmit" {
		t.Fatalf("lost %+v, journal %s", lost[0], journaltest.Kinds(s.journalOf(t, "b1")))
	}
}

// Esc on a turn with a prompt queued behind it fires no hook, and Claude
// runs the queued prompt at once (#88): the screen shows the Interrupted
// line with Claude at work below it and no prompt of the user's between.
// With the prompt queued, the look appends that Claude took it, which
// hands the state over to it, working since then, once; not while the
// prompt is still queued, nor when nothing is queued.
func TestTheLookHandsTheStateOverToTheQueuedPromptClaudeTookAfterAnEsc(t *testing.T) {
	now := time.Unix(1000, 0)
	const queued = "Run exactly this bash command in the foreground and show me its output: for i in $(seq 25); do echo tock $i; sleep 1; done"
	prompt := func(text string) []byte {
		return journaltest.Hook("main", map[string]any{"hook_event_name": "UserPromptSubmit", "prompt_id": "t1", "prompt": text})
	}
	look := func(screen string, queue bool) (Agent, *memStore) {
		t.Helper()
		s := newStore(map[string]string{"%1": capture(t, screen)})
		s.Leave("/w/app", "a1", "000000001.prompt.1", prompt("Count the tocks."), now.Add(-2*time.Minute))
		if queue {
			s.Leave("/w/app", "a1", "000000002.prompt.1", prompt(queued), now.Add(-time.Minute))
		}
		return Look([]tmux.Window{lookWindow("1")}, Sandboxes{}, now, s, nil)[0], s
	}
	a, s := look("fs-queued-taken-thinking", true)
	if a.State != state.Working || !a.Since.Equal(now) || ofSourceKinds(s.journalOf(t, "a1"), state.SourceScreen) != state.KindTookQueued {
		t.Fatalf("taken: %+v, journal %s", a, journaltest.Kinds(s.journalOf(t, "a1")))
	}
	// The next look finds nothing to hand over.
	if a := Look([]tmux.Window{lookWindow("1")}, Sandboxes{}, now.Add(time.Second), s, nil)[0]; a.State != state.Working || !a.Since.Equal(now) || ofSourceKinds(s.journalOf(t, "a1"), state.SourceScreen) != state.KindTookQueued {
		t.Fatalf("the look after: %+v, journal %s", a, journaltest.Kinds(s.journalOf(t, "a1")))
	}
	if a, s := look("fs-queued-at-work", true); a.State != state.Working || !a.Since.Equal(now.Add(-time.Minute)) || ofSourceKinds(s.journalOf(t, "a1"), state.SourceScreen) != "" {
		t.Errorf("still queued: %+v", a)
	}
	if a, s := look("fs-queued-taken-thinking", false); a.State != state.Working || ofSourceKinds(s.journalOf(t, "a1"), state.SourceScreen) == state.KindTookQueued {
		t.Errorf("nothing queued: %+v", a)
	}
}

// The detail of an agent is its latest turn end's reply in full, with
// when it was given, which a prompt does not take away, and what the
// dialog its hooks reported asks while it is open.
func TestTheDetailIsTheLatestReplyAndWhatTheOpenDialogAsks(t *testing.T) {
	now := time.Unix(1000, 0)
	ws := []tmux.Window{lookWindow("1")}
	s := newStore(nil)
	ask := &state.Ask{Tool: "Bash", Description: "Clean", Command: "rm -rf build"}
	s.Memory.Report("/w/app", "a1", state.Report{State: state.NeedsInput, Since: now.Add(-time.Minute), LastStop: "On it."}, state.Detail{Reply: "On it.\n\nNext.", Ask: ask})
	a := Look(ws, Sandboxes{}, now, s, nil)[0]
	if d := a.Detail(); d.Reply != "On it.\n\nNext." || !d.ReplyAt.Equal(now.Add(-time.Minute-time.Millisecond)) || !reflect.DeepEqual(d.Ask, ask) || a.Dialog().Asks(d.Ask) != d.Ask {
		t.Fatalf("at the dialog: %+v", d)
	}
	s.report("a1", state.Report{State: state.Working, Since: now})
	a = Look(ws, Sandboxes{}, now, s, nil)[0]
	if d := a.Detail(); d.Reply != "On it.\n\nNext." || d.Ask != nil {
		t.Fatalf("working on: %+v", d)
	}
	if d := Look([]tmux.Window{lookWindow("2")}, Sandboxes{}, now, s, nil)[0].Detail(); d != (state.Detail{}) {
		t.Fatalf("no turn end yet: %+v", d)
	}
}

// An agent's age counts to the moment of the look: with the clock it is
// given moved back and forth between looks, no agent changes state and
// every age moves by the time the clock moved, as the moment each state
// counts from stays put.
func TestTheClockMovedBetweenLooksChangesNothingButTheAges(t *testing.T) {
	now := time.Unix(10_000, 0)
	ws := []tmux.Window{lookWindow("1"), lookWindow("2"), lookWindow("3")}
	ws[2].PaneDead, ws[2].DeadAt = true, now.Add(-30*time.Minute)
	s := newStore(map[string]string{"%1": declinedScreen()})
	rs := map[string]state.Report{
		"a1": {State: state.Working, Since: now.Add(-time.Hour)},
		"a2": {State: state.Done, Since: now.Add(-time.Hour), Last: "Done."},
		"a3": {State: state.Working, Since: now.Add(-time.Hour)},
	}
	before := s.look(ws, rs, Sandboxes{}, now)
	for _, moved := range []time.Duration{-10 * time.Minute, 10 * time.Minute, 20 * time.Minute} {
		after := s.look(ws, rs, Sandboxes{}, now.Add(moved))
		for i := range after {
			if after[i].State != before[i].State || !after[i].Since.Equal(before[i].Since) || after[i].reduced.Age-before[i].reduced.Age != moved {
				t.Errorf("moved %v: %s %s since %v, age %v; was %s since %v, age %v", moved, after[i].Name, after[i].State, after[i].Since, after[i].reduced.Age, before[i].State, before[i].Since, before[i].reduced.Age)
			}
		}
	}
}

// Looks of several hq processes at once (the list, hq ls, hq wait) show
// the same state and the same age for every agent: the first to append
// what they saw wins, and every look folds the same journal.
func TestLooksAtOnceShowTheSameStateAndAge(t *testing.T) {
	now := time.Unix(1000, 0)
	ws := []tmux.Window{lookWindow("1"), lookWindow("2")}
	s := newStore(map[string]string{"%1": declinedScreen(), "%2": capture(t, "fs-esc-idle")})
	rs := map[string]state.Report{
		"a1": {State: state.Working, Since: now.Add(-time.Minute)},
		"a2": {State: state.Working, Since: now.Add(-time.Minute), Prompt: "Count."},
	}
	list, ls := NewFolds(), NewFolds()
	views := [][]Agent{
		s.look(ws, rs, Sandboxes{}, now),
		Look(ws, Sandboxes{}, now.Add(10*time.Millisecond), s, list),
		Look(ws, Sandboxes{}, now.Add(20*time.Millisecond), s, ls),
	}
	for _, v := range views[1:] {
		for i := range v {
			if v[i].State != views[0][i].State || !v[i].Since.Equal(views[0][i].Since) || v[i].Entered() != views[0][i].Entered() {
				t.Errorf("%s: %s since %v, %s since %v", v[i].Name, v[i].State, v[i].Since, views[0][i].State, views[0][i].Since)
			}
		}
	}
}

// The look's Folds keep the list's tick with journals of 50,000 events for
// 15 agents: once folded, a look reads and folds only the events new since
// the last, and is done well within the tick (design §5.1).
func TestALookAtFifteenLongJournalsFoldsOnlyWhatIsNew(t *testing.T) {
	if testing.Short() {
		t.Skip("scale")
	}
	now := time.Unix(100_000, 0)
	s := &countingStore{memStore: newStore(nil)}
	var ws []tmux.Window
	for i := range 15 {
		w := lookWindow(strconv.Itoa(i + 1))
		w.Options["id"] = "a" + strconv.FormatInt(int64(i+1), 16)
		ws = append(ws, w)
		events := make([]state.Event, 0, 50_000)
		for n := range 50_000 {
			e := state.Event{Source: state.SourceHook, Kind: "UserPromptSubmit", Time: now.Add(time.Duration(n-50_000) * time.Second), Facts: state.Facts{Hook: "prompt", Turn: "t" + strconv.Itoa(n)}}
			if n%2 == 1 {
				e.Kind, e.Facts.Hook = state.KindTurnEnd, "stop"
			}
			events = append(events, e)
		}
		s.Put(w.Options["id"], events...)
	}
	folds := NewFolds()
	for _, a := range Look(ws, Sandboxes{}, now, s, folds) {
		if a.State != state.Done {
			t.Fatalf("%s: %s", a.Name, a.State)
		}
	}
	s.read = 0
	for _, w := range ws {
		s.Put(w.Options["id"], state.Event{Source: state.SourceHook, Kind: "UserPromptSubmit", Time: now, Facts: state.Facts{Hook: "prompt", Turn: "next"}})
	}
	start := time.Now()
	as := Look(ws, Sandboxes{}, now.Add(time.Second), s, folds)
	if took := time.Since(start); took > 250*time.Millisecond/5 {
		t.Errorf("a look took %v", took)
	}
	if s.read != len(ws) {
		t.Errorf("a look read %d events", s.read)
	}
	for _, a := range as {
		if a.State != state.Working {
			t.Fatalf("%s: %s", a.Name, a.State)
		}
	}
}

// A look that listed the windows before a relaunch respawned the pane, and
// reads the journal after the relaunch's event, sees the pane of the
// previous session dead: it appends no death tmux dates before the
// relaunch, so the relaunched agent, its pane alive and reporting, is not
// ended (#198).
func TestAStaleLookAtARelaunchedAgentEndsNobody(t *testing.T) {
	t1 := time.Unix(1000, 0)
	alive := []tmux.Window{lookWindow("1")}
	dead := []tmux.Window{lookWindow("1")}
	dead[0].PaneDead, dead[0].DeadAt = true, t1
	s := newStore(map[string]string{})
	s.report("a1", state.Report{State: state.Working, Since: t1.Add(-time.Minute), Prompt: "p"})
	if a := Look(dead, Sandboxes{}, t1.Add(time.Second), s, nil)[0]; a.State != state.Ended {
		t.Fatalf("not ended first: %+v", a)
	}
	// The relaunch's event first, as relaunchAgent appends it before it
	// respawns the pane.
	if _, err := s.Append("a1", state.Event{Source: state.SourceHQ, Kind: state.KindRelaunched, Time: t1.Add(2 * time.Second), Basis: journal.Basis(s.journalOf(t, "a1"))}); err != nil {
		t.Fatal(err)
	}
	before := journaltest.Kinds(s.journalOf(t, "a1"))
	Look(dead, Sandboxes{}, t1.Add(2*time.Second), s, nil)
	if got := journaltest.Kinds(s.journalOf(t, "a1")); got != before {
		t.Fatalf("the stale look appended: %s, was %s", got, before)
	}
	s.report("a1", state.Report{State: state.Working, Since: t1.Add(3 * time.Second), Prompt: "next"})
	if a := Look(alive, Sandboxes{}, t1.Add(4*time.Second), s, nil)[0]; a.State != state.Working {
		t.Fatalf("the relaunched agent, alive and reporting, is %s; journal %s", a.State, journaltest.Kinds(s.journalOf(t, "a1")))
	}
}
