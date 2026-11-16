//go:build integration

package cli

import (
	"github.com/rkrysinski/hq/internal/journal"
	"github.com/rkrysinski/hq/internal/journal/journaltest"
	"github.com/rkrysinski/hq/internal/state"
)

// readState is what agent id of the repository at root reports last, as
// its journal gives it (the hooks' event files ingested first): its state,
// last message and Claude session; ok is false while its journal holds no
// report of its hook.
func (d deps) readState(root, id string) (r journaltest.HookState, ok bool) {
	if err := d.journal.Ingest(root, id); err != nil {
		return journaltest.HookState{}, false
	}
	j, err := d.journal.Read(id)
	if err != nil {
		return journaltest.HookState{}, false
	}
	red := state.Fold{}.With(j.Events...).Result(j.Reply, d.now())
	return journaltest.HookState{State: red.State, Since: red.Since, Last: red.Last, Branch: red.Branch, Cwd: red.Worktree, SessionID: red.Session}, journal.Basis(j.Events) != 0
}
