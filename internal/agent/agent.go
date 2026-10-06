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
	Window   string     `json:"-"`
	Pane     string     `json:"-"` // the agent's own pane
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	RepoPath string     `json:"repo_path"`
	Sandbox  string     `json:"sandbox"`
	Started  time.Time  `json:"started"`
	Alive    bool       `json:"-"` // the agent's pane's process runs
	Docked   bool       `json:"-"` // its pane is in the dashboard's slot
	New      bool       `json:"-"` // started with hq new, not yet reported (S2)
	Inbox    bool       `json:"-"` // its hooks deliver messages (hq send, ADR 0012)
	ending   bool       // hq is taking the agent down (its sandbox restarting)
	reported bool       // its session has reported, so its sandbox has run
	turnEnd  string     // a turn the user ended, as hq first saw it (see Settle)
	restSeen string     // when hq first saw its screen at rest, as it looked (see Settle)
	resting  bool       // its screen is at rest, not yet for long enough (see Settle)
	rewound  bool       // its turn end is a rewind, decided RestDelay after its moment (see Entered)
	prompt   string     // the prompt it works on, as its hooks reported it
	deadAt   time.Time  // when its pane died, as tmux says; zero when unknown
	endedAt  time.Time  // when its session reported its end
	atStart  bool       // its latest report is its session's start
	atPrompt bool       // its turn ended with background work running: it works, at its prompt (see OnBackgroundWork)
	owed     bool       // at its prompt with nothing running: Claude owes it a turn (see Settle)
	woken    bool       // it works on a turn Claude woke itself for (see Settle)
	startAsk *state.Ask // the dialog its screen shows at the start, if any
	started  string     // the start as stored, which keys endSeen
	reportAt time.Time  // the time of its session's last report; zero before its first
	endSeen  string     // when hq first saw it ended (see SeeEnd)
	State    string     `json:"state"`
	Since    time.Time  `json:"since"`
	Branch   string     `json:"branch"`
	Worktree string     `json:"worktree"` // where Claude works, as the sandbox sees it (for hq code, M4)
	Last     string     `json:"last"`
}

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
		a := Agent{Window: w.ID, Pane: w.Pane, turnEnd: o["turnend"], restSeen: o["restseen"], ID: o["id"], Name: o["name"], RepoPath: o["repo"], Sandbox: o["sandbox"], Alive: !w.PaneDead, Docked: w.Docked, ending: o["ending"] != "", State: state.Starting,
			deadAt: w.DeadAt, started: o["started"], endSeen: o["endseen"]}
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
		as = append(as, a)
	}
	return as
}

// EndedLast is the last message of an agent that ended without one: it
// never reported a message before its session went (spec S7, the mocks).
const EndedLast = "[session ended]"

