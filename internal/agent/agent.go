// Package agent is hq's row model: one agent as hq ls and, later, the
// dashboard show it (design §3.8, §6).
package agent

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/rkrysinski/hq/internal/sbx"
	"github.com/rkrysinski/hq/internal/state"
	"github.com/rkrysinski/hq/internal/tmux"
	"github.com/rkrysinski/hq/internal/version"
)

// Agent is one agent: its home window, and its state as the reducer folds
// it from the agent's journal (design §3.4, ADR 0018).
type Agent struct {
	Window     string    `json:"-"`
	Pane       string    `json:"-"` // the agent's own pane
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	RepoPath   string    `json:"repo_path"`
	Sandbox    string    `json:"sandbox"`
	Started    time.Time `json:"started"`
	Alive      bool      `json:"-"` // the agent's pane's process runs
	Docked     bool      `json:"-"` // its pane is in the dashboard's slot
	New        bool      `json:"-"` // started with hq new, not yet reported (S2)
	Generation int       `json:"-"` // the generation of the hq that last launched it, as stored on its window (see Outdated)
	deadAt     time.Time // when its pane died, as tmux says; zero when unknown
	// reduced is its state as the reducer folded it from its journal, and
	// journaled says the journal could be read; an agent whose journal
	// cannot be read is starting, or ended when its pane is dead.
	reduced   state.Reduced
	journaled bool
	// journalGone says the look found its journal gone (JournalGone).
	journalGone bool
	// through is what the counter held when its journal was last read
	// (journal.Journal.Through): every event of any agent numbered up to it
	// was in its journal then; 0 when it was not read.
	through int64
	// fold, reply and ask are the journal folded, its reply of the latest
	// turn end and what the open dialog of its hooks asks, for Detail.
	fold      state.Fold
	reply     state.Reply
	ask       *state.Ask
	resting   bool       // its screen is at rest, not yet for long enough (see Resting)
	ownAsk    *state.Ask // what the dialog of Claude's own it is at asks, as this look saw it on its screen
	ownScroll bool       // that dialog's list scrolls to options the screen does not show (state.OwnView)
	State     string     `json:"state"`
	Since     time.Time  `json:"since"`
	Branch    string     `json:"branch"`
	Worktree  string     `json:"worktree"` // where Claude works, as the sandbox sees it (for hq code, M4)
	Last      string     `json:"last"`

	// OwnTasks are its own tasks (#78): the shell commands and watches it
	// left running itself at the turn end its done or question comes from,
	// as its hooks reported them; none in any other state.
	OwnTasks []state.Task `json:"-"`
}

// Outdated reports whether the agent is an outdated agent: its home window
// carries a generation that is not this hq's (version.Generation), so hq
// cannot read its state (FromWindows). It is listed as outdated, whatever
// its pane does, until it is killed, and can only be opened or killed.
func (a Agent) Outdated() bool { return a.State == state.Outdated }

// StartedBy says which hq started an agent of generation gen, of another
// generation than this hq's: an older one, or a newer one after a
// downgrade.
func StartedBy(gen int) string {
	if gen > version.Generation {
		return "started by a newer hq"
	}
	return "started by an older hq"
}

// OutdatedLast is the last message of an outdated agent of generation gen,
// which names the remedy.
func OutdatedLast(gen int) string { return StartedBy(gen) + ": kill it and start it again" }

// OutdatedErr is the line every command but those that open or kill an
// agent refuses an outdated agent with: its name and its last message.
func (a Agent) OutdatedErr() string { return a.Name + " was " + OutdatedLast(a.Generation) }

// ofGeneration gives a its generation gen: of another than this hq's, it
// is outdated, and nothing of its own is read: its state, its age and its
// branch are unknown to this hq (see FromJournals).
func (a *Agent) ofGeneration(gen int) {
	a.Generation = gen
	if gen != version.Generation {
		a.State, a.Last = state.Outdated, OutdatedLast(gen)
	}
}

