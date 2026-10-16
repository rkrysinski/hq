// Package agent is hq's row model: one agent as hq ls and, later, the
// dashboard show it (design §3.8, §6).
package agent

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"hash/fnv"
	"math"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/rkrysinski/hq/internal/state"
	"github.com/rkrysinski/hq/internal/tmux"
)

// Agent is one agent: its home window and what hq stored on it.
type Agent struct {
	Window     string     `json:"-"`
	Pane       string     `json:"-"` // the agent's own pane
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	RepoPath   string     `json:"repo_path"`
	Sandbox    string     `json:"sandbox"`
	Started    time.Time  `json:"started"`
	Alive      bool       `json:"-"` // the agent's pane's process runs
	Docked     bool       `json:"-"` // its pane is in the dashboard's slot
	New        bool       `json:"-"` // started with hq new, not yet reported (S2)
	Inbox      bool       `json:"-"` // its hooks deliver messages (hq send, ADR 0012)
	Hooks      int        `json:"-"` // the generation of its hooks, as hq stored it (see Supervisable)
	ending     bool       // hq is taking the agent down (its sandbox restarting)
	reported   bool       // its session has reported, so its sandbox has run
	turnEnd    string     // a turn the user ended, as hq first saw it (see settle)
	restSeen   string     // when hq first saw its screen at rest, as it looked (see settle)
	resting    bool       // its screen is at rest, not yet for long enough (see settle)
	rested     bool       // its turn end was read off a screen at rest, decided RestDelay after its moment (see Entered)
	prompt     string     // the prompt it works on, as its hooks reported it
	deadAt     time.Time  // when its pane died, as tmux says; zero when unknown
	endedAt    time.Time  // when its session reported its end
	atPrompt   bool       // its turn ended with background work running: it works, at its prompt (see OnBackgroundWork)
	owed       bool       // at its prompt with nothing running: Claude owes it a turn (see settle)
	woken      bool       // it works on a turn Claude woke itself for (see settle)
	ownAsk     *state.Ask // the dialog Claude shows of its own on its screen, if any (see settle)
	dialog     Whose      // whose dialog it is at while it needs input: its hooks', a subagent's, or Claude's own, seen (ownAsk) or recorded on its window (see recallDialog)
	answered   string     // when hq first saw Claude at work after the dialog its hooks reported (see settle)
	said       string     // its latest turn's last message, while a dialog's text stands in for it
	dialogSeen string     // when hq first saw that dialog, as it read (see settle)
	started    string     // the start as stored, which keys endSeen
	reportAt   time.Time  // the time of its session's last report; zero before its first
	endSeen    string     // when hq first saw it ended (see seeEnd)
	wokeSeen   string     // when Claude woke it after a turn the user ended (see seeWake)
	wokeKey    string     // what keys that record now, if such a turn it is
	wokeAt     time.Time  // the moment to record for it, when this look sees the wake-up
	State      string     `json:"state"`
	Since      time.Time  `json:"since"`
	Branch     string     `json:"branch"`
	Worktree   string     `json:"worktree"` // where Claude works, as the sandbox sees it (for hq code, M4)
	Last       string     `json:"last"`
}

// supervisingHooks is the generation of hooks from which they tell a
// supervised turn.
const supervisingHooks = 2

// Supervisable reports whether the agent's hooks tell a supervised turn,
// whose end notifies nobody (spec §5). The hooks of an agent started by an
// older hq do not: it notifies at every turn end until it is relaunched.
func (a Agent) Supervisable() bool { return a.Hooks >= supervisingHooks }

// Repo is the repository's display name.
func (a Agent) Repo() string { return filepath.Base(a.RepoPath) }

var nameRE = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// MaxName is the longest agent name, in characters (spec §4.2).
const MaxName = 32

// NameChars reports whether name is made of the characters a name may have:
// letters, digits, - and _ (spec §4.2), and has at least one.
func NameChars(name string) bool { return nameRE.MatchString(name) }

