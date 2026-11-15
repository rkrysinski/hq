package journaltest

import (
	"testing"
	"time"
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