// Lost is the agent of the record r in the repository at root, which no
// window of this tmux server has (#140): of another generation than this
// hq's, it is outdated, as one with a window is (FromWindows).
func Lost(r state.Record, root string) Agent {
	a := Agent{ID: r.ID, Name: r.Name, RepoPath: root, Sandbox: r.Sandbox}
	a.ofGeneration(r.Generation)
	return a
}

// Repo is the repository's display name.
func (a Agent) Repo() string { return filepath.Base(a.RepoPath) }

// Setup is the setup its sandbox was created for, read off the sandbox's
// name (#135), so it stays when the setup leaves the preferences.
func (a Agent) Setup() string { return sbx.SetupOf(a.Sandbox, a.Repo()) }

var nameRE = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// MaxName is the longest agent name, in characters (spec §4.2).
const MaxName = 32

// NameChars reports whether name is made of the characters a name may have:
// letters, digits, -, _ and . (spec §4.2), and has at least one.
func NameChars(name string) bool { return nameRE.MatchString(name) }

var letterOrDigitRE = regexp.MustCompile(`[A-Za-z0-9]`)

// NameHasLetterOrDigit reports whether name has a letter or a digit, which
// a name needs: dots, dashes and underscores alone make none (spec §4.2).
func NameHasLetterOrDigit(name string) bool { return letterOrDigitRE.MatchString(name) }

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
// relaunch from one of the relaunched session.
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

// GenerationOption is the user option of a home window that holds the
// generation of the hq that last launched the agent (version.Generation).
// It is named inbox since hq marked with it the agents whose hooks deliver
// the messages hq send leaves (ADR 0012), counting up from 1.
const GenerationOption = "inbox"

// FromWindows returns the agents among hq's windows, in window order.
// Windows without an hq id (the dashboard's own window) are not agents. An
// agent of another generation is outdated, with OutdatedLast as its last
// message.
func FromWindows(ws []tmux.Window) []Agent {
	var as []Agent
	for _, w := range ws {
		o := w.Options
		if o["id"] == "" {
			continue
		}
		a := Agent{Window: w.ID, Pane: w.Pane, ID: o["id"], Name: o["name"], RepoPath: o["repo"], Sandbox: o["sandbox"], Alive: !w.PaneDead, Docked: w.Docked, State: state.Starting, deadAt: w.DeadAt}
		if a.Name == "" {
			a.Name = w.Name
		}
		if t, ok := ParseStamp(o["started"]); ok {
			a.Started = t
		}
		a.Since = a.Started
		// A window marked ending belongs to an agent hq is taking down (its
		// sandbox restarting): ended already, though its pane still runs.
		// This is tmux's word alone, for what does not read the agent's
		// journal; the journal decides once it is read (FromJournals, Look).
		if w.PaneDead || o["ending"] != "" {
			a.State = state.Ended
		}
		gen, _ := strconv.Atoi(o[GenerationOption])
		a.ofGeneration(gen)
		a.New = o["new"] != "" && a.State == state.Starting
		as = append(as, a)
	}
	return as
}

// EndedLast is the last message of an agent that ended without one: it
// never reported a message before its session went (spec S7, the mocks).
const EndedLast = state.EndedLast

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

// SandboxesOf is sbx's answer all, the sandboxes sbx lists, asked at asked.
func SandboxesOf(all []sbx.Sandbox, asked time.Time) Sandboxes {
	running := map[string]bool{}
	for _, s := range all {
		running[s.Name] = s.Running()
	}
	return Sandboxes{Running: running, Asked: asked}
}

// stopped reports whether the answer ends a: its sandbox does not run, by
// the rule of #48 (state.Reduced.NotRunningEnds).
func (s Sandboxes) stopped(a Agent) bool {
	return s.notRunning(a) && a.reduced.NotRunningEnds(s.Asked)
}

