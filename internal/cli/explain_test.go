package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/journal/journaltest"
	"github.com/rkrysinski/hq/internal/sbx"
	"github.com/rkrysinski/hq/internal/state"
	"github.com/rkrysinski/hq/internal/tmux"
	"github.com/rkrysinski/hq/internal/version"
)

// explainFakes has the agent 42 in app, started an hour ago in the running
// sandbox claude-x, with events put into its journal.
func explainFakes(events ...state.Event) *fakes {
	f := newLsFakes()
	f.tmux.windows = []tmux.Window{
		{ID: "@0", Name: "hq", Options: map[string]string{}},
		agentWindow("@4", "42", "/w/app", f.now.Add(-time.Hour), false),
	}
	f.journal.Put(journalID("id-42"), events...)
	return f
}

// hookEvent is an event of the hook of kind, at the hook's case at, at t.
func hookEvent(kind, at string, t time.Time, facts state.Facts) state.Event {
	facts.Hook = at
	return state.Event{Source: state.SourceHook, Kind: kind, Time: t, Facts: facts}
}

// backgroundJournal is an agent's journal from its launch to a turn end
// that left a subagent running, after a turn with a dialog: it works on
// background work, with the text of its prompts and of the dialog in its
// events.
func backgroundJournal(now time.Time) []state.Event {
	at := func(m int) time.Time { return now.Add(time.Duration(m-10) * time.Minute) }
	return []state.Event{
		{Source: state.SourceHQ, Kind: state.KindLaunched, Time: at(0), Facts: state.Facts{Start: "new"}},
		hookEvent("SessionStart", "start", at(1), state.Facts{Start: "startup", Session: "s1", Branch: "main"}),
		hookEvent("UserPromptSubmit", "prompt", at(2), state.Facts{Prompt: "list the files", Turn: "t0", Branch: "main"}),
		hookEvent("PermissionRequest", "dialog", at(3), state.Facts{Tool: "Bash", Text: "Bash: ls -la", Turn: "t0"}),
		hookEvent("PostToolUse", "answer", at(4), state.Facts{Tool: "Bash", Turn: "t0"}),
		hookEvent(state.KindTurnEnd, "stop", at(5), state.Facts{Turn: "t0"}),
		hookEvent("UserPromptSubmit", "prompt", at(6), state.Facts{Prompt: "run the tests in the background", Turn: "t1", Branch: "main"}),
		hookEvent("PostToolUse", "answer", at(7), state.Facts{Tool: "Agent", Turn: "t1", Started: "a58b4"}),
		hookEvent(state.KindTurnEnd, "stop", at(8), state.Facts{Turn: "t1", Tasks: []state.EventTask{{ID: "a58b4", Type: "subagent", Status: "running"}}}),
	}
}

// hq explain prints the events the state rests on, oldest first, each
// with its number, time, source, what it was and the state after it: for
// an agent working on background work, the prompt, the start of the task
// and the turn end that left it working; and the dialog verdict with the
// events that opened and closed the last dialog.
func TestExplainShowsTheEventsTheStateRestsOn(t *testing.T) {
	f := explainFakes()
	f.journal.Put(journalID("id-42"), backgroundJournal(f.now)...)
	code, out, errOut := f.run("explain", "42")
	if code != 0 {
		t.Fatalf("exit %d %q", code, errOut)
	}
	want := `name      42
state     working 4m, since 2026-09-27 11:56:00.000
rests on  7 8 9
dialog    at none; the last, its hooks', opened at 4, closed at 5
rests on  4 5

the state rests on, oldest first
  N  TIME                     SOURCE  WHAT                                              STATE
  7  2026-09-27 11:56:00.000  hook    prompt                                            working
  8  2026-09-27 11:57:00.000  hook    end of tool Agent, started background work a58b4  working
  9  2026-09-27 11:58:00.000  hook    turn end, running: subagent a58b4                 working

the dialog verdict rests on, oldest first
  N  TIME                     SOURCE  WHAT                 STATE
  4  2026-09-27 11:53:00.000  hook    dialog of tool Bash  needs input
  5  2026-09-27 11:54:00.000  hook    end of tool Bash     working
`
	if out != want {
		t.Fatalf("got\n%s\nwant\n%s", out, want)
	}
}