// NewID returns a fresh agent id.
func NewID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// Stamp is how hq stores a start time on a home window: Unix seconds with
// milliseconds, fine enough to tell a report of the session before a
// relaunch from one of the relaunched session (see Apply).
func Stamp(t time.Time) string {
	return strconv.FormatFloat(float64(t.UnixMilli())/1000, 'f', 3, 64)
}

// ParseStamp reads a Stamp, or whole Unix seconds as hq stored them before.
func ParseStamp(s string) (time.Time, bool) {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.UnixMilli(int64(math.Round(f * 1000))), true
}

// FromWindows returns the agents among hq's windows, in window order.
// Windows without an hq id (the dashboard's own window) are not agents.
func FromWindows(ws []tmux.Window) []Agent {
	var as []Agent
	for _, w := range ws {
		o := w.Options
		if o["id"] == "" {
			continue
		}
		a := Agent{Window: w.ID, Pane: w.Pane, turnEnd: o["turnend"], restSeen: o["restseen"], dialogSeen: o["dialogseen"], answered: o["answered"], ID: o["id"], Name: o["name"], RepoPath: o["repo"], Sandbox: o["sandbox"], Alive: !w.PaneDead, Docked: w.Docked, ending: o["ending"] != "", State: state.Starting,
			deadAt: w.DeadAt, started: o["started"], endSeen: o["endseen"], wokeSeen: o["wokeseen"]}
		if a.Name == "" {
			a.Name = w.Name
		}
		if t, ok := ParseStamp(o["started"]); ok {
			a.Started = t
		}
		a.Since = a.Started
		// A window marked ending belongs to an agent hq is taking down (its
		// sandbox restarting): ended already, though its pane still runs.
		if w.PaneDead || a.ending {
			a.State = state.Ended
		}
		a.New = o["new"] != "" && a.State == state.Starting
		a.Inbox = o["inbox"] != ""
		a.Hooks, _ = strconv.Atoi(o["inbox"])
		as = append(as, a)
	}
	return as
}

// EndedLast is the last message of an agent that ended without one: it
// never reported a message before its session went (spec S7, the mocks).
const EndedLast = "[session ended]"

// Sandboxes is sbx's answer to which sandboxes run, with the moment hq
// asked for it. hq keeps an answer for up to a second, and reads the agents
// after it has it, so an answer can be older than an agent's report.
type Sandboxes struct {
	// Running says, by a sandbox's name, whether it runs; nil when sbx
	// could not say, which ends no agent.
	Running map[string]bool
	// Asked is when hq asked sbx.
	Asked time.Time
}

// stopped reports whether the answer ends a: its sandbox does not run, and
// a has reported, but not since hq asked. Before its first report its
// session may still wait for sbx run to start the sandbox (S2). A report
// after the question proves the sandbox ran after it: the answer was taken
// while the sandbox started, and the next one decides (#48).
func (s Sandboxes) stopped(a Agent) bool {
	return s.Running != nil && !s.Running[a.Sandbox] && a.reported && !a.reportAt.After(s.Asked)
}

// Collect is the row model's collection, shared by hq ls and the list
// program (design §3.8, §5.1): the agents of the home windows with what
// their state files report, and ended when sbx lists their sandbox as not
// running (Sandboxes). sbx ends only an agent that has reported: until then its sandbox may not
// have started yet (sbx run starts a stopped one, which takes seconds), so
// a sandbox not running is no sign that the session ended; the agent is
// starting (S2), and ended when its pane dies, as sbx run returns when its
// sandbox stops. An ended agent keeps the last turn end hq saw on its
// screen, and one without a last message has EndedLast, so
// hq ls and the list show the same (design §6).
func Collect(ws []tmux.Window, read func(root, id string) (state.Report, bool), sandboxes Sandboxes) []Agent {
	as := FromWindows(ws)
	for i := range as {
		as[i].Apply(read(as[i].RepoPath, as[i].ID))
		if sandboxes.stopped(as[i]) {
			as[i].State = state.Ended
			as[i].New = false
		}
		if as[i].State == state.Ended {
			as[i].keepTurnEnd()
			as[i].endTime()
			if as[i].Last == "" {
				as[i].Last = EndedLast
			}
		}
	}
	return as
}

