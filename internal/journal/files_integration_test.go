//go:build integration

package journal_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/journal"
	"github.com/rkrysinski/hq/internal/journal/journaltest"
	"github.com/rkrysinski/hq/internal/state"
)

// TestMain lets the test binary run as an hq process that ingests:
// HQ_JOURNAL_INGEST names the store's directory, the repository and the
// agent; with HQ_JOURNAL_CRASH set the process kills itself once it has
// appended, before it removes the files. HQ_JOURNAL_APPEND, naming the
// store's directory and the agent, has it append a look's evidence.
func TestMain(m *testing.M) {
	if arg := os.Getenv("HQ_JOURNAL_APPEND"); arg != "" {
		// A look appending what it saw: a turn the user ended, based on
		// the agent's first report.
		f := strings.Split(arg, "\n")
		e := state.Event{Source: state.SourceScreen, Kind: state.KindTurnEnded, Time: time.Now(), Basis: 1}
		if _, err := (journal.Files{Dir: f[0]}).Append(f[1], e); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	if arg := os.Getenv("HQ_JOURNAL_INGEST"); arg != "" {
		f := strings.Split(arg, "\n")
		if os.Getenv("HQ_JOURNAL_CRASH") != "" {
			journal.KillAfterAppend()
		}
		if err := (journal.Files{Dir: f[0]}).Ingest(f[1], f[2]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// leave writes an event file as the hook does, dated at.
func leave(t *testing.T, root, id, name string, data []byte, at time.Time) {
	t.Helper()
	dir := state.EventsDir(root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

// left are the event files waiting for agent id, temporary ones included.
func left(t *testing.T, root, id string) []string {
	t.Helper()
	entries, _ := os.ReadDir(state.EventsDir(root, id))
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func newRoot(t *testing.T) string {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestFilesKeepTheContract(t *testing.T) {
	journaltest.Contract(t, func(t *testing.T) journaltest.Harness {
		return journaltest.Harness{
			Store: journal.Files{Dir: filepath.Join(t.TempDir(), "hq")},
			Leave: func(root, id, name string, data []byte, at time.Time) { leave(t, root, id, name, data, at) },
			Left:  func(root, id string) []string { return left(t, root, id) },
			Root:  func() string { return newRoot(t) },
		}
	})
}

var prompt = journaltest.Hook("main", map[string]any{"hook_event_name": "UserPromptSubmit", "prompt_id": "p1"})

// ingester starts the test binary as an hq process ingesting agent id.
func ingester(t *testing.T, dir, root, id string, crash bool) *exec.Cmd {
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), "HQ_JOURNAL_INGEST="+dir+"\n"+root+"\n"+id)
	if crash {
		cmd.Env = append(cmd.Env, "HQ_JOURNAL_CRASH=1")
	}
	return cmd
}

func TestEightProcessesIngestEachFileOnce(t *testing.T) {
	dir, root := filepath.Join(t.TempDir(), "hq"), newRoot(t)
	const files = 40
	for i := range files {
		leave(t, root, "a1", fmt.Sprintf("%09d.1", i+1), prompt, time.Now())
	}
	var cmds []*exec.Cmd
	for range 8 {
		cmd := ingester(t, dir, root, "a1", false)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		cmds = append(cmds, cmd)
	}
	for _, cmd := range cmds {
		if err := cmd.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	j, err := journal.Files{Dir: dir}.Read("a1")
	if err != nil {
		t.Fatal(err)
	}
	if len(j.Events) != files || len(left(t, root, "a1")) != 0 {
		t.Fatalf("%d events, left %v", len(j.Events), left(t, root, "a1"))
	}
	for i, e := range j.Events {
		if e.File != fmt.Sprintf("%09d.1", i+1) || e.Number != int64(i+1) {
			t.Errorf("event %d: %+v", i, e)
		}
	}
}

// Of eight hq processes appending the same evidence at once, one event
// stands and the others are set aside as duplicates (#195).
func TestOfEightProcessesAppendingTheSameEvidenceOneStands(t *testing.T) {
	dir, root := filepath.Join(t.TempDir(), "hq"), newRoot(t)
	leave(t, root, "a1", "000000001.1", prompt, time.Now())
	if err := (journal.Files{Dir: dir}).Ingest(root, "a1"); err != nil {
		t.Fatal(err)
	}
	var cmds []*exec.Cmd
	for range 8 {
		cmd := exec.Command(os.Args[0], "-test.run=^$")
		cmd.Env = append(os.Environ(), "HQ_JOURNAL_APPEND="+dir+"\na1")
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		cmds = append(cmds, cmd)
	}
	for _, cmd := range cmds {
		if err := cmd.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	j, err := journal.Files{Dir: dir}.Read("a1")
	if err != nil {
		t.Fatal(err)
	}
	var asides []string
	for _, e := range j.Events[1:] {
		asides = append(asides, e.Aside)
	}
	if len(j.Events) != 9 || strings.Join(asides, " ") != " duplicate duplicate duplicate duplicate duplicate duplicate duplicate" {
		t.Fatalf("%d events, set aside: %q", len(j.Events), asides)
	}
}

func TestAProcessKilledAfterItsAppendLeavesNoSecondCopy(t *testing.T) {
	dir, root := filepath.Join(t.TempDir(), "hq"), newRoot(t)
	for i := range 3 {
		leave(t, root, "a1", fmt.Sprintf("%09d.1", i+1), prompt, time.Now())
	}
	err := ingester(t, dir, root, "a1", true).Run()
	if ee, ok := err.(*exec.ExitError); !ok || ee.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
		t.Fatalf("the ingest was not killed: %v", err)
	}
	if got := left(t, root, "a1"); len(got) != 3 {
		t.Fatalf("the killed ingest removed files: %v", got)
	}
	leave(t, root, "a1", fmt.Sprintf("%09d.1", 4), prompt, time.Now())
	s := journal.Files{Dir: dir}
	if err := s.Ingest(root, "a1"); err != nil {
		t.Fatal(err)
	}
	j, _ := s.Read("a1")
	var got []string
	for _, e := range j.Events {
		got = append(got, fmt.Sprintf("%d:%s", e.Number, e.File))
	}
	if strings.Join(got, " ") != "1:000000001.1 2:000000002.1 3:000000003.1 4:000000004.1" || len(left(t, root, "a1")) != 0 {
		t.Fatalf("%v, left %v", got, left(t, root, "a1"))
	}
	if _, err := os.Stat(filepath.Join(dir, "journals", "a1.ingest")); !os.IsNotExist(err) {
		t.Errorf("the ingest under way is still recorded: %v", err)
	}
}

func TestAnAppendCutOffIsUndoneAndDoneAgain(t *testing.T) {
	dir, root := filepath.Join(t.TempDir(), "hq"), newRoot(t)
	s := journal.Files{Dir: dir}
	leave(t, root, "a1", "000000001.1", prompt, time.Now())
	if err := s.Ingest(root, "a1"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "journals", "a1")
	whole, _ := os.ReadFile(path)
	// A process died while it appended the next file: half a line, and the
	// record of what it was about.
	leave(t, root, "a1", "000000002.1", prompt, time.Now())
	if err := os.WriteFile(path, append(append([]byte{}, whole...), `{"n":2,"sou`...), 0o600); err != nil {
		t.Fatal(err)
	}
	pend := fmt.Sprintf(`{"from":%d,"to":%d,"files":["000000002.1"]}`, len(whole), len(whole)+200)
	if err := os.WriteFile(path+".ingest", []byte(pend), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Ingest(root, "a1"); err != nil {
		t.Fatal(err)
	}
	j, _ := s.Read("a1")
	if len(j.Events) != 2 || j.Events[1].File != "000000002.1" || j.Events[1].Number != 2 {
		t.Fatalf("%+v", j.Events)
	}
	// hq's own event after a line cut off by a process that died.
	data, _ := os.ReadFile(path)
	if err := os.WriteFile(path, append(data, `{"n":9,`...), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append("a1", state.Event{Source: state.SourceHQ, Kind: "typed"}); err != nil {
		t.Fatal(err)
	}
	if j, _ := s.Read("a1"); len(j.Events) != 3 || j.Events[2].Kind != "typed" {
		t.Fatalf("%+v", j.Events)
	}
	// A record no ingest could have written is dropped.
	if err := os.WriteFile(path+".ingest", []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	leave(t, root, "a1", "000000003.1", prompt, time.Now())
	if err := s.Ingest(root, "a1"); err != nil {
		t.Fatal(err)
	}
	if j, _ := s.Read("a1"); len(j.Events) != 4 {
		t.Fatalf("%+v", j.Events)
	}
}

func TestEveryEventUpToTheNumberReadIsInItsJournal(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "hq")
	ids := []string{"a1", "b2", "c3"}
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := journal.Files{Dir: dir}
			for range 60 {
				if _, err := s.Append(id, state.Event{Source: state.SourceHQ, Kind: "typed"}); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	type snapshot struct {
		through int64
		numbers map[int64]bool
	}
	var snaps []snapshot
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	s := journal.Files{Dir: dir}
	for running := true; running; {
		select {
		case <-done:
			running = false
		default:
		}
		j, err := s.Read("a1")
		if err != nil {
			t.Fatal(err)
		}
		snap := snapshot{through: j.Through, numbers: map[int64]bool{}}
		for _, e := range j.Events {
			snap.numbers[e.Number] = true
		}
		snaps = append(snaps, snap)
	}
	final, _ := s.Read("a1")
	if len(final.Events) != 60 {
		t.Fatalf("%d events", len(final.Events))
	}
	for _, snap := range snaps {
		for _, e := range final.Events {
			if e.Number <= snap.through && !snap.numbers[e.Number] {
				t.Fatalf("event %d is not in a journal read through %d", e.Number, snap.through)
			}
		}
	}
	if len(snaps) < 2 {
		t.Errorf("only %d reads", len(snaps))
	}
}

func TestTheCounterGoesOnInAnotherProcess(t *testing.T) {
	dir, root := filepath.Join(t.TempDir(), "hq"), newRoot(t)
	leave(t, root, "a1", "000000001.1", prompt, time.Now())
	if err := (journal.Files{Dir: dir}).Ingest(root, "a1"); err != nil {
		t.Fatal(err)
	}
	// A lost tmux server, a restore, another repository: a new process.
	other := newRoot(t)
	leave(t, other, "b2", "000000001.1", prompt, time.Now())
	if err := ingester(t, dir, other, "b2", false).Run(); err != nil {
		t.Fatal(err)
	}
	j, _ := journal.Files{Dir: dir}.Read("b2")
	if len(j.Events) != 1 || j.Events[0].Number != 2 || j.Through != 2 {
		t.Fatalf("%+v", j)
	}
	if err := os.WriteFile(filepath.Join(dir, "counter"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	leave(t, other, "b2", "000000002.1", prompt, time.Now())
	if err := (journal.Files{Dir: dir}).Ingest(other, "b2"); err == nil || len(left(t, other, "b2")) != 1 {
		t.Fatalf("a counter that holds no number numbered events: %v", err)
	}
}

func TestEventFilesAreReadAsStateFilesAre(t *testing.T) {
	dir, root := filepath.Join(t.TempDir(), "hq"), newRoot(t)
	events := state.EventsDir(root, "a1")
	secret := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secret, prompt, 0o600); err != nil {
		t.Fatal(err)
	}
	leave(t, root, "a1", "000000001.1", prompt, time.Now())
	if err := os.Symlink(secret, filepath.Join(events, "000000002.1")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(events, "000000003.1"), 0o644); err != nil {
		t.Fatal(err)
	}
	big := filepath.Join(events, "000000004.1")
	if err := os.WriteFile(big, nil, 0o644); err != nil || os.Truncate(big, 16<<20+1) != nil {
		t.Fatal(err)
	}
	leave(t, root, "a1", "000000005.1", []byte("not an event"), time.Now())
	if err := os.Mkdir(filepath.Join(events, "000000006.1"), 0o755); err != nil {
		t.Fatal(err)
	}
	leave(t, root, "a1", ".000000007.1", prompt, time.Now()) // the hook's, half written
	s := journal.Files{Dir: dir}
	if err := s.Ingest(root, "a1"); err != nil {
		t.Fatal(err)
	}
	j, _ := s.Read("a1")
	var got []string
	for _, e := range j.Events {
		got = append(got, fmt.Sprintf("%s:%s:%d", e.File, e.Kind, e.Facts.Size))
	}
	link := len(secret)
	want := fmt.Sprintf("000000001.1:UserPromptSubmit:0 000000002.1:unreadable:%d 000000003.1:unreadable:0 000000004.1:unreadable:%d 000000005.1:unreadable:12", link, 16<<20+1)
	if strings.Join(got, " ") != want {
		t.Fatalf("%v\nwant %s", got, want)
	}
	if rest := strings.Join(left(t, root, "a1"), " "); rest != ".000000007.1 000000006.1" {
		t.Errorf("left %s", rest)
	}
	if data, _ := os.ReadFile(secret); string(data) != string(prompt) {
		t.Errorf("the link's target changed")
	}
	if err := s.Ingest(root, "not-an-id"); err == nil {
		t.Error("an id hq did not issue")
	}
}

func TestTheJournalIsTheUsersAlone(t *testing.T) {
	dir, root := filepath.Join(t.TempDir(), "state", "hq"), newRoot(t)
	leave(t, root, "a1", "000000001.1", journaltest.Hook("main", map[string]any{"hook_event_name": "Stop", "last_assistant_message": "hi"}), time.Now())
	if err := (journal.Files{Dir: dir}).Ingest(root, "a1"); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"journals", "journals/a1", "journals/a1.reply", "counter", "lock"} {
		info, err := os.Stat(filepath.Join(dir, p))
		if err != nil {
			t.Fatal(err)
		}
		want := os.FileMode(0o600)
		if info.IsDir() {
			want = 0o700
		}
		if info.Mode().Perm() != want {
			t.Errorf("%s: %v", p, info.Mode())
		}
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "journals"))
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if strings.Join(names, " ") != "a1 a1.reply" {
		t.Errorf("files %v", names)
	}
	// A store with no directory refuses rather than writes anywhere.
	if err := (journal.Files{}).Ingest(root, "a1"); err != nil {
		t.Errorf("nothing to ingest: %v", err)
	}
	leave(t, root, "a1", "000000002.1", prompt, time.Now())
	if err := (journal.Files{}).Ingest(root, "a1"); err == nil {
		t.Error("ingested with no directory")
	}
	if _, err := (journal.Files{}).Read("a1"); err == nil {
		t.Error("read with no directory")
	}
	if err := (journal.Files{}).Remove(root, "a1"); err != nil || len(left(t, root, "a1")) != 0 {
		t.Errorf("remove with no directory: %v", err)
	}
}

func TestAFileTheIngestCannotRemoveIsNeverAppendedTwice(t *testing.T) {
	dir, root := filepath.Join(t.TempDir(), "hq"), newRoot(t)
	s := journal.Files{Dir: dir}
	leave(t, root, "a1", "000000001.1", prompt, time.Now())
	events := state.EventsDir(root, "a1")
	if err := os.Chmod(events, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(events, 0o755) })
	if err := s.Ingest(root, "a1"); err == nil {
		t.Fatal("an ingest that could not remove its file says nothing")
	}
	if err := s.Ingest(root, "a1"); err == nil {
		t.Fatal("the file was taken again")
	}
	if err := os.Chmod(events, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := s.Ingest(root, "a1"); err != nil {
		t.Fatal(err)
	}
	j, _ := s.Read("a1")
	if len(j.Events) != 1 || len(left(t, root, "a1")) != 0 {
		t.Fatalf("%+v, left %v", j.Events, left(t, root, "a1"))
	}
}

func TestTheStoreFailsWhereItCannotWrite(t *testing.T) {
	root := newRoot(t)
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// Its directory under a file: nothing can be locked.
	s := journal.Files{Dir: filepath.Join(file, "hq")}
	leave(t, root, "a1", "000000001.1", prompt, time.Now())
	_, aerr := s.Append("a1", state.Event{Kind: "typed"})
	_, rerr := s.Read("a1")
	if s.Ingest(root, "a1") == nil || aerr == nil || rerr == nil || s.Remove(root, "a1") == nil {
		t.Errorf("worked without a directory: %v %v", aerr, rerr)
	}
	if len(left(t, root, "a1")) != 0 {
		t.Errorf("the event files of a removed agent stay")
	}

	// A journal, its reply or the record of an ingest under way that is
	// no file.
	for _, name := range []string{"a1", "a1.reply", "a1.ingest"} {
		dir := filepath.Join(t.TempDir(), "hq")
		s := journal.Files{Dir: dir}
		if err := os.MkdirAll(filepath.Join(dir, "journals", name, "x"), 0o700); err != nil {
			t.Fatal(err)
		}
		leave(t, root, "a1", "000000001.1", journaltest.Hook("main", map[string]any{"hook_event_name": "Stop"}), time.Now())
		if err := s.Ingest(root, "a1"); err == nil {
			t.Errorf("%s: ingested", name)
		}
		if len(left(t, root, "a1")) != 1 {
			t.Errorf("%s: the event file went", name)
		}
		if name == "a1" {
			_, aerr := s.Append("a1", state.Event{Kind: "typed"})
			_, rerr := s.Read("a1")
			if aerr == nil || rerr == nil || s.Remove(root, "a1") == nil {
				t.Errorf("a journal that is a directory: %v %v", aerr, rerr)
			}
		}
		os.RemoveAll(state.EventsDir(root, "a1"))
	}

	// A counter that holds no number numbers nothing.
	dir := filepath.Join(t.TempDir(), "hq")
	if err := os.MkdirAll(dir, 0o700); err != nil || os.WriteFile(filepath.Join(dir, "counter"), []byte("-1\n"), 0o600) != nil {
		t.Fatal(err)
	}
	if _, err := (journal.Files{Dir: dir}).Append("a1", state.Event{Kind: "typed"}); err == nil {
		t.Error("appended with no number")
	}
	if _, err := (journal.Files{Dir: dir}).Read("a1"); err == nil {
		t.Error("read with no number")
	}
}

// An append reads on from what the append before kept beside the journal
// (ID.read): a report the hook made in between overtakes a look's evidence
// based on the one before it, the same evidence again is a duplicate, and
// what was kept going beyond the journal, or spoilt, is read anew from the
// journal's start.
func TestAnAppendReadsOnFromTheAppendBefore(t *testing.T) {
	root, dir := newRoot(t), t.TempDir()
	s := journal.Files{Dir: dir}
	const id = "a1"
	at := time.Unix(1000, 0)
	leave(t, root, id, "000000001.prompt.1", journaltest.Hook("main", map[string]any{"hook_event_name": "UserPromptSubmit"}), at)
	if err := s.Ingest(root, id); err != nil {
		t.Fatal(err)
	}
	ended := state.Event{Source: state.SourceScreen, Kind: state.KindTurnEnded, Time: at, Basis: 1}
	asides := func(e state.Event) string {
		t.Helper()
		out, err := s.Append(id, e)
		if err != nil {
			t.Fatal(err)
		}
		return out[0].Aside
	}
	if got := asides(ended); got != "" {
		t.Fatalf("first: %q", got)
	}
	if got := asides(ended); got != state.AsideDuplicate {
		t.Fatalf("again: %q", got)
	}
	leave(t, root, id, "000000002.stop.1", journaltest.Hook("main", map[string]any{"hook_event_name": "Stop"}), at)
	if err := s.Ingest(root, id); err != nil {
		t.Fatal(err)
	}
	if got := asides(state.Event{Source: state.SourceScreen, Kind: state.KindAtRest, Time: at, Basis: 1}); got != state.AsideOvertaken {
		t.Fatalf("after a report: %q", got)
	}
	lastRead := filepath.Join(dir, "journals", id+".read")
	for _, spoilt := range []string{`{"offset":99999999,"basis":0,"evidence":{}}`, `not json`} {
		if err := os.WriteFile(lastRead, []byte(spoilt), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := asides(state.Event{Source: state.SourceScreen, Kind: state.KindAtRest, Time: at, Basis: 1}); got != state.AsideOvertaken {
			t.Fatalf("%s: %q", spoilt, got)
		}
	}
	if err := s.Remove(root, id); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(lastRead); !os.IsNotExist(err) {
		t.Fatalf("kept after the removal: %v", err)
	}
}

// An append reads on from the append before (design 3.4): what it kept
// beside the journal stands for every byte read before, which it does not
// read again however long the journal. With those bytes overwritten by
// events that, read, would make the event appended last no longer the
// latest turn end (another basis), the append still sets that event aside
// as a duplicate, as it can only by what it kept and by reading none of
// them, neither in place of what it kept nor on top of it (#191).
func TestAnAppendReadsNothingTheAppendBeforeRead(t *testing.T) {
	root, dir := newRoot(t), t.TempDir()
	s := journal.Files{Dir: dir}
	const id = "a1"
	at := time.Unix(1000, 0)
	for i := range 1000 {
		leave(t, root, id, fmt.Sprintf("%09d.prompt.1", i+1), journaltest.Hook("main", map[string]any{"hook_event_name": "UserPromptSubmit", "prompt_id": fmt.Sprint(i)}), at)
	}
	if err := s.Ingest(root, id); err != nil {
		t.Fatal(err)
	}
	e := state.Event{Source: state.SourceScreen, Kind: state.KindTurnEnded, Time: at, Basis: 1000}
	if _, err := s.Append(id, e); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "journals", id)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	other, err := json.Marshal(state.Event{Number: 1, Source: state.SourceScreen, Kind: state.KindTurnEnded, Time: at, Basis: 999})
	if err != nil {
		t.Fatal(err)
	}
	// As many lines of the other turn end as the journal's bytes take, the
	// last padded with spaces to their length.
	line := append(other, '\n')
	overwritten := bytes.Repeat(line, int(info.Size())/len(line)-1)
	pad := int(info.Size()) - len(overwritten) - len(line)
	overwritten = append(overwritten, append(append(other, bytes.Repeat([]byte(" "), pad)...), '\n')...)
	if err := os.WriteFile(path, overwritten, 0o600); err != nil {
		t.Fatal(err)
	}
	if j, err := s.Read(id); err != nil || int64(len(overwritten)) != info.Size() || len(j.Events) == 0 || j.Events[len(j.Events)-1].Basis != 999 {
		t.Fatalf("overwritten %d bytes of %d: %v, %d events", len(overwritten), info.Size(), err, len(j.Events))
	}
	out, err := s.Append(id, e)
	if err != nil || len(out) != 1 || out[0].Aside != state.AsideDuplicate {
		t.Fatalf("the append after the journal's bytes were overwritten: %v %+v", err, out)
	}
}
