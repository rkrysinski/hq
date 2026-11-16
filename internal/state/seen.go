package state

import "slices"

// The reducer, part two (#197): what hq saw of an agent beyond its hook's
// reports, and what hq did to it, read beside the hook's state as the look
// reads its screen, its pane, sbx's answer and its home window (design
// §3.4, §5.1). Every decision is an event: a decision that takes time (a
// rewound turn, a turn Claude owed that does not come) is the event of the
// look that decided it, dated from the screen first seen at rest, and the
// time passed in for AGE never decides one.

// RewoundLast is the last message of a turn the user rewound: an early Esc
// that put the prompt back in the box, which Claude reports nowhere.
const RewoundLast = "Interrupted"

// EndedLast is the last message of an agent that ended without one: it
// never reported a message before its session went (spec S7, the mocks).
const EndedLast = "[session ended]"

// seen is what hq saw of the agent and did to it since its latest launch.
type seen struct {
	// launched is the latest launch or relaunch; zero when the journal
	// holds none, and then every report is of the agent's session.
	launched Ref
	// prompted says the latest launch gave the agent a first prompt
	// (Facts.Prompted).
	prompted bool
	// ends are the events that say the agent ended, in their order: its
	// pane's death, its sandbox not running (counted only while no report
	// is newer than the moment sbx was asked), a restart taking it down.
	ends []Event
	// The evidence of the screen, each as first seen for the hook's report
	// it was seen at, as the look keeps it on the home window: a turn the
	// user ended (by its line, rewound, or a turn Claude owed that did not
	// come), a screen at rest, Claude at work after a dialog its hooks
	// reported, a dialog of Claude's own (nil once gone), and Claude's
	// wake-up after a turn the user ended.
	turnEnd, atRest, atWork, own, woke *evidence
}

// evidence is an event of the screen with the number of the hook's report
// it was seen at (key); for a wake-up, the turn end it follows (after).
type evidence struct {
	Event
	key, after int64
}

// at says x was seen at the report key.
func (x *evidence) at(key int64) bool { return x != nil && x.key == key }

// see follows event e of hq's own (screen, tmux, sbx, hq), and reports
// whether it is one the reducer reads. Their kinds are unique across
// their sources, so the kind alone tells them.
func (f *Fold) see(e Event) bool {
	h := f.reading(nil)
	key := h.report.Number
	s := &f.seen
	mark := &evidence{Event: e, key: key}
	switch e.Kind {
	case KindTurnEnded, KindRewound, KindTurnNotTaken:
		// The first turn end seen for a report stands.
		if !s.turnEnd.at(key) {
			s.turnEnd = mark
		}
	case KindAtRest, KindToolCut:
		if !s.atRest.at(key) || s.atRest.Facts.Screen != e.Facts.Screen {
			s.atRest = mark
		}
	case KindAtWork:
		if !s.atWork.at(key) {
			s.atWork = mark
		}
	case KindOwnDialog:
		// The same dialog seen again, wherever its cursor is, is the one
		// first seen; another, or one opened again, counts anew.
		if !s.own.at(key) || s.own.Facts.Screen != e.Facts.Screen {
			s.own = mark
		}
	case KindOwnDialogGone:
		if s.own.at(key) {
			s.own = nil
		}
	case KindTookQueued:
		f.handOver(e)
	case KindWokeUp:
		if s.turnEnd != nil {
			s.woke = &evidence{Event: e, key: h.Entered.Number, after: s.turnEnd.Number}
		}
	case KindPaneDied, KindTakenDown:
		s.ends = append(s.ends, e)
	case KindNotRunning:
		// An answer that carries no moment asked ends nobody.
		if e.Facts.Asked.IsZero() {
			return false
		}
		s.ends = append(s.ends, e)
	case KindLaunched, KindRelaunched:
		b := f.seen
		b.ends = slices.Clip(b.ends)
		f.beforeLaunch = &b
		f.seen = seen{launched: RefOf(e), prompted: e.Facts.Prompted}
	case KindWithdrawn:
		if (e.Facts.Withdrawn != KindLaunched && e.Facts.Withdrawn != KindRelaunched) || f.beforeLaunch == nil {
			return false
		}
		f.seen, f.beforeLaunch = *f.beforeLaunch, nil
	default:
		return false
	}
	return true
}