// keepTurnEnd gives an ended agent the message of the last turn the user
// ended at it, as hq saw it on its screen (settle), when no hook has
// reported since: its last known message (spec S7, #111). Its time stays
// the moment it ended.
func (a *Agent) keepTurnEnd() {
	if f := strings.SplitN(a.turnEnd, " ", 3); len(f) == 3 && f[0] == a.key() && f[2] != ownLast {
		a.Last = f[2]
	}
}

// endTime makes an ended agent's time the moment it ended (spec §5): its
// session's end when it reported one (/exit), else when hq first saw it
// ended after its last report, else when its pane died, as tmux says (a
// crash, a kill, its sandbox stopped), never before its last report. When none is known it
// stays at the last report until seeEnd records the moment.
func (a *Agent) endTime() {
	switch {
	case !a.endedAt.IsZero():
		a.Since = a.endedAt
	case a.seen():
	case !a.deadAt.IsZero() && a.deadAt.After(a.Since):
		a.Since = a.deadAt
	}
}

// seen applies the moment seeEnd recorded for this session since its last
// report, if any: a record from before the report is not its end (#104).
func (a *Agent) seen() bool {
	t, ok := a.seenAt()
	if ok {
		a.Since = t
	}
	return ok
}

// seenAt is the moment seeEnd recorded for this session since its last
// report.
func (a Agent) seenAt() (time.Time, bool) {
	at, ok := strings.CutPrefix(a.endSeen, a.endKey())
	if !ok {
		return time.Time{}, false
	}
	n, err := strconv.ParseInt(at, 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.Unix(0, n), true
}

// Entered is the moment from which any hq process can see the agent in its
// state (design §3.4, "Entered after a moment"), which hq wait compares with
// its --since. It is Since, the moment AGE counts from, except where that
// is dated before the state can be seen: a turn the user rewound, or one
// Claude owed and did not take, is decided only RestDelay after its screen
// was first seen at rest, and an ended
// agent is ended from the first of its session's end report, the moment hq
// first saw it ended, and its pane's death, which tmux dates to the whole
// second, so a second after that. Every process derives it from the same
// state file and window records, so all agree on it.
func (a Agent) Entered() time.Time {
	switch {
	case a.State == state.Ended:
		var first time.Time
		earlier := func(t time.Time) {
			if first.IsZero() || t.Before(first) {
				first = t
			}
		}
		if !a.endedAt.IsZero() {
			earlier(a.endedAt)
		}
		if !a.deadAt.IsZero() {
			earlier(a.deadAt.Add(time.Second))
		}
		if t, ok := a.seenAt(); ok {
			earlier(t)
		}
		if !first.IsZero() {
			return first
		}
	case a.State == state.Done && a.rested:
		return a.Since.Add(RestDelay)
	}
	return a.Since
}

// seeEnd records, for an ended agent whose end nothing dates (its sandbox
// stopped or restarting while its pane still runs, a docked pane gone),
// that hq sees it ended now: the record to store as the endseen option,
// keyed by the agent's start and its last report, so that neither a
// relaunch nor a report after the record carries it over, with no Value
// when there is nothing to store.
func (a *Agent) seeEnd(now time.Time) record {
	if a.State != state.Ended || !a.endedAt.IsZero() || !a.deadAt.IsZero() {
		return record{}
	}
	key := a.endKey()
	if strings.HasPrefix(a.endSeen, key) {
		return record{}
	}
	a.Since = now
	return record{"endseen", key, key + nanos(now)}
}

// endKey names the agent's session and its last report in its endseen
// record: "STARTED REPORT ", REPORT 0 before its first report.
func (a Agent) endKey() string {
	report := "0"
	if !a.reportAt.IsZero() {
		report = nanos(a.reportAt)
	}
	return a.started + " " + report + " "
}

// A record is a moment hq saw, to keep on the agent's window as the user
// option Option (one of tmux.OptionKeys): Value, unless the option already
// holds a value starting with Key, a record of the same thing that another
// hq process stored first. The first record wins, so every process shows
// the same moment (design §3.4); see keep.
type record struct{ Option, Key, Value string }

// keep applies what the window holds after r was offered to it: stored,
// which is r.Value, or the record another hq process stored first.
func (a *Agent) keep(r record, stored string) {
	if stored == r.Value {
		return
	}
	switch r.Option {
	case "turnend":
		a.turnEnd = stored
		a.recallFor(strings.TrimSuffix(r.Key, " "))
	case "restseen":
		a.restSeen = stored
	case "answered":
		a.answered = stored
	case "dialogseen":
		a.dialogSeen = stored
		if at, ok := a.dialogSeenAt(a.ownFingerprint()); ok {
			a.Since = at
		}
	case "endseen":
		a.endSeen = stored
		a.seen()
	case "wokeseen":
		a.wokeSeen = stored
		if at, ok := a.wokeSeenAt(); ok {
			a.Since = at
		}
	}
}

// Apply adds what the agent's state file reports (ok false: nothing yet).
// A dead pane, or one hq is taking down, stays ended whatever the file
// says (Collect dates the end); the file still gives the last known
// message. A report older than the agent's start is its previous
// session's (hq sandbox restart relaunches an agent under its id, design
// §3.6): it gives the last known message, branch and worktree until the
// new session reports, but neither the state nor its time.
func (a *Agent) Apply(r state.Report, ok bool) {
	if !ok {
		return
	}
	a.Last = r.Last
	a.Branch, a.Worktree, a.prompt = r.Branch, r.Cwd, r.Prompt
	if r.Since.Before(a.Started) {
		return
	}
	a.New, a.reported, a.reportAt = false, true, r.Since
	// An event the hook kept beside the report (state.Report.Kept) is the
	// later one: the agent works on, since the same moment.
	if r.Latest.After(r.Since) {
		a.reportAt = r.Latest
	}
	a.atPrompt, a.owed, a.woken = r.Background, r.Background && r.Owed, r.Woken
	a.said = r.Said
	a.dialog = NoDialog
	switch {
	case r.State != state.NeedsInput:
	case r.SubagentDialog:
		a.dialog = SubagentDialog
	default:
		a.dialog = HooksDialog
	}
	if r.State == state.Ended {
		a.endedAt = r.Since
	}
	if a.Alive && !a.ending {
		a.State = r.State
	}
	if a.Alive || r.Since.After(a.Since) {
		a.Since = r.Since
	}
	a.sinceWake(r)
}

// sinceWake dates the work of an agent Claude woke after a turn the user
// ended at it (design §3.4). The hooks know nothing of that end, so its
// report still counts from the prompt of the turn the user ended; hq saw
// the end (the turnend record of an earlier report since that prompt), and
// the agent works since the wake-up: the time of the report that is the
// wake-up, which the first look to see it records (seeWake), as the
// reports after it, of the same work, no longer tell.
func (a *Agent) sinceWake(r state.Report) {
	a.wokeKey, a.wokeAt = "", time.Time{}
	f := strings.SplitN(a.turnEnd, " ", 3)
	if a.State != state.Working || !r.Latest.After(r.Since) || len(f) != 3 || f[0] == a.key() {
		return
	}
	if ended, err := strconv.ParseInt(f[0], 10, 64); err != nil || ended < r.Since.UnixNano() || ended >= r.Latest.UnixNano() {
		return
	}
	a.wokeKey = nanos(r.Since) + " " + f[0] + " "
	switch at, seen := a.wokeSeenAt(); {
	case seen:
		a.Since = at
	case r.Woken:
		a.Since, a.wokeAt = r.Latest, r.Latest
	}
}

// wokeSeenAt is the moment seeWake recorded for the agent's work now.
func (a Agent) wokeSeenAt() (time.Time, bool) {
	at, ok := strings.CutPrefix(a.wokeSeen, a.wokeKey)
	if !ok || a.wokeKey == "" {
		return time.Time{}, false
	}
	n, err := strconv.ParseInt(at, 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.Unix(0, n), true
}

// seeWake records, for an agent whose report is Claude's wake-up after a
// turn the user ended, when that was: the record to store as the wokeseen
// option, keyed by the report its state counts from and the turn end it
// follows, with no Value when there is nothing to store.
func (a Agent) seeWake() record {
	if a.wokeAt.IsZero() {
		return record{}
	}
	return record{"wokeseen", a.wokeKey, a.wokeKey + nanos(a.wokeAt)}
}

// settleDelay is how old the hooks' report must be before the agent's
// screen can overrule it: Claude draws and runs its hooks independently, so
// for a moment the screen may lag behind the report.
const settleDelay = 500 * time.Millisecond

// unsettled reports whether the agent's screen may show what no hook
// reports (design §3.4): the agent runs, and its hooks said a moment ago
// that it works, so the user may have ended its turn; or that it needs
// input, so the user may have ended its turn or answered a dialog no hook
// reports the end of; or that it waits at its prompt, done, with a
// question, or working only on background work (OnBackgroundWork), where
// Claude may show a dialog of its own (settle). An agent at its prompt
// with background work running rests there, as after a rewind, until
// Claude wakes it, so only a dialog of Claude's own is read off its screen;
// unless none of that work runs any more and Claude only owes the agent a
// turn for it: that turn comes within moments, or never, which the screen
// tells (settle).
func (a Agent) unsettled(now time.Time) bool {
	return a.Alive && !a.ending && a.reported && a.State != state.Starting && a.State != state.Ended && now.Sub(a.reportAt) >= settleDelay
}

// OnBackgroundWork reports whether the agent works only on background work
// (subagents): its turn ended, and it sits at its prompt until Claude
// wakes it for the closing turn (spec §5). hq send types a message in
// there, as for an agent that is done.
func (a Agent) OnBackgroundWork() bool { return a.atPrompt && a.State == state.Working }

// waitsAtPrompt reports whether the agent waits at its prompt, where Claude
// may show a dialog of its own in place of the box (seeDialog): done or
// with a question, by its hooks or by a turn end hq saw, or working only on
// background work that Claude owes it no turn for. A dialog its hooks
// reported is never one.
func (a Agent) waitsAtPrompt() bool {
	return a.State == state.Done || a.State == state.Question || a.OnBackgroundWork() && !a.owed
}

// recall applies a turn end hq saw earlier on an unsettled agent: done,
// with its last message, since hq first saw it, as long as no hook has
// reported since. The screen is then not needed; it may have moved on, as
// when the user sends the next prompt a moment before its hook reports.
func (a *Agent) recall() bool { return a.recallFor(a.key()) }

// recallFor applies the turn end recorded for the report named key.
func (a *Agent) recallFor(key string) bool {
	f := strings.SplitN(a.turnEnd, " ", 3)
	if len(f) != 3 || f[0] != key {
		return false
	}
	n, err := strconv.ParseInt(f[1], 10, 64)
	if err != nil {
		return false
	}
	a.State, a.Since = state.Done, time.Unix(0, n)
	if f[2] != ownLast {
		a.Last = f[2]
	}
	// A turn end decided by a screen that stayed at rest (a rewind, a turn
	// Claude owed and did not take) carries the moment the screen was first
	// seen at rest, which the restseen record of the same report holds too.
	r := strings.Fields(a.restSeen)
	a.rested = len(r) == 3 && r[0] == key && r[2] == f[1]
	return true
}

// RestDelay is how long the screen of a working agent must stay at rest,
// unchanged, before hq takes the turn for one the user rewound (design
// §3.4). Claude at work redraws its spinner or its streaming reply several
// times a second; seen with Claude Code 2.1.283, its screen never stayed the
// same for more than about half a second.
const RestDelay = 2 * time.Second

// ownLast stands in a turnend record for the agent's own last message, as
// its hooks reported it: a turn that ended by itself. The message itself
// is never stored on the window: it is the agent's text, and only ever
// data (design §7.3).
const ownLast = "="

// Rewound is the last message of a turn the user rewound: an early Esc
// that put the prompt back in the box, which Claude reports nowhere.
const Rewound = "Interrupted"

// settle applies the screen of an unsettled agent: a dialog its hooks
// reported, of its own and not a subagent's, closes when the screen shows
// Claude at work (seeAnswer); a turn the user ended is done, since hq first
// saw it. Claude prints a line for most such turns
// (state.EndedByUser), whose last message it shows. An early Esc may
// rewind the turn instead, with no line and no hook: the working agent's
// screen is then at rest (state.AtRest), and once it has stayed so,
// unchanged, for RestDelay, the turn is done with Rewound as its last
// message, since hq first saw it at rest; not after a dialog of its hooks'
// that the screen saw answered (recallAnswer), as Claude has acted on the
// turn then, and the bare box closes no dialog of theirs (#70). The same
// goes for an agent at
// its prompt that Claude owes a turn, for background work that finished
// together with other work (state.Report.Owed): the turn comes at once, so
// a screen at rest for RestDelay (state.Idle) says it does not come, and
// the agent is done with its turn's message. Both moments are kept on the
// agent's window, keyed by the report they overrule, so hq ls and the list
// agree (see recall): the record to store there, as the turnend or the
// restseen option, with no Value when there is nothing new to store. An
// agent that waits at its prompt (waitsAtPrompt) may show a dialog of
// Claude's own instead (seeDialog); nothing else on the screen of an agent
// on background work counts.
func (a *Agent) settle(screen string, now time.Time) record {
	a.resting = false
	if a.recall(); a.waitsAtPrompt() {
		return a.seeDialog(screen, now)
	}
	answered := a.recallAnswer()
	key := a.key() + " "
	// What the record says of the turn's last message, and what it is.
	mark, last := Rewound, Rewound
	var rest, again bool
	if a.owed {
		// Its turn ended by itself, and Claude owes it one more: at rest
		// whatever its box holds, the turn's message its last. One more
		// look, RestDelay later, tells.
		rest, again = state.Idle(screen), true
		mark, last = ownLast, a.Last
	} else {
		// A rewind right after a turn the user ended leaves that turn's line
		// above the box: the rewind below tells that turn's end.
		rewound := a.State == state.Working && state.PutBack(screen, a.prompt)
		// A turn Claude woke itself for shows no prompt, so a line above the
		// box may be an earlier turn's: it counts only as the last thing said.
		ended := state.EndedByUser
		if a.woken {
			ended = state.EndedLast
		}
		if said, ok := ended(screen); ok && !rewound {
			a.State, a.Since, a.Last = state.Done, now, said
			return record{"turnend", key, key + nanos(now) + " " + said}
		}
		if a.State == state.NeedsInput {
			return a.seeAnswer(screen, now)
		}
		// After a dialog Claude has acted on the turn, which only an early
		// Esc rewinds: an answered agent's screen at rest is no rewind.
		if a.State != state.Working || answered {
			return record{}
		}
		rest, again = state.AtRest(screen, a.prompt)
	}
	if !rest {
		return record{}
	}
	fp := fingerprint(screen)
	at, ok := a.restedSince(fp)
	if !ok {
		a.resting = again
		return record{"restseen", key + fp + " ", key + fp + " " + nanos(now)}
	}
	if now.Sub(at) < RestDelay {
		a.resting = again
		return record{}
	}
	a.State, a.Since, a.Last, a.rested = state.Done, at, last, true
	return record{"turnend", key, key + nanos(at) + " " + mark}
}

// seeDialog applies the screen of an agent that waits at its prompt: a
// dialog of Claude's own in place of its prompt box (state.OwnDialog),
// which no hook reports, at the start of its session (#29) or opened after
// a turn, as by /effort (#66). While the screen shows it, the agent needs
// input, with the dialog's title as its last message, since hq first saw
// that dialog during its current report. That moment is kept on its
// window, so hq ls and the list agree and hq wait sees the agent arrive:
// the record to store as the dialogseen option, keyed by the report and
// the dialog's title and text. A look that finds the dialog
// gone closes the record, so a dialog opened again counts anew.
func (a *Agent) seeDialog(screen string, now time.Time) record {
	key := a.key() + " "
	ask, ok := state.OwnDialog(screen)
	if !ok {
		if _, _, open := recorded(a.dialogSeen, a.key()); open {
			// Open for this report: closed now.
			return record{"dialogseen", key + "- ", key + "-"}
		}
		return record{}
	}
	a.State, a.ownAsk, a.dialog, a.Last = state.NeedsInput, ask, OwnDialog, ask.Questions[0].Question
	fp := a.ownFingerprint()
	if at, ok := a.dialogSeenAt(fp); ok {
		a.Since = at
		return record{}
	}
	a.Since = now
	return record{"dialogseen", key + fp + " ", key + fp + " " + nanos(now)}
}

// recallSeen applies, for a look that cannot see the agent's screen, what
// hq saw there earlier during its current report and recorded on its
// window: a turn end (recall), or Claude at work after a dialog its hooks
// reported (recallAnswer), and a dialog of Claude's own (recallDialog). A
// look that sees the screen applies them as it settles it (settle).
func (a *Agent) recallSeen() {
	if !a.recall() {
		a.recallAnswer()
	}
	a.recallDialog()
}

// recallDialog applies, for a look that cannot see the agent's screen, a
// dialog of Claude's own that hq saw open there during its current report,
// as recorded on its window, at an agent that waits at its prompt: it
// needs input since hq first saw the dialog, rather than dropping back to
// its hooks' state for a refresh. What the dialog asks is not recorded.
func (a *Agent) recallDialog() {
	if _, at, ok := recorded(a.dialogSeen, a.key()); ok && a.waitsAtPrompt() {
		a.State, a.Since, a.dialog = state.NeedsInput, at, OwnDialog
	}
}

// seeAnswer applies the screen of an agent at a dialog its hooks reported:
// Claude at work there (state.AtWork) says the dialog was answered, as an
// MCP elicitation's answer, which no hook reports, or a tool's end Claude
// reports only when it ends (#70). The agent then works, since the
// dialog's report, with its turn's last message, as after a dialog its
// hooks saw answered. Anything else keeps the dialog open: the dialog
// itself, the prompt box, which a reply streaming shows too, and a narrow
// pane, whose footer drops Esc to interrupt. A subagent's dialog closes by
// its hooks only. The moment is kept on the agent's window, keyed by the
// report, so a later look at a screen that tells nothing keeps it working
// (recallAnswer): the record to store as the answered option. Like every
// first-wins record it holds the moment hq first saw it, "REPORT NANOS",
// which says which look saw it first; the agent's time is the report's.
func (a *Agent) seeAnswer(screen string, now time.Time) record {
	if !a.atHooksDialog() || !state.AtWork(screen) {
		return record{}
	}
	a.worksOn()
	key := a.key() + " "
	return record{"answered", key, key + nanos(now)}
}

// recallAnswer applies Claude at work that hq saw after the dialog the
// agent's hooks reported, during its current report (seeAnswer): it works,
// since that report, with its turn's last message.
func (a *Agent) recallAnswer() bool {
	if _, _, ok := recorded(a.answered, a.key()); !ok || !a.atHooksDialog() {
		return false
	}
	a.worksOn()
	return true
}

// atHooksDialog reports whether the agent needs input at a dialog its
// hooks reported, its own and not a subagent's: the one the screen may
// close (seeAnswer).
func (a Agent) atHooksDialog() bool { return a.State == state.NeedsInput && a.dialog == HooksDialog }

// worksOn applies a dialog of its hooks' answered: the agent works, since
// the dialog's report, with its turn's last message.
func (a *Agent) worksOn() { a.State, a.Last = state.Working, a.said }

// ownFingerprint names the dialog Claude shows of its own on the agent's
// screen by its title and text: not by its options, which a list such as
// the picker of /model scrolls as the cursor moves.
func (a Agent) ownFingerprint() string {
	if a.ownAsk == nil {
		return ""
	}
	return fingerprint(a.ownAsk.Questions[0].Question + "\n" + a.ownAsk.Description)
}

// dialogSeenAt is when hq first saw the dialog of fingerprint fp on the
// agent's screen, during its current report.
func (a Agent) dialogSeenAt(fp string) (time.Time, bool) {
	if fp == "" {
		return time.Time{}, false
	}
	return seenAt(a.dialogSeen, a.key(), fp)
}

// Resting reports whether settle saw the agent's screen at rest in a way
// that one more look, RestDelay later, decides: with its prompt put back
// in the box, the look of a turn the user rewound, or owed a turn by
// Claude; but not yet for long enough.
func (a Agent) Resting() bool { return a.resting }

// restedSince is when hq first saw the agent's screen at rest as it shows
// now (its fingerprint fp), during its current report.
func (a Agent) restedSince(fp string) (time.Time, bool) { return seenAt(a.restSeen, a.key(), fp) }

// seenAt reads the stored record of a screen hq first saw during a report,
// "REPORT FINGERPRINT NANOS": its moment, when it is of the report key and
// the fingerprint fp.
func seenAt(stored, key, fp string) (time.Time, bool) {
	got, at, ok := recorded(stored, key)
	return at, ok && got == fp
}

// recorded reads the stored record of what hq first saw on a screen during
// the report key, "REPORT [FINGERPRINT] NANOS": the screen's fingerprint,
// "" for a record without one, and the moment. A record closed since
// ("REPORT -"), or of another report, is none.
func recorded(stored, key string) (fp string, at time.Time, ok bool) {
	f := strings.Fields(stored)
	if len(f) < 2 || len(f) > 3 || f[0] != key {
		return "", time.Time{}, false
	}
	n, err := strconv.ParseInt(f[len(f)-1], 10, 64)
	if err != nil {
		return "", time.Time{}, false
	}
	if len(f) == 3 {
		fp = f[1]
	}
	return fp, time.Unix(0, n), true
}

// fingerprint tells screens apart without keeping them.
func fingerprint(screen string) string {
	h := fnv.New64a()
	h.Write([]byte(screen))
	return strconv.FormatUint(h.Sum64(), 16)
}

func nanos(t time.Time) string { return strconv.FormatInt(t.UnixNano(), 10) }

// key names the agent's current report in its turnend record: its latest
// one, which may be later than the moment its state counts from (Apply).
func (a Agent) key() string {
	if a.reported {
		return nanos(a.reportAt)
	}
	return nanos(a.Since)
}

// Find returns the agent named name.
func Find(as []Agent, name string) (Agent, bool) {
	for _, a := range as {
		if a.Name == name {
			return a, true
		}
	}
	return Agent{}, false
}

// Age formats a duration as spec §5 shows it: 3s, 2m, 1h, 2d.
func Age(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
