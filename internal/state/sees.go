package state

import (
	"hash/fnv"
	"strconv"
	"time"
)

// What a look appends (#198): the look at an agent ingests its event
// files, folds its journal, and appends as events what it sees that the
// journal does not hold yet: the pane's death, sbx's answer that the
// sandbox does not run, what the screen shows that no hook reports, and
// Claude's wake-up after a turn the user ended. Which evidence it appends
// depends on the state folded so far, so the decisions live here, beside
// the fold that reads them back (seen.go); the screen rules themselves
// (screen.go, owndialog.go) decide what the screen shows.

// OverruleDelay is how old the hooks' report must be before the agent's
// screen can overrule it: Claude draws and runs its hooks independently,
// so for a moment the screen may lag behind the report.
const OverruleDelay = 500 * time.Millisecond

// RestDelay is how long the screen of a working agent must stay at rest,
// unchanged, before hq takes the turn for one the user rewound (design
// §3.4). Claude at work redraws its spinner or its streaming reply several
// times a second; seen with Claude Code 2.1.283, its screen never stayed
// the same for more than about half a second.
const RestDelay = 2 * time.Second

// Fingerprint tells screens apart without keeping them.
func Fingerprint(screen string) string {
	h := fnv.New64a()
	h.Write([]byte(screen))
	return strconv.FormatUint(h.Sum64(), 16)
}

// OwnFingerprint names a dialog Claude shows of its own on the agent's
// screen, ask, by its title and text: not by its options, which a list
// such as the picker of /model scrolls as the cursor moves.
func OwnFingerprint(ask *Ask) string {
	if ask == nil || len(ask.Questions) == 0 {
		return ""
	}
	return Fingerprint(ask.Questions[0].Question + "\n" + ask.Description)
}

// session reports whether the hook's report h is of the agent's session
// since its latest launch, the one whose state counts.
func (f *Fold) session(h Reduced) bool {
	return h.Reported && (f.seen.launched.Time.IsZero() || !h.Since.Before(f.seen.launched.Time))
}

// NotRunningEnds reports whether sbx's answer that the agent's sandbox
// does not run, asked at asked, ends the agent whose session since its
// latest launch is in state r: that session has reported, but not since
// sbx was asked (#48). Before its first report its session may still wait
// for sbx run to start the sandbox (S2); a report after the question
// proves the sandbox ran after it: the answer was taken while the sandbox
// started, and the next one decides.
func (r Reduced) NotRunningEnds(asked time.Time) bool {
	return r.Reported && !r.Latest.After(asked)
}

// Stopped reports whether sbx's answer that the agent's sandbox does not
// run, asked at asked, ends it (NotRunningEnds). The look appends that
// answer only then.
func (f Fold) Stopped(asked time.Time) bool {
	h := f.reading(nil)
	return !asked.IsZero() && f.session(h) && h.NotRunningEnds(asked)
}

// TakenDown reports whether hq takes the agent down since its latest
// launch (a restart, before its sandbox stops).
func (f Fold) TakenDown() bool {
	for _, e := range f.seen.ends {
		if e.Kind == KindTakenDown {
			return true
		}
	}
	return false
}

// ScreenMayTell reports whether the screen of the agent, its pane alive,
// may show at now what no hook reports (design §3.4): its session runs and
// has reported, OverruleDelay or more before now, that it works, so the
// user may have ended its turn; that it needs input, so the user may have
// ended its turn or answered a dialog no hook reports the end of; or that
// it waits at its prompt, done, with a question, or working only on
// background work, where Claude may show a dialog of its own (Sees); or
// that it started and waits for the first prompt hq launched it with,
// where Claude may show one in that prompt's way.
func (f Fold) ScreenMayTell(now time.Time) bool {
	h := f.reading(nil)
	r := f.look(nil)
	waits := r.State != Starting || f.firstPromptDue()
	return f.session(h) && waits && r.State != Ended && now.Sub(h.Latest) >= OverruleDelay
}