// Collect is the row model's collection, shared by hq ls and the list
// program (design §3.8, §5.1): the agents of the home windows with what
// their state files report, and ended when sbx lists their sandbox as not
// running. running is nil when sbx could not say, which changes nothing.
// sbx ends only an agent that has reported: until then its sandbox may not
// have started yet (sbx run starts a stopped one, which takes seconds), so
// a sandbox not running is no sign that the session ended; the agent is
// starting (S2), and ended when its pane dies, as sbx run returns when its
// sandbox stops. An ended agent keeps the last turn end hq saw on its
// screen, and one without a last message has EndedLast, so
// hq ls and the list show the same (design §6).
func Collect(ws []tmux.Window, read func(root, id string) (state.Report, bool), running map[string]bool) []Agent {
	as := FromWindows(ws)
	for i := range as {
		as[i].Apply(read(as[i].RepoPath, as[i].ID))
		if running != nil && !running[as[i].Sandbox] && as[i].reported {
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
// ended at it, as hq saw it on its screen (Settle), when no hook has
// reported since: its last known message (spec S7, #111). Its time stays
// the moment it ended.
func (a *Agent) keepTurnEnd() {
	if f := strings.SplitN(a.turnEnd, " ", 3); len(f) == 3 && f[0] == a.key() {
		a.Last = f[2]
	}
}

// endTime makes an ended agent's time the moment it ended (spec §5): its
// session's end when it reported one (/exit), else when hq first saw it
// ended after its last report, else when its pane died, as tmux says (a
// crash, a kill, its sandbox stopped), never before its last report. When none is known it
// stays at the last report until SeeEnd records the moment.
func (a *Agent) endTime() {
	switch {
	case !a.endedAt.IsZero():
		a.Since = a.endedAt
	case a.seen():
	case !a.deadAt.IsZero() && a.deadAt.After(a.Since):
		a.Since = a.deadAt
	}
}

// seen applies the moment SeeEnd recorded for this session since its last
// report, if any: a record from before the report is not its end (#104).
func (a *Agent) seen() bool {
	t, ok := a.seenAt()
	if ok {
		a.Since = t
	}
	return ok
}

// seenAt is the moment SeeEnd recorded for this session since its last
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
// is dated before the state can be seen: a turn the user rewound is decided
// only RestDelay after its screen was first seen at rest, and an ended
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
	case a.State == state.Done && a.rewound:
		return a.Since.Add(RestDelay)
	}
	return a.Since
}

// SeeEnd records, for an ended agent whose end nothing dates (its sandbox
// stopped or restarting while its pane still runs, a docked pane gone),
// that hq sees it ended now: the record to store as the endseen option,
// keyed by the agent's start and its last report, so that neither a
// relaunch nor a report after the record carries it over, with no Value
// when there is nothing to store.
func (a *Agent) SeeEnd(now time.Time) Record {
	if a.State != state.Ended || !a.endedAt.IsZero() || !a.deadAt.IsZero() {
		return Record{}
	}
	key := a.endKey()
	if strings.HasPrefix(a.endSeen, key) {
		return Record{}
	}
	a.Since = now
	return Record{"endseen", key, key + nanos(now)}
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

// A Record is a moment hq saw, to keep on the agent's window as the user
// option Option (one of tmux.OptionKeys): Value, unless the option already
// holds a value starting with Key, a record of the same thing that another
// hq process stored first. The first record wins, so every process shows
// the same moment (design §3.4); see Keep.
type Record struct{ Option, Key, Value string }

// Keep applies what the window holds after r was offered to it: stored,
// which is r.Value, or the record another hq process stored first.
func (a *Agent) Keep(r Record, stored string) {
	if stored == r.Value {
		return
	}
	switch r.Option {
	case "turnend":
		a.turnEnd = stored
		a.recall(strings.TrimSuffix(r.Key, " "))
	case "restseen":
		a.restSeen = stored
	case "endseen":
		a.endSeen = stored
		a.seen()
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
	a.atStart = r.AtStart && r.State == state.Done
	a.atPrompt, a.owed, a.woken = r.Background, r.Background && r.Owed, r.Woken
	if r.State == state.Ended {
		a.endedAt = r.Since
	}
	if a.Alive && !a.ending {
		a.State = r.State
	}
	if a.Alive || r.Since.After(a.Since) {
		a.Since = r.Since
	}
}

// settleDelay is how old the hooks' report must be before the agent's
// screen can overrule it: Claude draws and runs its hooks independently, so
// for a moment the screen may lag behind the report.
const settleDelay = 500 * time.Millisecond

// Unsettled reports whether the agent's screen may show a turn that the
// user ended, which no hook reports (design §3.4): the agent runs, and its
// hooks said a moment ago that it works or needs input. An agent whose turn
// ended with background work running (OnBackgroundWork) is not: its hooks reported
// that end, and it rests at its prompt, as after a rewind, until Claude
// wakes it. Unless none of that work runs any more and Claude only owes
// the agent a turn for it: that turn comes within moments, or never, which
// the screen tells (Settle).
func (a Agent) Unsettled(now time.Time) bool {
	return a.Alive && !a.ending && a.reported && (!a.atPrompt || a.owed) && (a.State == state.Working || a.State == state.NeedsInput || a.atStart) && now.Sub(a.reportAt) >= settleDelay
}

// OnBackgroundWork reports whether the agent works only on background work
// (subagents): its turn ended, and it sits at its prompt until Claude
// wakes it for the closing turn (spec §5). hq send types a message in
// there, as for an agent that is done.
func (a Agent) OnBackgroundWork() bool { return a.atPrompt && a.State == state.Working }

// StartAsk is the dialog Claude shows in place of its prompt box at the
// start of the agent's session, as Settle read it off the screen; nil when
// there is none.
func (a Agent) StartAsk() *state.Ask { return a.startAsk }

// Recall applies a turn end hq saw earlier on an Unsettled agent: done,
// with its last message, since hq first saw it, as long as no hook has
// reported since. The screen is then not needed; it may have moved on, as
// when the user sends the next prompt a moment before its hook reports.
func (a *Agent) Recall() bool { return a.recall(a.key()) }

// recall applies the turn end recorded for the report named key.
func (a *Agent) recall(key string) bool {
	f := strings.SplitN(a.turnEnd, " ", 3)
	if len(f) != 3 || f[0] != key {
		return false
	}
	n, err := strconv.ParseInt(f[1], 10, 64)
	if err != nil {
		return false
	}
	a.State, a.Since, a.Last = state.Done, time.Unix(0, n), f[2]
	// A turn end decided by a screen that stayed at rest (a rewind, a turn
	// Claude owed and did not take) carries the moment the screen was first
	// seen at rest, which the restseen record of the same report holds too.
	r := strings.Fields(a.restSeen)
	a.rewound = len(r) == 3 && r[0] == key && r[2] == f[1]
	return true
}

// RestDelay is how long the screen of a working agent must stay at rest,
// unchanged, before hq takes the turn for one the user rewound (design
// §3.4). Claude at work redraws its spinner or its streaming reply several
// times a second; seen with Claude Code 2.1.283, its screen never stayed the
// same for more than about half a second.
const RestDelay = 2 * time.Second

// Rewound is the last message of a turn the user rewound: an early Esc
// that put the prompt back in the box, which Claude reports nowhere.
const Rewound = "Interrupted"

// Settle applies the screen of an Unsettled agent: a turn the user ended
// is done, since hq first saw it. Claude prints a line for most such turns
// (state.EndedByUser), whose last message it shows. An early Esc may
// rewind the turn instead, with no line and no hook: the working agent's
// screen is then at rest (state.AtRest), and once it has stayed so,
// unchanged, for RestDelay, the turn is done with Rewound as its last
// message, since hq first saw it at rest. The same goes for an agent at
// its prompt that Claude owes a turn, for background work that finished
// together with other work (state.Report.Owed): the turn comes at once, so
// a screen at rest for RestDelay (state.Idle) says it does not come, and
// the agent is done with its turn's message. Both moments are kept on the
// agent's window, keyed by the report they overrule, so hq ls and the list
// agree (see Recall): the record to store there, as the turnend or the
// restseen option, with no Value when there is nothing new to store.
func (a *Agent) Settle(screen string, now time.Time) Record {
	a.resting = false
	// A session that has just started may show a dialog of Claude's own
	// before its prompt box, which no hook reports (#29): the agent needs
	// input, for as long as the screen shows it. Nothing is stored: every
	// hq process reads it off the same screen.
	if a.atStart {
		if ask, ok := state.StartDialog(screen); ok {
			a.State, a.startAsk = state.NeedsInput, ask
			a.Last = ask.Questions[0].Question
		}
		return Record{}
	}
	if a.Recall() {
		return Record{}
	}
	key := a.key() + " "
	last := Rewound
	var rest, restored bool
	if a.owed {
		// Its turn ended by itself, and Claude owes it one more: at rest
		// whatever its box holds, the turn's message its last.
		rest, last = state.Idle(screen), a.Last
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
		if last, ok := ended(screen); ok && !rewound {
			a.State, a.Since, a.Last = state.Done, now, last
			return Record{"turnend", key, key + nanos(now) + " " + last}
		}
		if a.State != state.Working {
			return Record{}
		}
		rest, restored = state.AtRest(screen, a.prompt)
	}
	if !rest {
		return Record{}
	}
	look := fingerprint(screen)
	at, ok := a.restedSince(look)
	if !ok {
		a.resting = restored
		return Record{"restseen", key + look + " ", key + look + " " + nanos(now)}
	}
	if now.Sub(at) < RestDelay {
		a.resting = restored
		return Record{}
	}
	a.State, a.Since, a.Last, a.rewound = state.Done, at, last, true
	return Record{"turnend", key, key + nanos(at) + " " + last}
}

// Resting reports whether Settle saw the agent's screen at rest with its
// prompt put back in the box, the look of a turn the user rewound, but not
// yet for long enough to take it for one.
func (a Agent) Resting() bool { return a.resting }

// restedSince is when hq first saw the agent's screen at rest looking as it
// looks now (look), during its current report.
func (a Agent) restedSince(look string) (time.Time, bool) {
	f := strings.Fields(a.restSeen)
	if len(f) != 3 || f[0] != a.key() || f[1] != look {
		return time.Time{}, false
	}
	n, err := strconv.ParseInt(f[2], 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.Unix(0, n), true
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