// --all prints every event since the agent started, with the state after
// each; an event set aside is marked with its reason and changes nothing.
func TestExplainAllShowsEveryEventAndWhyOneWasSetAside(t *testing.T) {
	f := explainFakes()
	at := func(s int) time.Time { return f.now.Add(time.Duration(s-60) * time.Second) }
	f.journal.Put(journalID("id-42"),
		state.Event{Source: state.SourceHQ, Kind: state.KindLaunched, Time: at(0), Facts: state.Facts{Start: "new"}},
		hookEvent("UserPromptSubmit", "prompt", at(1), state.Facts{Prompt: "say hi", Turn: "t1"}),
		hookEvent(state.KindTurnEnd, "stop", at(2), state.Facts{Turn: "t1", Question: true}),
		state.Event{Source: state.SourceScreen, Kind: state.KindAtRest, Time: at(3), Basis: 2, Aside: state.AsideOvertaken},
		state.Event{Source: state.SourceTmux, Kind: state.KindPaneDied, Time: at(4), Basis: 3, Facts: state.Facts{From: at(3)}},
		state.Event{Source: state.SourceTmux, Kind: state.KindPaneDied, Time: at(5), Basis: 3, Facts: state.Facts{From: at(3)}, Aside: state.AsideDuplicate},
	)
	code, out, errOut := f.run("explain", "42", "--all")
	if code != 0 {
		t.Fatalf("exit %d %q", code, errOut)
	}
	want := `name      42
state     ended 57s, since 2026-09-27 11:59:03.000
rests on  3 5
dialog    at none, never at one

every event, oldest first
  N  TIME                     SOURCE  WHAT                                                                                                 STATE
  1  2026-09-27 11:59:00.000  hq      launched (new)                                                                                       starting
  2  2026-09-27 11:59:01.000  hook    prompt                                                                                               working
  3  2026-09-27 11:59:02.000  hook    turn end, the reply ends with ?                                                                      question
  4  2026-09-27 11:59:03.000  screen  the screen first seen at rest - set aside: a later report overtook it                                question
  5  2026-09-27 11:59:04.000  tmux    the pane died, dated from 2026-09-27 11:59:03.000                                                    ended
  6  2026-09-27 11:59:05.000  tmux    the pane died, dated from 2026-09-27 11:59:03.000 - set aside: another hq process appended it first  ended
`
	if out != want {
		t.Fatalf("got\n%s\nwant\n%s", out, want)
	}
}

// --json gives the same events as data, with the state and the dialog
// verdict, with and without --all.
func TestExplainJSONGivesTheSameEventsAsData(t *testing.T) {
	for _, all := range []bool{false, true} {
		f := explainFakes()
		f.journal.Put(journalID("id-42"), backgroundJournal(f.now)...)
		args := []string{"explain", "42", "--json"}
		if all {
			args = append(args, "--all")
		}
		code, out, errOut := f.run(args...)
		if code != 0 {
			t.Fatalf("exit %d %q", code, errOut)
		}
		var v explainView
		if err := json.Unmarshal([]byte(out), &v); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		var ns []int64
		for _, e := range v.Events {
			ns = append(ns, e.Number)
		}
		wantNs := []int64{4, 5, 7, 8, 9}
		if all {
			wantNs = []int64{1, 2, 3, 4, 5, 6, 7, 8, 9}
		}
		if v.Name != "42" || v.State != state.Working || v.AgeSeconds != 240 || !slices.Equal(v.RestsOn, []int64{7, 8, 9}) || !slices.Equal(ns, wantNs) {
			t.Fatalf("all %v:\n%s", all, out)
		}
		if d := v.Dialog; d.Open || d.Whose != state.DialogHooks || d.Opened != 4 || d.Closed != 5 || !slices.Equal(d.RestsOn, []int64{4, 5}) {
			t.Fatalf("all %v: dialog %+v", all, d)
		}
		last := v.Events[len(v.Events)-1]
		if last.Source != state.SourceHook || last.Kind != state.KindTurnEnd || last.State != state.Working || last.What != "turn end, running: subagent a58b4" ||
			last.Facts.Hook != "stop" || len(last.Facts.Tasks) != 1 || last.Facts.Tasks[0].ID != "a58b4" || last.Facts.Turn != "t1" {
			t.Fatalf("all %v: last %+v", all, last)
		}
	}
	// No journal yet: starting, nothing it rests on, as empty lists.
	f := explainFakes()
	_, out, _ := f.run("explain", "42", "--json")
	if !strings.Contains(out, `"rests_on": []`) || !strings.Contains(out, `"events": []`) || !strings.Contains(out, `"state": "starting"`) {
		t.Fatalf("empty:\n%s", out)
	}
}

