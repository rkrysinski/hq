//go:build integration

package state

import (
	"errors"
	"os"
	"path/filepath"
	"time"
)

// reported is the state of agent id of the repository at root as hq on the
// host gives it from the event files its hook left, and nothing else (ADR
// 0018): each read as the journal's ingest reads it (hookEvents), folded by
// the reducer with the latest turn end's reply. ok is false until the
// hook's first report. The tests of the hook read what it reported so.
func reported(root, id string) (r Report, ok bool) {
	events, reply := hookEvents(root, id)
	s := Reduce(events, reply, time.Now())
	return Report{
		State: s.State, Since: s.Since, Last: s.Last, LastStop: s.LastStop,
		SessionID: s.Session, Cwd: s.Worktree, Branch: s.Branch, Prompt: s.Prompt, Latest: s.Latest,
		Background: s.Background, Owed: s.Owed, Woken: s.Woken, Acted: s.Acted, Queued: s.Queued,
		SubagentDialog: s.Dialog.Open && s.Dialog.Whose == DialogSubagent, OwnTasks: s.OwnTasks,
	}, s.Reported
}

// detailed is the last reply of agent id of the repository at root in
// full, with when it was given, as hq read gives it from the event files
// its hook left.
func detailed(root, id string) Detail {
	events, reply := hookEvents(root, id)
	text, at := Fold{}.With(events...).LastReply(reply)
	return Detail{Reply: text, ReplyAt: at}
}

// asked is what the journal keeps of the latest dialog among the event
// files the hook of agent id of the repository at root left, as its ingest
// keeps it (AskedOf, Asked.Answer); ok is false before any.
func asked(root, id string) (a Asked, ok bool) {
	eachEvent(root, id, func(e Event, _ *Reply, data []byte) {
		if d, dialog := AskedOf(e, data); dialog {
			a, ok = d, true
		} else {
			a.Answer(e, data)
		}
	})
	return a, ok
}

// askedQuestions is the question dialog of agent id of the repository at root
// as hq answer reads it from the journal.
func askedQuestions(root, id string) (Questions, error) {
	a, _ := asked(root, id)
	if a.Report == "" {
		return Questions{}, errors.New("no question dialog of the agent's own is open")
	}
	return ParseQuestions([]byte(a.Report))
}

// askedAnswers is what Claude reports it got for the question dialog of agent
// id of the repository at root, as hq answer reads it from the journal;
// ok is false until the dialog's tool ended.
func askedAnswers(root, id string) (map[string]string, bool) {
	a, _ := asked(root, id)
	return a.Answers, a.Answered
}

// hookEvents are the events of the event files the hook of agent id of the
// repository at root left, in the order of their names, numbered from one,
// and the reply of the latest turn end among them (eachEvent).
func hookEvents(root, id string) (events []Event, reply Reply) {
	eachEvent(root, id, func(e Event, r *Reply, _ []byte) {
		e.Number = int64(len(events) + 1)
		if r != nil {
			r.Number = e.Number
			reply = *r
		}
		events = append(events, e)
	})
	return events, reply
}

// eachEvent calls do with the event of each event file the hook of agent
// id of the repository at root left, in the order of their names, with its
// reply and the file's content: each file read as the journal's ingest
// reads it, a regular file of at most 16 MiB never read through a link,
// and one it cannot read an event that says so.
func eachEvent(root, id string, do func(e Event, r *Reply, data []byte)) {
	dir := EventsDir(root, id)
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		name := entry.Name()
		if !IsEventFile(name) || entry.IsDir() {
			continue
		}
		path := filepath.Join(dir, name)
		info, err := os.Lstat(path)
		if err != nil {
			continue
		}
		var e Event
		var r *Reply
		data, read, err := ReadFile(path)
		switch big, tooLarge := TooLargeSize(err); {
		case tooLarge:
			e = UnreadableEvent(name, big, info.ModTime())
		case err != nil:
			e = UnreadableEvent(name, info.Size(), info.ModTime())
		default:
			e, r = ParseEvent(name, data, read.ModTime())
		}
		do(e, r, data)
	}
}
