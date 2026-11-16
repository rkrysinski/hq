package journaltest

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/state"
)

func TestMemoryKeepsTheContract(t *testing.T) {
	Contract(t, func(t *testing.T) Harness {
		m := NewMemory()
		n := 0
		return Harness{
			Store: m,
			Leave: m.Leave,
			Left:  m.Left,
			Root: func() string {
				n++
				return "/w/app" + string(rune('0'+n))
			},
		}
	})
}

func TestMemoryIngestsOnlyEventFilesAndListsItsJournals(t *testing.T) {
	m := NewMemory()
	at := time.Now()
	m.Leave("/w/app", "a1", ".000000001.1", Hook("main", map[string]any{"hook_event_name": "Stop"}), at)
	m.Leave("/w/app", "a1", "000000002.1", Hook("main", map[string]any{"hook_event_name": "Stop"}), at)
	if err := m.Ingest("/w/app", "a1"); err != nil {
		t.Fatal(err)
	}
	j, _ := m.Read("a1")
	if len(j.Events) != 1 || j.Events[0].File != "000000002.1" || len(m.Left("/w/app", "a1")) != 1 {
		t.Fatalf("%+v %v", j.Events, m.Left("/w/app", "a1"))
	}
	if got := m.Agents(); len(got) != 1 || got[0] != "a1" {
		t.Errorf("agents %v", got)
	}
}

// Put numbers its events on from the one counter, as Append does.
func TestPutNumbersOnFromTheCounter(t *testing.T) {
	m := NewMemory()
	m.Put("a1", state.Event{Source: state.SourceHook, Kind: "Stop"}, state.Event{Source: state.SourceHook, Kind: "Stop"})
	out, _ := m.Append("b2", state.Event{Source: state.SourceHQ, Kind: state.KindLaunched})
	j, _ := m.Read("a1")
	if len(j.Events) != 2 || j.Events[0].Number != 1 || j.Events[1].Number != 2 || out[0].Number != 3 {
		t.Fatalf("%+v %+v", j.Events, out)
	}
}

// The hook of an agent that ended reports its last message at a turn end
// before the session's end, and only the end when it has none.
func TestReportFilesOfAnEndedAgent(t *testing.T) {
	at := time.Unix(1000, 0)
	kinds := func(fs []File) string {
		var s []string
		for _, f := range fs {
			s = append(s, f.Kind)
		}
		return strings.Join(s, " ")
	}
	if got := kinds(ReportFiles(state.Report{State: state.Ended, Since: at, Last: "Bye."}, state.Detail{})); got != "stop end" {
		t.Errorf("with a message: %s", got)
	}
	if got := kinds(ReportFiles(state.Report{State: state.Ended, Since: at}, state.Detail{})); got != "end" {
		t.Errorf("without one: %s", got)
	}
}

// A dialog with no question, tool or form of its own is Claude's
// Notification, saying what its ask says, else the last message.
func TestReportFilesOfANotification(t *testing.T) {
	at := time.Unix(1000, 0)
	for ask, want := range map[*state.Ask]string{nil: `"message":"Waiting"`, {Message: "Pick a colour"}: `"message":"Pick a colour"`} {
		fs := ReportFiles(state.Report{State: state.NeedsInput, Since: at, Last: "Waiting"}, state.Detail{Ask: ask})
		if d := string(fs[len(fs)-1].Data); !strings.Contains(d, `"Notification"`) || !strings.Contains(d, want) {
			t.Errorf("%+v: %s", ask, d)
		}
	}
}

// payloadOf is the hook's payload in event file f, after its branch line.
func payloadOf(t *testing.T, f File) map[string]any {
	t.Helper()
	_, data, _ := strings.Cut(string(f.Data), "\n")
	var p map[string]any
	if err := json.Unmarshal([]byte(data), &p); err != nil {
		t.Fatalf("%s: %v", f.Kind, err)
	}
	return p
}

// The hook's payloads name the prompt of a report that has one, and only
// then; a turn end lists the agent's own tasks, numbered from b1, and no
// background tasks when it has none.
func TestReportFilesNameThePromptAndTheOwnTasks(t *testing.T) {
	at := time.Unix(1000, 0)
	fs := ReportFiles(state.Report{State: state.Working, Since: at, Prompt: "fix"}, state.Detail{})
	if p := payloadOf(t, fs[len(fs)-1]); p["prompt_id"] != "p-fix" {
		t.Errorf("with a prompt: %v", p)
	}
	fs = ReportFiles(state.Report{State: state.Done, Since: at, Last: "Done."}, state.Detail{})
	p := payloadOf(t, fs[0])
	if _, tasks := p["background_tasks"]; p["prompt_id"] != nil || tasks || p["last_assistant_message"] != "Done." {
		t.Errorf("without a prompt or tasks: %v", p)
	}
	own := []state.Task{{Type: state.ShellTask, Text: "npm run dev"}, {Type: state.MonitorTask, Text: "the logs"}}
	fs = ReportFiles(state.Report{State: state.Done, Since: at, Last: "Done.", OwnTasks: own}, state.Detail{})
	tasks, _ := payloadOf(t, fs[0])["background_tasks"].([]any)
	var ids []string
	for _, task := range tasks {
		ids = append(ids, task.(map[string]any)["id"].(string))
	}
	if strings.Join(ids, " ") != "b1 b2" {
		t.Errorf("own tasks: %v", tasks)
	}
}

// The contract's check of a journal's numbers: each above zero and above
// the one before it.
func TestNumberedInOrderWantsEachNumberAboveTheOneBefore(t *testing.T) {
	for _, tc := range []struct {
		numbers []int64
		want    bool
	}{
		{nil, true}, {[]int64{1}, true}, {[]int64{1, 2, 5}, true},
		{[]int64{0}, false}, {[]int64{-1, 2}, false}, {[]int64{2, 2}, false}, {[]int64{3, 2}, false}, {[]int64{1, 3, 3}, false},
	} {
		var events []state.Event
		for _, n := range tc.numbers {
			events = append(events, state.Event{Number: n})
		}
		if got := numberedInOrder(events); got != tc.want {
			t.Errorf("%v: %v", tc.numbers, got)
		}
	}
}
