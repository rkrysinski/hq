package journaltest

import (
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