// handOver follows Claude at work on the prompt it took from its queue
// after the user interrupted the turn it was queued in, which no hook
// reports (#88): the state is handed over to it, so the queued prompt
// becomes the latest report, dated by the event, and the kept event goes,
// as does the mark of a turn acted on that is the queued prompt's. With
// no prompt queued (the hook took it away first) nothing is handed over.
func (f *Fold) handOver(e Event) {
	if f.queued == nil {
		return
	}
	q := *f.queued
	f.report, f.beside, f.queued = &written{q, RefOf(e)}, nil, nil
	if f.acted != nil && f.acted.Facts.Turn == q.Facts.Turn {
		f.acted = nil
	}
}

// looked is the agent's state as the look gives it, and the event of the
// screen that decided the turn Claude owed does not come, if its state is
// that decision (KindTurnNotTaken; 0 otherwise): it completes the turn
// whose end the hook reported and kept working for background work.
type looked struct {
	Reduced
	notTaken int64
}

// look is the agent's state from the hook's (reading, with reply the
// journal's reply of its latest turn end) and what hq saw and did, as the
// look gives it (agent.Look): a report from before the agent's latest
// launch is its previous session's, which gives the last message, branch,
// worktree and prompt but not the state; an agent whose session reported
// its end, whose pane died, whose sandbox sbx said is not running, or that
// a restart takes down has ended (ended); while its session runs, a turn
// end its screen showed, Claude at work after a dialog its hooks reported,
// Claude's wake-up after a turn the user ended and a dialog of Claude's
// own overrule its hooks (overrule).
func (f *Fold) look(reply *Reply) looked {
	h := f.reading(reply)
	s := f.seen
	session := f.session(h)
	r := looked{Reduced: h}
	if !session {
		r.Reduced = Reduced{State: Starting, Since: s.launched.Time, Entered: s.launched, report: h.report}
		if h.Reported {
			// The previous session's report gives the session a relaunch
			// continues too.
			r.Last, r.Branch, r.Worktree, r.Prompt, r.Session, r.RestsOn = h.Last, h.Branch, h.Worktree, h.Prompt, h.Session, h.RestsOn
		}
		if s.launched.Number != 0 {
			r.restOn(s.launched.Number)
		}
	}
	switch {
	case f.ended(&r.Reduced, session):
	case session && r.State != Starting:
		f.overrule(&r, h)
	}
	if session && f.awaitsFirstPrompt(r.Reduced, h) {
		// Its new session waits for the first prompt hq gave it, which is
		// no turn done: starting from its launch until it reports it.
		r.State, r.Since, r.Entered = Starting, s.launched.Time, s.launched
		r.restOn(s.launched.Number)
	}
	if r.State != NeedsInput {
		r.Dialog = DialogVerdict{}
	}
	if (r.State != Done && r.State != Question) || !r.Since.Equal(h.Since) {
		// Own tasks are the turn end's the state comes from (#78): once hq
		// overrules it there are none.
		r.OwnTasks = nil
	}
	return r
}

// awaitsFirstPrompt says r, from the hook's report h, is the done of a new
// session's start of an agent launched with a first prompt, which the
// screen did not overrule (a dialog of Claude's own still needs input).
func (f *Fold) awaitsFirstPrompt(r, h Reduced) bool {
	c := f.report
	return f.seen.prompted && r.State == Done && r.Entered == h.Entered &&
		c.Kind == KindSessionStart && c.Facts.Start == newSource
}