// The agent at a dialog: the verdict says whose and which event opened
// it.
func TestExplainSaysWhoseDialogTheAgentIsAt(t *testing.T) {
	f := explainFakes()
	at := func(s int) time.Time { return f.now.Add(time.Duration(s-60) * time.Second) }
	f.journal.Put(journalID("id-42"),
		hookEvent("UserPromptSubmit", "prompt", at(1), state.Facts{Prompt: "go", Turn: "t1"}),
		hookEvent("PermissionRequest", "dialog", at(2), state.Facts{Tool: "Bash", Text: "Bash: rm -rf x", Turn: "t1", Subagent: true}),
	)
	_, out, _ := f.run("explain", "42")
	for _, want := range []string{
		"state     needs input 58s",
		"dialog    at a dialog, a subagent's, opened at 2\nrests on  2\n",
		"dialog of tool Bash, of a subagent",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in\n%s", want, out)
		}
	}
	if strings.Contains(out, "rm -rf") {
		t.Errorf("the dialog's text:\n%s", out)
	}
}

// Every way an event is told in words, by its kind and the facts that hold
// no text.
func TestEventWhatTellsEveryKind(t *testing.T) {
	hook := func(kind string, f state.Facts) state.Event {
		return state.Event{Source: state.SourceHook, Kind: kind, Facts: f}
	}
	of := func(source, kind string, f state.Facts) state.Event {
		return state.Event{Source: source, Kind: kind, Facts: f}
	}
	for _, tc := range []struct {
		e    state.Event
		want string
	}{
		{hook("UserPromptSubmit", state.Facts{Prompt: "x"}), "prompt"},
		{hook("UserPromptSubmit", state.Facts{Wake: true}), "Claude's wake-up when background work finished"},
		{hook("UserPromptSubmit", state.Facts{Wake: true, Woke: "b1"}), "Claude's wake-up when background work finished, for task b1"},
		{hook("Stop", state.Facts{}), "turn end"},
		{hook("Stop", state.Facts{Question: true, Tasks: []state.EventTask{{ID: "s1", Type: "shell", Status: "running"}, {ID: "w1", Type: "monitor", Status: "running", Artifact: true}, {ID: "d", Type: "shell", Status: "completed"}}}),
			"turn end, the reply ends with ?, running: shell s1, monitor w1 (the artifact's watch)"},
		{hook("StopFailure", state.Facts{Reason: "authentication_failed"}), "turn end by an API error (authentication_failed)"},
		{hook("StopFailure", state.Facts{}), "turn end by an API error"},
		{hook("PermissionRequest", state.Facts{Tool: "AskUserQuestion"}), "dialog of tool AskUserQuestion"},
		{hook("Notification", state.Facts{Notice: "permission_prompt"}), "notification (permission_prompt)"},
		{hook("Notification", state.Facts{}), "notification"},
		{hook("Elicitation", state.Facts{Server: "elicit"}), "MCP elicitation of server elicit"},
		{hook("ElicitationResult", state.Facts{Server: "elicit"}), "MCP elicitation answered"},
		{hook("PostToolUse", state.Facts{Tool: "Bash"}), "end of tool Bash"},
		{hook("PostToolUseFailure", state.Facts{Tool: "Bash"}), "failed end of tool Bash"},
		{hook("PostToolUse", state.Facts{Tool: "Bash", Started: "~b2", Subagent: true}), "end of tool Bash, started a subagent's background task b2, of a subagent"},
		{hook("PostToolUse", state.Facts{Tool: "Monitor", Started: "=m1"}), "end of tool Monitor, started its own watch m1"},
		{hook("SessionStart", state.Facts{Start: "resume"}), "session start (resume)"},
		{hook("SessionEnd", state.Facts{Reason: "prompt_input_exit"}), "session end (prompt_input_exit)"},
		{hook(state.KindDelivery, state.Facts{Hook: "stop", Messages: []string{"1"}}), "delivery of 1 message, at a turn end the hook held back"},
		{hook(state.KindDelivery, state.Facts{Hook: "prompt", Messages: []string{"1", "2"}}), "delivery of 2 messages, with the next prompt"},
		{hook(state.KindDelivery, state.Facts{Hook: "answer", Messages: []string{"1"}}), "delivery of 1 message, after a tool's end"},
		{hook(state.KindDelivery, state.Facts{Hook: "other"}), "delivery of 0 messages"},
		{hook(state.KindUnreadable, state.Facts{Size: 20 << 20}), "an event file hq cannot read, 20971520 bytes"},
		{hook("PreCompact", state.Facts{}), "PreCompact"},
		{of(state.SourceScreen, state.KindTurnEnded, state.Facts{Text: "Interrupted"}), "a turn the user ended (Esc)"},
		{of(state.SourceScreen, state.KindAtRest, state.Facts{}), "the screen first seen at rest"},
		{of(state.SourceScreen, state.KindToolCut, state.Facts{}), "the screen first seen at rest at a tool the user cut short"},
		{of(state.SourceScreen, state.KindRewound, state.Facts{}), "the turn was rewound: its screen stayed at rest"},
		{of(state.SourceScreen, state.KindTurnNotTaken, state.Facts{}), "the turn Claude owed does not come: its screen stayed at rest"},
		{of(state.SourceScreen, state.KindOwnDialog, state.Facts{}), "a dialog of Claude's own"},
		{of(state.SourceScreen, state.KindOwnDialogGone, state.Facts{}), "the dialog of Claude's own is gone"},
		{of(state.SourceScreen, state.KindAtWork, state.Facts{}), "Claude at work"},
		{of(state.SourceScreen, state.KindTookQueued, state.Facts{}), "Claude took the prompt it queued"},
		{of(state.SourceScreen, state.KindWokeUp, state.Facts{}), "Claude woke up after a turn the user ended"},
		{of(state.SourceScreen, state.KindTookSlashCommand, state.Facts{}), "Claude took the first prompt as a slash command of its own: no turn comes"},
		{of(state.SourceScreen, "Other", state.Facts{}), "Other"},
		{of(state.SourceTmux, state.KindPaneDied, state.Facts{}), "the pane died"},
		{of(state.SourceTmux, "Other", state.Facts{}), "Other"},
		{of(state.SourceSbx, state.KindNotRunning, state.Facts{}), "sbx says the sandbox does not run"},
		{of(state.SourceSbx, "Other", state.Facts{}), "Other"},
		{of(state.SourceHQ, state.KindLaunched, state.Facts{Start: "restore"}), "launched (restore)"},
		{of(state.SourceHQ, state.KindRelaunched, state.Facts{}), "relaunched"},
		{of(state.SourceHQ, state.KindTakenDown, state.Facts{}), "taken down for a restart"},
		{of(state.SourceHQ, state.KindAnswer, state.Facts{Whose: state.DialogHooks}), "answered its question dialog"},
		{of(state.SourceHQ, state.KindAnswer, state.Facts{Whose: state.DialogOwn}), "answered a dialog of Claude's own"},
		{of(state.SourceHQ, state.KindWithdrawn, state.Facts{Withdrawn: state.KindDelivery}), "took back what it appended as delivery: it did not reach the agent"},
		{of(state.SourceHQ, state.KindDelivery, state.Facts{Messages: []string{"1", "2"}}), "delivery of 2 messages, typed in by hq"},
		{of(state.SourceHQ, "Other", state.Facts{}), "Other"},
	} {
		if got := eventWhat(tc.e); got != tc.want {
			t.Errorf("%s %s: got %q, want %q", tc.e.Source, tc.e.Kind, got, tc.want)
		}
	}
}

