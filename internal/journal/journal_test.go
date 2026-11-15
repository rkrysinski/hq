package journal

import (
	"testing"

	"github.com/rkrysinski/hq/internal/state"
)

func TestDirIsHqsUnderTheUsersStateDirectory(t *testing.T) {
	for _, tc := range []struct{ state, home, want string }{
		{"/s", "/h", "/s/hq"},
		{"", "/h", "/h/.local/state/hq"},
		{"rel", "/h", "/h/.local/state/hq"},
		{"", "", ""},
		{"", "rel", ""},
	} {
		env := map[string]string{"XDG_STATE_HOME": tc.state, "HOME": tc.home}
		if got := Dir(func(k string) string { return env[k] }); got != tc.want {
			t.Errorf("%+v: %q", tc, got)
		}
	}
}

// An agent's home is where its latest launch that stands put it: a launch
// set aside, one of another source, an event of another kind and a launch
// that names no repository do not move it.
func TestTheHomeIsWhereTheLatestLaunchThatStandsWas(t *testing.T) {
	launch := func(source, repo, tmux, aside string) state.Event {
		return state.Event{Source: source, Kind: state.KindLaunched, Aside: aside, Facts: state.Facts{Repo: repo, Tmux: tmux}}
	}
	if _, _, ok := Home(nil); ok {
		t.Fatal("a home without a launch")
	}
	if _, _, ok := Home([]state.Event{launch(state.SourceHQ, "", "", "")}); ok {
		t.Fatal("a home from a launch that names no repository")
	}
	events := []state.Event{
		launch(state.SourceHQ, "/w/a", "", ""),
		launch(state.SourceHQ, "/w/b", "qa", ""),
		{Source: state.SourceHQ, Kind: state.KindRelaunched, Facts: state.Facts{Repo: "/w/c"}},
		launch(state.SourceHQ, "/w/d", "", state.AsideDuplicate),
		launch(state.SourceHook, "/w/e", "", ""),
		launch(state.SourceHQ, "", "", ""),
	}
	if repo, tmux, ok := Home(events); !ok || repo != "/w/b" || tmux != "qa" {
		t.Fatalf("home %q %q %v", repo, tmux, ok)
	}
}
