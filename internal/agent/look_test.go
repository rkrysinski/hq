package agent

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/state"
	"github.com/rkrysinski/hq/internal/tmux"
)

// fakeStore is a Store over maps: the screens it shows, by pane, and the
// options it keeps, by window, first-wins as tmux keeps them.
type fakeStore struct {
	screens    map[string]string
	options    map[string]map[string]string
	captureErr error // the capture fails
	keepErr    error // every record write fails
	calls      []string
}

func (s *fakeStore) Screens(panes []string) (map[string]string, error) {
	s.calls = append(s.calls, "screens "+strings.Join(panes, " "))
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

func (s *fakeStore) KeepFirst(window, option, prefix, value string) (string, error) {
	s.calls = append(s.calls, "keep "+window+" "+option)
	if s.keepErr != nil {
		return "", s.keepErr
	}
	if s.options == nil {
		s.options = map[string]map[string]string{}
	}
	if s.options[window] == nil {
		s.options[window] = map[string]string{}
	}
	if !strings.HasPrefix(s.options[window][option], prefix) {
		s.options[window][option] = value
	}
	return s.options[window][option], nil
}

// lookRule draws the rules around Claude's prompt box.
var lookRule = strings.Repeat("─", 20)

// declinedScreen is a turn the user ended at a question: Claude's line
// above the empty prompt box.
const declinedLine = "User declined to answer questions"

func declinedScreen() string {
	return "● " + declinedLine + "\n" + lookRule + "\n❯ \n" + lookRule + "\n  footer\n"
}

// lookWindow is an agent's home window, started long before the look.
func lookWindow(n string) tmux.Window {
	return tmux.Window{ID: "@" + n, Pane: "%" + n, Name: "a" + n, Options: map[string]string{"id": "id" + n, "name": "a" + n, "started": "100"}}
}

func reportsOf(rs map[string]state.Report) func(root, id string) (state.Report, bool) {
	return func(_, id string) (state.Report, bool) { r, ok := rs[id]; return r, ok }
}

func TestTheLookCapturesTheScreensOfAllUnsettledAgentsInOneCall(t *testing.T) {
	now := time.Unix(1000, 0)
	ws := []tmux.Window{lookWindow("1"), lookWindow("2"), lookWindow("3"), lookWindow("4")}
	ws[3].PaneDead, ws[3].DeadAt = true, now.Add(-time.Minute)
	rs := map[string]state.Report{
		"id1": {State: state.Working, Since: now.Add(-time.Minute)},            // the user may have ended its turn
		"id2": {State: state.Done, Since: now.Add(-10 * time.Minute)},          // Claude may show a dialog of its own
		"id3": {State: state.Working, Since: now.Add(-100 * time.Millisecond)}, // too fresh for the screen to overrule
		"id4": {State: state.Done, Since: now.Add(-10 * time.Minute)},          // ended
	}
	s := &fakeStore{screens: map[string]string{"%1": declinedScreen()}}
	as := Look(ws, reportsOf(rs), nil, Sandboxes{}, now, s)
	if got := strings.Join(s.calls, "; "); !strings.HasPrefix(got, "screens %1 %2") || strings.Count(got, "screens") != 1 {
		t.Fatalf("calls: %s", got)
	}
	if as[0].State != state.Done || as[0].Last != declinedLine || !as[0].Since.Equal(now) {
		t.Fatalf("settled: %+v", as[0])
	}
	if as[2].State != state.Working || as[3].State != state.Ended {
		t.Fatalf("the others: %+v %+v", as[2], as[3])
	}

	// No agent is unsettled: no capture at all.
	s = &fakeStore{}
	Look(ws[2:], reportsOf(rs), nil, Sandboxes{}, now, s)
	if len(s.calls) != 0 {
		t.Fatalf("calls with nothing unsettled: %v", s.calls)
	}
}

func TestARecalledTurnEndStandsWhenTheLookCannotCaptureTheScreens(t *testing.T) {
	now := time.Unix(1000, 0)
	asked, seen := now.Add(-time.Minute), now.Add(-30*time.Second)
	ws := []tmux.Window{lookWindow("1"), lookWindow("2")}
	// An earlier look saw the user end a's turn.
	ws[0].Options["turnend"] = nanos(asked) + " " + nanos(seen) + " " + declinedLine
	rs := map[string]state.Report{
		"id1": {State: state.Working, Since: asked, Last: "Red or blue?"},
		"id2": {State: state.Working, Since: asked, Last: "Thinking."},
	}
	s := &fakeStore{captureErr: errors.New("tmux: no server")}
	as := Look(ws, reportsOf(rs), nil, Sandboxes{}, now, s)
	if as[0].State != state.Done || as[0].Last != declinedLine || !as[0].Since.Equal(seen) {
		t.Fatalf("recalled: %+v", as[0])
	}
	// The hooks' state stands for the other, and nothing is recorded.
	if as[1].State != state.Working || as[1].Last != "Thinking." || !as[1].Since.Equal(asked) {
		t.Fatalf("the hooks' state: %+v", as[1])
	}
	if got := strings.Join(s.calls, "; "); got != "screens %1 %2" {
		t.Fatalf("calls: %s", got)
	}
}

func TestTheLookKeepsWhatItSawFirstWinsAndShowsWhatTheWindowHolds(t *testing.T) {
	now := time.Unix(1000, 0)
	asked, earlier := now.Add(-time.Minute), now.Add(-2*time.Second)
	rs := map[string]state.Report{"id1": {State: state.NeedsInput, Since: asked, Last: "Red or blue?"}}
	screens := map[string]string{"%1": declinedScreen()}
	key := nanos(asked) + " "

	// Nothing stored yet: the look's own record is kept, and shows.
	s := &fakeStore{screens: screens}
	as := Look([]tmux.Window{lookWindow("1")}, reportsOf(rs), nil, Sandboxes{}, now, s)
	if got, want := s.options["@1"]["turnend"], key+nanos(now)+" "+declinedLine; got != want {
		t.Fatalf("stored %q, want %q", got, want)
	}
	if as[0].State != state.Done || !as[0].Since.Equal(now) || as[0].Last != declinedLine {
		t.Fatalf("its own record: %+v", as[0])
	}

	// Another process stored its record of the same turn end first, after
	// the windows were read: that one stays, and its moment shows.
	other := key + nanos(earlier) + " " + declinedLine
	s = &fakeStore{screens: screens, options: map[string]map[string]string{"@1": {"turnend": other}}}
	as = Look([]tmux.Window{lookWindow("1")}, reportsOf(rs), nil, Sandboxes{}, now, s)
	if s.options["@1"]["turnend"] != other {
		t.Fatalf("overwrote the first record: %q", s.options["@1"]["turnend"])
	}
	if as[0].State != state.Done || !as[0].Since.Equal(earlier) || as[0].Last != declinedLine {
		t.Fatalf("the record stored first: %+v", as[0])
	}

	// A record of an earlier report does not count: this one is kept.
	s = &fakeStore{screens: screens, options: map[string]map[string]string{"@1": {"turnend": nanos(asked.Add(-time.Hour)) + " " + nanos(earlier) + " " + declinedLine}}}
	as = Look([]tmux.Window{lookWindow("1")}, reportsOf(rs), nil, Sandboxes{}, now, s)
	if !strings.HasPrefix(s.options["@1"]["turnend"], key) || !as[0].Since.Equal(now) {
		t.Fatalf("an earlier report's record: %q %+v", s.options["@1"]["turnend"], as[0])
	}

	// The record cannot be written: it is not applied, and the agent shows
	// what this look saw.
	s = &fakeStore{screens: screens, keepErr: errors.New("tmux: no server")}
	as = Look([]tmux.Window{lookWindow("1")}, reportsOf(rs), nil, Sandboxes{}, now, s)
	if as[0].State != state.Done || !as[0].Since.Equal(now) || as[0].Last != declinedLine {
		t.Fatalf("unwritten: %+v", as[0])
	}
}

func TestTheLookSeesEndsAndWakeUpsAfterSettlingTheScreens(t *testing.T) {
	now := time.Unix(1000, 0)
	prompted, ended, woken := now.Add(-5*time.Minute), now.Add(-4*time.Minute), now.Add(-time.Minute)
	ws := []tmux.Window{lookWindow("1"), lookWindow("2"), lookWindow("3")}
	ws[0].Options["sandbox"], ws[1].Options["sandbox"], ws[2].Options["sandbox"] = "claude-app", "claude-lib", "claude-lib"
	// The user ended b's turn while a subagent ran; Claude woke it since.
	ws[1].Options["turnend"] = nanos(prompted) + " " + nanos(ended) + " " + declinedLine
	rs := map[string]state.Report{
		"id1": {State: state.Done, Since: prompted},
		"id2": {State: state.Working, Since: prompted, Latest: woken, Woken: true},
		"id3": {State: state.Working, Since: now.Add(-time.Minute)},
	}
	// a's sandbox stopped since its last report, its pane still running:
	// nothing else dates its end.
	sbxs := Sandboxes{Running: map[string]bool{"claude-app": false, "claude-lib": true}, Asked: now.Add(-time.Second)}
	s := &fakeStore{screens: map[string]string{"%3": declinedScreen()}}
	as := Look(ws, reportsOf(rs), nil, sbxs, now, s)
	if got, want := strings.Join(s.calls, "; "), "screens %2 %3; keep @3 turnend; keep @1 endseen; keep @2 wokeseen"; got != want {
		t.Fatalf("calls %s, want %s", got, want)
	}
	if as[0].State != state.Ended || !as[0].Since.Equal(now) || !strings.HasSuffix(s.options["@1"]["endseen"], " "+nanos(now)) {
		t.Fatalf("seen ended: %+v %q", as[0], s.options["@1"]["endseen"])
	}
	if as[1].State != state.Working || !as[1].Since.Equal(woken) || !strings.HasSuffix(s.options["@2"]["wokeseen"], " "+nanos(woken)) {
		t.Fatalf("seen woken: %+v %q", as[1], s.options["@2"]["wokeseen"])
	}

	// Another process saw the end first: its moment shows.
	first := strings.Replace(s.options["@1"]["endseen"], nanos(now), nanos(now.Add(-time.Second)), 1)
	s = &fakeStore{options: map[string]map[string]string{"@1": {"endseen": first}}}
	if as = Look(ws[:1], reportsOf(rs), nil, sbxs, now, s); !as[0].Since.Equal(now.Add(-time.Second)) {
		t.Fatalf("the end seen first: %+v", as[0])
	}
}

// Esc on a turn with a prompt queued behind it fires no hook, and Claude
// runs the queued prompt at once (#88): the screen shows the Interrupted
// line with Claude at work below it and no prompt of the user's between.
// With the hooks' copy of the prompt pending that is Claude on the queued
// prompt, so the look hands the state over to it (state.TakeQueued), as the
// hook does at a Stop, and reads the report again: working since now, and
// no turn end recorded. Not while the prompt is still queued (its line is
// the last prompt shown), not for a woken agent (its wake-up looks the
// same), and not when nothing is queued, where the line with Claude at
// work below it is no end either. A look that cannot hand over (no taker,
// or a hand-over that fails) keeps the hooks' word.
func TestTheLookHandsOverToTheQueuedPromptClaudeTookAfterAnEsc(t *testing.T) {
	now := time.Unix(1000, 0)
	asked := now.Add(-time.Minute)
	const queued = "Run exactly this bash command in the foreground and show me its output: for i in $(seq 25); do echo tock $i; sleep 1; done"
	var rs map[string]state.Report
	var taken []string
	take := func(root, id string) error {
		taken = append(taken, root+"/"+id)
		rs["id1"] = state.Report{State: state.Working, Since: now, Latest: now, Last: "earlier", Prompt: queued}
		return nil
	}
	w := lookWindow("1")
	w.Options["repo"] = "/w/app"
	look := func(name string, r state.Report, take Taker) (Agent, *fakeStore) {
		t.Helper()
		rs = map[string]state.Report{"id1": r}
		taken = nil
		s := &fakeStore{screens: map[string]string{"%1": capture(t, name)}}
		return Look([]tmux.Window{w}, reportsOf(rs), take, Sandboxes{}, now, s)[0], s
	}
	pending := state.Report{State: state.Working, Since: asked, Latest: asked, Last: "earlier", Prompt: queued, Queued: true}

	for _, name := range []string{"fs-queued-taken-thinking", "fs-queued-taken-streaming", "fs-queued-taken-shown-thinking", "fs-queued-taken-shown-tool"} {
		a, s := look(name, pending, take)
		if a.State != state.Working || !a.Since.Equal(now) || a.Last != "earlier" || len(taken) != 1 || taken[0] != "/w/app/id1" || len(s.options) != 0 {
			t.Errorf("%s: %+v, taken %v, records %v", name, a, taken, s.options)
		}
	}
	keeps := func(when, name string, r state.Report, take Taker) {
		t.Helper()
		a, s := look(name, r, take)
		if a.State != state.Working || !a.Since.Equal(asked) || len(taken) != 0 || len(s.options) != 0 {
			t.Errorf("%s: %+v, taken %v, records %v", when, a, taken, s.options)
		}
	}
	keeps("still queued", "fs-queued-at-work", pending, take)
	woken := pending
	woken.Woken = true
	keeps("woken", "fs-queued-taken-thinking", woken, take)
	settled := pending
	settled.Queued = false
	keeps("nothing queued, Claude at work below the line", "fs-queued-taken-thinking", settled, take)
	keeps("no taker", "fs-queued-taken-thinking", pending, nil)
	keeps("the hand-over failed", "fs-queued-taken-thinking", pending, func(string, string) error { return errors.New("gone") })
	// Claude idle below the line is the turn's end, queued copy or not: Esc
	// twice, or the queued prompt pulled back into the box.
	for _, r := range []state.Report{pending, settled} {
		for _, name := range []string{"fs-esc-idle", "fs-esc-twice"} {
			a, s := look(name, r, take)
			if a.State != state.Done || a.Last != Rewound || !a.Since.Equal(now) || len(taken) != 0 || s.options["@1"]["turnend"] == "" {
				t.Errorf("%s queued=%v: %+v, taken %v, records %v", name, r.Queued, a, taken, s.options)
			}
		}
	}
}