// Why an event was set aside, in words.
func TestAsideWhyTellsEveryReason(t *testing.T) {
	for aside, want := range map[string]string{
		"":                   "",
		state.AsideDuplicate: " - set aside: another hq process appended it first",
		state.AsideOvertaken: " - set aside: a later report overtook it",
		"odd":                " - set aside: odd",
	} {
		if got := asideWhy(aside); got != want {
			t.Errorf("%q: got %q, want %q", aside, got, want)
		}
	}
}

// A turn end that made the agent question shows the fact that its reply
// ends with ?, never the reply.
func TestExplainShowsThatTheReplyEndsWithAQuestionMark(t *testing.T) {
	f := explainFakes()
	f.states["id-42"] = journaltest.HookState{State: state.Question, Since: f.now.Add(-time.Minute), Last: "Shall I open the pull request?"}
	f.details["id-42"] = state.Detail{Reply: "All tests pass.\n\nShall I open the pull request?"}
	code, out, _ := f.run("explain", "42")
	if code != 0 || !strings.Contains(out, "turn end, the reply ends with ?") || !strings.Contains(out, "question") ||
		strings.Contains(out, "pull request") || strings.Contains(out, "tests pass") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

// An agent whose turn an API error ended is done on that event, which hq
// explain names with the error's kind, never Claude's error line (#224).
func TestExplainNamesTheAPIErrorTheStateRestsOn(t *testing.T) {
	f := explainFakes()
	at := func(m int) time.Time { return f.now.Add(time.Duration(m-10) * time.Minute) }
	f.journal.Put(journalID("id-42"),
		state.Event{Source: state.SourceHQ, Kind: state.KindLaunched, Time: at(0), Facts: state.Facts{Start: "new"}},
		hookEvent("SessionStart", "start", at(1), state.Facts{Start: "startup", Session: "s1", Branch: "main"}),
		hookEvent("UserPromptSubmit", "prompt", at(2), state.Facts{Prompt: "list the files", Turn: "t0", Branch: "main"}),
		hookEvent(state.KindTurnFailed, state.CaseFail, at(3), state.Facts{Turn: "t0", Reason: "authentication_failed"}),
	)
	f.details["id-42"] = state.Detail{Reply: "Login expired · Please run /login"}
	code, out, errOut := f.run("explain", "42")
	if code != 0 {
		t.Fatalf("exit %d %q", code, errOut)
	}
	if !strings.Contains(out, "state     done 7m") || !strings.Contains(out, "rests on  4\n") ||
		!strings.Contains(out, "  4  2026-09-27 11:53:00.000  hook    turn end by an API error (authentication_failed)  done") || strings.Contains(out, "Login") {
		t.Fatalf("got\n%s", out)
	}
}

// fixtureJournal is what hq explain reads of a fixture journal of
// internal/state/testdata/journals: the events of its steps and the
// replies the journal kept.
type fixtureJournal struct {
	Steps []struct {
		Events []state.Event `json:"events"`
		Reply  *state.Reply  `json:"reply"`
	} `json:"steps"`
}

// No output of hq explain, plain or --json, with or without --all,
// contains a prompt, a reply, a dialog's question or a message's text from
// the fixture journal it explains (story 5 of #165).
func TestExplainShowsNoTextOfTheConversationOfAnyFixtureJournal(t *testing.T) {
	paths, err := filepath.Glob("../state/testdata/journals/*.json")
	if err != nil || len(paths) == 0 {
		t.Fatalf("no fixture journals: %v", err)
	}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		var fx fixtureJournal
		if err := json.Unmarshal(data, &fx); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		var events []state.Event
		var texts []string
		reply := state.Reply{}
		for _, s := range fx.Steps {
			events = append(events, s.Events...)
			for _, e := range s.Events {
				texts = append(texts, e.Facts.Prompt, e.Facts.Text)
			}
			if s.Reply != nil {
				reply = *s.Reply
				texts = append(texts, s.Reply.Text)
				for _, task := range s.Reply.Tasks {
					texts = append(texts, task.Description, task.Command)
				}
			}
		}
		// The texts of messages the fixtures leave in the inbox.
		for i := 1; i <= 9; i++ {
			texts = append(texts, "message "+strconv.Itoa(i))
		}
		// The same journal with every text replaced: what hq explain shows
		// of it must be the same, character for character.
		blank := make([]state.Event, len(events))
		for i, e := range events {
			e.Facts.Prompt, e.Facts.Text = "", ""
			blank[i] = e
		}
		blankReply := state.Reply{Number: reply.Number}
		now := events[len(events)-1].Time.Add(time.Minute)
		for _, all := range []bool{false, true} {
			out, js := explainOutputs(t, events, reply, now, all)
			bout, bjs := explainOutputs(t, blank, blankReply, now, all)
			if out != bout || js != bjs {
				t.Errorf("%s (all %v): the output depends on the conversation's text:\n%s\nwithout it:\n%s", filepath.Base(p), all, out, bout)
			}
			if all && strings.Count(js, `"n":`) != len(events) {
				t.Errorf("%s: --all shows not all %d events", filepath.Base(p), len(events))
			}
			for _, o := range []string{out, js} {
				for _, text := range texts {
					// A text the output without any text holds too is one of
					// its own words ("started") or a fact of the event (a
					// dialog's text that is its tool's name).
					text = strings.TrimSpace(text)
					if text != "" && strings.Contains(o, text) && !strings.Contains(bout+bjs, text) {
						t.Errorf("%s (all %v): shows %q", filepath.Base(p), all, text)
					}
				}
			}
		}
	}
}