// Sees is what a look at now appends of the screen of an agent, when that
// screen may tell (ScreenMayTell), and whether it is at rest in a way one
// more look, RestDelay later, decides (resting).
//
// A dialog its hooks reported, its own and not a subagent's, closes when
// the screen shows Claude at work (KindAtWork): an MCP elicitation's
// answer, which no hook reports, or a tool's end Claude reports only when
// it ends (#70). A turn the user ended is done since it was first seen:
// Claude prints a line for most such turns (EndedByUser, KindTurnEnded).
// An early Esc may rewind the turn instead, with no line and no hook: the
// working agent's screen is then at rest (AtRest, KindAtRest), and once it
// has stayed so, unchanged, for RestDelay, the turn is done since it was
// first seen at rest (KindRewound). Not once Claude has acted on the turn,
// which no early Esc rewinds: a tool of its own ended (#77), or a dialog
// of its hooks' the screen saw answered, whose bare box closes no dialog
// of theirs (#70); then a screen at rest ends the turn only at a tool the
// user cut short (ToolCut, KindToolCut). The same goes for an agent at its
// prompt that Claude owes a turn for background work that finished
// together with other work: the turn comes at once, so a screen at rest
// for RestDelay (Idle) says it does not come (KindTurnNotTaken). An agent
// that waits at its prompt may show a dialog of Claude's own instead
// (ViewOwnDialog, KindOwnDialog), which closes when the screen no longer
// shows it (KindOwnDialogGone). Claude at work below the line of a turn
// the user ended, with a prompt of the user's queued, is Claude on that
// prompt (TookQueued, KindTookQueued, #88): no end, and the hook's state
// is handed over to the prompt.
func (f Fold) Sees(screen string, now time.Time) (events []Event, resting bool) {
	h := f.reading(nil)
	key := h.report.Number
	s := f.seen
	if h.Queued && !h.Woken && h.State == Working && TookQueued(screen, h.Prompt) {
		return []Event{screenEvent(KindTookQueued, now)}, false
	}
	st := h.State
	if s.turnEnd.at(key) {
		st = Done
	}
	if st == Done || st == Question || st == Working && h.Background && !h.Owed {
		return append(f.seesDialog(screen, key, now), f.seesSlashCommand(screen, key, now)...), false
	}
	hooks := h.State == NeedsInput && h.Dialog.Whose == DialogHooks
	answered := hooks && s.atWork.at(key)
	if answered {
		st = Working
	}
	seenKind, decision := KindAtRest, KindRewound
	var rest, again bool
	if h.Background && h.Owed {
		// Its turn ended by itself, and Claude owes it one more: at rest
		// whatever its box holds. One more look, RestDelay later, tells.
		rest, again, decision = Idle(screen), true, KindTurnNotTaken
	} else {
		// A rewind right after a turn the user ended leaves that turn's
		// line above the box: the rewind below tells that turn's end.
		rewound := st == Working && PutBack(screen, h.Prompt)
		if said, ok := EndedByUser(screen); ok && !rewound {
			e := screenEvent(KindTurnEnded, now)
			e.Facts.Text = said
			return []Event{e}, false
		}
		if st == NeedsInput {
			if hooks && AtWork(screen) {
				return []Event{screenEvent(KindAtWork, now)}, false
			}
			return nil, false
		}
		if st != Working {
			return nil, false
		}
		rest, again = AtRest(screen, h.Prompt)
		if h.Acted || answered {
			rest, seenKind = rest && ToolCut(screen), KindToolCut
		}
	}
	if !rest {
		return nil, false
	}
	fp := Fingerprint(screen)
	if !s.atRest.at(key) || s.atRest.Facts.Screen != fp {
		e := screenEvent(seenKind, now)
		e.Facts.Screen = fp
		return []Event{e}, again
	}
	at := s.atRest.Time
	if now.Sub(at) < RestDelay {
		return nil, again
	}
	e := screenEvent(decision, now)
	e.Facts.Screen, e.Facts.From = fp, at
	return []Event{e}, false
}