// notRunning reports whether sbx said that a's sandbox does not run: listed
// as not running, or not listed at all.
func (s Sandboxes) notRunning(a Agent) bool {
	return s.Running != nil && !s.Running[a.Sandbox]
}

// Ends reports whether the answer leaves nothing of a to end in its
// sandbox: the sandbox does not run (listed as not running, or not listed
// at all) and a's session went with it, its pane dead or, while the pane
// still runs, ended by the list's rule (stopped) or already ended. A
// session ending as its sandbox stops reports its end, which may come
// after the answer: that report is no sign that the sandbox ran after it.
// sbx exec would start the sandbox again (#101), so hq kill leaves such a
// sandbox alone (#87). When sbx could not say, nothing is known to have
// ended.
func (s Sandboxes) Ends(a Agent) bool {
	return s.notRunning(a) && (!a.Alive || s.stopped(a) || a.State == state.Ended)
}

// apply gives the agent the state the reducer folds from its journal, f,
// at now, and what this look saw of a dialog of Claude's own on its
// screen, if it saw it (screen, captured). An agent with no event yet
// counts from its start.
func (a *Agent) apply(f *folded, now time.Time, screen string, captured bool) {
	r := f.fold.Result(f.reply, now)
	a.reduced, a.journaled, a.fold, a.reply, a.through = r, true, f.fold, f.reply, f.through
	a.journalGone = f.gone
	a.State, a.Since, a.Last, a.Branch, a.Worktree, a.OwnTasks = r.State, r.Since, r.Last, r.Branch, r.Worktree, r.OwnTasks
	if a.Since.IsZero() {
		a.Since = a.Started
	}
	a.New = a.New && a.State == state.Starting
	v := r.Dialog
	if a.State != state.NeedsInput || !v.Open {
		return
	}
	if v.Whose != state.DialogOwn && f.ask.Number == v.Opened.Number {
		a.ask = f.ask.Ask
	}
	if own, ok := state.ViewOwnDialog(screen); ok && captured && v.Whose == state.DialogOwn && state.OwnFingerprint(own.Ask) == v.Screen {
		a.ownAsk, a.ownScroll = own.Ask, own.Scrolls
	}
}

// Detail is what hq read shows of the agent beyond its row (spec §4.1):
// its last reply in full, as its journal keeps it of its latest turn end,
// with when it was given, and what the open dialog of its hooks asks, as
// the journal keeps it of the dialog's report.
func (a Agent) Detail() state.Detail {
	text, at := a.fold.LastReply(a.reply)
	return state.Detail{Reply: text, Ask: a.ask, ReplyAt: at, ReplyNumber: a.fold.LastReplyNumber()}
}

// Session is the Claude session of the agent's latest report, as its
// journal gives it: the one hq sandbox restart continues; "" before any.
func (a Agent) Session() string { return a.reduced.Session }

// unread is the agent as its window alone says, for one whose journal
// could not be read: starting, or ended when its pane is dead, saying it
// ended.
func (a *Agent) unread() {
	if a.State == state.Ended && a.Last == "" {
		a.Last = EndedLast
	}
}

// JournalGone reports whether the look found the agent's journal gone:
// holding nothing when it read it, or removed while it looked. That is an
// agent whose launch is not in its journal yet, one whose journal was
// lost, or one hq kill is removing: hq kill kills an agent's home window
// before it removes its journal, so a look can list the window and find
// the journal gone after (#239).
func (a Agent) JournalGone() bool { return a.journalGone }

// StateSeq is the number of the event at which the agent's state, or the
// dialog it is at, last changed (state_seq, ADR 0019): for an agent starting
// from its launch, whose state has not changed since, the event it entered
// it at, the launch. ok is false for an agent with no event behind its
// state: outdated, one whose journal could not be read, one starting before
// its journal holds an event, and one gone (Gone).
func (a Agent) StateSeq() (n int64, ok bool) {
	n = a.reduced.Changed.Number
	if n == 0 {
		n = a.reduced.Entered.Number
	}
	return n, a.journaled && !a.Outdated() && n > 0
}