// explainOutputs are hq explain's plain and JSON outputs of a journal.
func explainOutputs(t *testing.T, events []state.Event, reply state.Reply, now time.Time, all bool) (plain, js string) {
	t.Helper()
	v := explain("42", events, reply, now, all)
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	if err := v.write(&b, time.UTC, all); err != nil {
		t.Fatal(err)
	}
	return b.String(), string(data)
}

// It explains an agent that has ended and is still listed, and a lost
// agent before hq restore, from its journal.
func TestExplainExplainsAnEndedAndALostAgent(t *testing.T) {
	f := explainFakes()
	f.states["id-42"] = journaltest.HookState{State: state.Done, Since: f.now.Add(-2 * time.Minute), Last: "done"}
	f.tmux.windows[1].PaneDead = true
	code, out, errOut := f.run("explain", "42")
	if code != 0 || !strings.HasPrefix(out, "name      42\nstate     ended") || !strings.Contains(out, "the pane died") {
		t.Fatalf("ended: exit %d %q\n%s", code, errOut, out)
	}

	f = lostFakes()
	code, out, errOut = f.run("explain", "a")
	if code != 0 || !strings.HasPrefix(out, "name      a\nstate     working") || !strings.Contains(out, "prompt") {
		t.Fatalf("lost: exit %d %q\n%s", code, errOut, out)
	}
	// A lost agent of another generation is refused as an outdated one.
	f = lostFakes()
	f.records["/w/app"][0].Generation = version.Generation - 1
	code, out, errOut = f.run("explain", "a")
	if code != ExitUsage || out != "" || errOut != "hq: a was started by an older hq: kill it and start it again\n" {
		t.Fatalf("lost outdated: exit %d %q %q", code, out, errOut)
	}
}