// seesDialog is what a look at now appends of the screen of an agent that
// waits at its prompt, its report key: a dialog of Claude's own in place
// of its prompt box, which no hook reports, at the start of its session
// (#29) or opened after a turn, as by /effort (#66), unless the journal
// holds it seen for this report; or, with none on the screen, that the one
// seen is gone.
func (f Fold) seesDialog(screen string, key int64, now time.Time) []Event {
	own := f.seen.own
	v, ok := ViewOwnDialog(screen)
	if !ok {
		if own.at(key) {
			return []Event{screenEvent(KindOwnDialogGone, now)}
		}
		return nil
	}
	fp := OwnFingerprint(v.Ask)
	if own.at(key) && own.Facts.Screen == fp {
		return nil
	}
	e := screenEvent(KindOwnDialog, now)
	e.Facts.Screen, e.Facts.Text = fp, v.Ask.Questions[0].Question
	return []Event{e}
}

// seesSlashCommand is what a look at now appends of the screen of an agent
// waiting for the first prompt hq launched it with, its report key: that
// Claude took that prompt as a slash command of its own that starts no
// turn, which it reports nowhere (TookSlashCommand, #220), unless the
// journal holds it seen for this report. A screen that still shows a
// dialog of Claude's own does not show it.
func (f Fold) seesSlashCommand(screen string, key int64, now time.Time) []Event {
	if !f.firstPromptDue() || f.seen.tookSlash.at(key) || !TookSlashCommand(screen) {
		return nil
	}
	return []Event{screenEvent(KindTookSlashCommand, now)}
}

// WakeUp is Claude's wake-up after a turn the user ended, for a look to
// append when its journal does not hold it: the agent's hooks know nothing
// of that end, so their report still counts from the prompt of the turn
// the user ended, and the agent works since the report that is the
// wake-up, which the reports after it, of the same work, no longer tell
// (design §3.4). ok is false when the agent is not woken so, its pane is
// gone or hq takes it down, or the journal holds it.
func (f Fold) WakeUp(alive bool) (e Event, ok bool) {
	h := f.reading(nil)
	s := f.seen
	key := h.report.Number
	te := s.turnEnd
	if !alive || f.TakenDown() || !f.session(h) || h.State != Working || !h.Woken ||
		te == nil || te.at(key) || te.key < h.Entered.Number || te.key >= key {
		return Event{}, false
	}
	if w := s.woke; w != nil && w.key == h.Entered.Number && w.after == te.Number {
		return Event{}, false
	}
	e = screenEvent(KindWokeUp, time.Time{})
	e.Facts.From = h.Latest
	return e, true
}

// screenEvent is an event of what a look saw on the screen at now.
func screenEvent(kind string, now time.Time) Event {
	return Event{Source: SourceScreen, Kind: kind, Time: now}
}

// LastReplyNumber is the number of the latest turn end, whose reply
// LastReply gives: 0 before the first.
func (f Fold) LastReplyNumber() int64 {
	if f.stop == nil {
		return 0
	}
	return f.stop.Number
}

// LastReply is the latest turn end's reply in full, as hq read shows it,
// with reply the journal's reply of that turn end, and when it was given:
// TooLarge for a turn end too large to read (#64), FailedLast for a turn
// an API error ended without a line; "" and the zero time before the
// first turn end.
func (f Fold) LastReply(reply Reply) (text string, at time.Time) {
	switch e := f.stop; {
	case e == nil:
		return "", time.Time{}
	case e.Kind == KindUnreadable && e.Facts.Size > maxFile:
		return TooLarge(e.Facts.Size), e.Time
	case !EndsTurn(e.Kind):
		return "", e.Time
	case reply.Number == e.Number:
		return orFailed(e, CleanText(reply.Text)), e.Time
	default:
		return orFailed(e, ""), e.Time
	}
}