// CompletionSeq is the number of the event that ended the agent's last
// completed turn (completion_seq, ADR 0019, state.Reduced.TurnEnded): a
// turn end its hook reported as done or question, or the screen's word that
// the turn Claude owed after one does not come; ok is false before the
// first, and for an agent with no StateSeq.
func (a Agent) CompletionSeq() (n int64, ok bool) {
	n = a.reduced.TurnEnded
	_, numbered := a.StateSeq()
	return n, numbered && n > 0
}

// Through is what the counter held when the look last read the agent's
// journal: every event of any agent numbered up to it was in its journal
// then; 0 when the look did not read it (an outdated agent, or a journal
// that cannot be read). The cursor of a look is built on it (ADR 0019).
func (a Agent) Through() int64 { return a.through }

// SeenThrough is the lowest counter among the look's reads of the agents'
// journals (Through), outdated agents aside: the look saw every change of
// theirs numbered up to it, whatever other processes numbered meanwhile
// (ADR 0019). ok is false when it vouches for nothing: no journal was
// read, or one could not be.
func SeenThrough(as []Agent) (n int64, ok bool) {
	for _, a := range as {
		if a.Outdated() {
			continue
		}
		if a.through == 0 {
			return 0, false
		}
		if !ok || a.through < n {
			n, ok = a.through, true
		}
	}
	return n, ok
}

// Gone is the agent, waited on at the last look, now gone from tmux
// (killed): ended at at, with what it last showed, and no numbers, since
// its journal went with it.
func (a Agent) Gone(at time.Time) Agent {
	a.State, a.Since, a.journaled, a.through = state.Ended, at, false, 0
	if a.Last == "" {
		a.Last = EndedLast
	}
	return a
}

// Entered is the moment from which any hq process can see the agent in its
// state: the time of the event that decided the state (a report of its
// hook, or what hq saw: a turn the user ended, a rewind decided RestDelay
// after its screen was first seen at rest, the pane's death), which may be
// later than Since, the moment AGE counts from. hq wait compares a moment
// given as --since with it (design §3.4, "Changes after a cursor"); a
// cursor is compared with StateSeq. Every process folds the same journal,
// so all agree on it; an agent with no event yet entered it at its start.
func (a Agent) Entered() time.Time {
	if t := a.reduced.Entered.Time; !t.IsZero() {
		return t
	}
	return a.Since
}

// OwnPrefix is what LAST starts with for an agent with own tasks, in hq ls
// and the list (#78): their count in the wording of Claude Code's footer
// and a dot, "1 shell · ", "2 shells · ", "1 shell, 1 monitor · "; "" for
// none.
func OwnPrefix(ts []state.Task) string {
	n := map[string]int{}
	for _, t := range ts {
		n[t.Type]++
	}
	var out []string
	for _, kind := range []string{state.ShellTask, state.MonitorTask} {
		switch n[kind] {
		case 0:
		case 1:
			out = append(out, "1 "+kind)
		default:
			out = append(out, strconv.Itoa(n[kind])+" "+kind+"s")
		}
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, ", ") + " · "
}

// OnBackgroundWork reports whether the agent works only on background work
// (subagents): its turn ended, and it sits at its prompt until Claude
// wakes it for the closing turn (spec §5). hq send types a message in
// there, as for an agent that is done.
func (a Agent) OnBackgroundWork() bool { return a.reduced.Background && a.State == state.Working }

// RestDelay is how long the screen of a working agent must stay at rest,
// unchanged, before hq takes the turn for one the user rewound (design
// §3.4, state.RestDelay).
const RestDelay = state.RestDelay

// Resting reports whether the look saw the agent's screen at rest in a
// way that one more look, RestDelay later, decides: with its prompt put
// back in the box, the look of a turn the user rewound, or owed a turn by
// Claude; but not yet for long enough (state.Fold.Sees).
func (a Agent) Resting() bool { return a.resting }

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