// An agent hq does not know fails as hq read does; an outdated agent is
// refused with its line; a usage error says the usage.
func TestExplainRefusesAnUnknownOrOutdatedAgent(t *testing.T) {
	f := explainFakes()
	code, out, errOut := f.run("explain", "x")
	if code != ExitNotFound || out != "" || errOut != "hq: no agent 'x' (see hq ls)\n" {
		t.Fatalf("unknown: exit %d %q %q", code, out, errOut)
	}
	for _, args := range [][]string{{"explain", "old"}, {"explain", "old", "--all", "--json"}} {
		f := outdatedFakes()
		code, out, errOut := f.run(args...)
		if code != ExitUsage || out != "" || errOut != outdatedLine {
			t.Errorf("%v: exit %d %q %q", args, code, out, errOut)
		}
	}
	for _, args := range [][]string{{"explain"}, {"explain", "42", "--text"}, {"explain", "42", "43"}} {
		code, _, errOut := explainFakes().run(args...)
		if code != ExitUsage || errOut != "hq: usage: hq explain NAME [--all] [--json]\n" {
			t.Errorf("%v: exit %d %q", args, code, errOut)
		}
	}
}

// An sbx too old for hq is one more failed sbx ls to hq explain (design
// §3.6): it explains the journal, and appends nothing of sbx.
func TestExplainTakesAnSbxTooOldAsAFailedSbxLs(t *testing.T) {
	f := explainFakes()
	f.journal.Put(journalID("id-42"), backgroundJournal(f.now)...)
	f.sbx.err = &sbx.TooOldError{Version: "0.47.0"}
	f.sbx.sandboxes = nil
	code, out, errOut := f.run("explain", "42", "--all")
	if code != 0 || errOut != "" || !strings.Contains(out, "state     working") || strings.Contains(out, "sbx  ") {
		t.Fatalf("exit %d %q\n%s", code, errOut, out)
	}
}