// ended applies the end of the agent to r, and reports whether it ended:
// its session's end report, dated by it; else the first of its pane's
// death, its sandbox not running and a restart taking it down. sbx's
// answer counts only for an agent of whose session a report came, none of
// them newer than the moment sbx was asked: its sandbox may still be
// starting, or ran after it (#48). The pane's death counts from when tmux
// dates it, after the moment the state counted from; anything else that
// ends the agent from the event, never before its latest report. An ended
// agent keeps the line of a turn the user ended that its screen showed
// for its latest report, and one without a last message says it ended.
func (f *Fold) ended(r *Reduced, session bool) bool {
	base := r.Since
	if session {
		base = r.report.Time
	}
	switch {
	case session && r.State == Ended:
	default:
		var by *Event
		for i, e := range f.seen.ends {
			if e.Kind == KindNotRunning && (!session || r.report.Time.After(e.Facts.Asked)) {
				continue
			}
			by = &f.seen.ends[i]
			break
		}
		if by == nil {
			return false
		}
		r.State, r.Entered = Ended, RefOf(*by)
		switch from := by.Facts.From; {
		case by.Kind == KindPaneDied && !from.IsZero():
			r.Since = laterOf(r.Since, from)
		default:
			r.Since = laterOf(base, by.Time)
		}
		r.restOn(by.Number)
	}
	if te := f.seen.turnEnd; session && te.at(r.report.Number) && te.Kind != KindTurnNotTaken {
		r.Last = turnEndLast(te.Event, r.Last)
		r.restOn(te.Number)
	}
	if r.Last == "" {
		r.Last = EndedLast
	}
	return true
}

// turnEndLast is the last message of the turn end e the screen showed:
// its line, Interrupted for a rewind, and last, the turn's own, for a turn
// Claude owed that did not come.
func turnEndLast(e Event, last string) string {
	switch e.Kind {
	case KindTurnEnded:
		return e.Facts.Text
	case KindRewound:
		return RewoundLast
	}
	return last
}

// overrule applies to r what the screen showed for the hook's report h of
// a session that runs, which overrules the hooks: Claude's wake-up after a turn the
// user ended dates the work since; a turn end the screen showed makes the
// agent done, since it was seen (since the screen was first seen at rest,
// for a decision that took time), with its line; else Claude at work after
// a dialog its hooks reported, its own and not a subagent's, closes it:
// the agent works, since the dialog's report, with its turn's last
// message. An agent that then waits at its prompt (done, with a question,
// or working only on background work Claude owes it no turn for) is at a
// dialog of Claude's own while the screen shows one, since it was first
// seen, with its title as the last message.
func (f *Fold) overrule(r *looked, h Reduced) {
	s := f.seen
	key := h.report.Number
	if te := s.turnEnd; r.State == Working && te != nil && !te.at(key) && te.key >= h.Entered.Number && te.key < key {
		// The hooks know nothing of the turn the user ended: their report
		// counts from its prompt, and the agent works since Claude woke it.
		switch w := s.woke; {
		case w != nil && w.key == h.Entered.Number && w.after == te.Number:
			r.Since, r.Entered = w.Facts.From, RefOf(w.Event)
			if f.wakeUp.Time.Equal(w.Facts.From) {
				r.Entered = f.wakeUp
			}
			r.restOn(te.Number, w.Number)
		case h.Woken:
			r.Since, r.Entered = h.Latest, h.report
			r.restOn(te.Number)
		}
	}
	switch te, w := s.turnEnd, s.atWork; {
	case te.at(key):
		r.State, r.Last, r.Entered = Done, turnEndLast(te.Event, r.Last), RefOf(te.Event)
		if te.Kind == KindTurnNotTaken {
			r.notTaken = te.Number
		}
		rests := []int64{te.Number}
		r.Since = te.Time
		if te.Kind != KindTurnEnded {
			r.Since = te.Facts.From
			if s.atRest.at(key) && s.atRest.Facts.Screen == te.Facts.Screen {
				rests = append(rests, s.atRest.Number)
			}
		}
		r.restOn(rests...)
	case r.State == NeedsInput && r.Dialog.Whose == DialogHooks && w.at(key):
		r.State, r.Last, r.Entered = Working, h.LastStop, RefOf(w.Event)
		r.restOn(w.Number)
	}
	waits := r.State == Done || r.State == Question || r.State == Working && r.Background && !r.Owed
	if o := s.own; waits && o.at(key) {
		at := RefOf(o.Event)
		r.State, r.Since, r.Last, r.Entered = NeedsInput, o.Time, o.Facts.Text, at
		r.Dialog = DialogVerdict{Open: true, Whose: DialogOwn, Opened: at, Screen: o.Facts.Screen, RestsOn: []int64{o.Number}}
		r.restOn(o.Number)
	}
}
