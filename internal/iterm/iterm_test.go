package iterm

import (
	"encoding/json"
	"testing"
)

func TestTheProfileIsHqWithOptionAsEscPlusOverTheDefault(t *testing.T) {
	var f struct{ Profiles []map[string]any }
	if err := json.Unmarshal(Profile(), &f); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if len(f.Profiles) != 1 {
		t.Fatalf("%d profiles, want one", len(f.Profiles))
	}
	p := f.Profiles[0]
	if p["Name"] != Name || p["Guid"] == "" {
		t.Errorf("name %v guid %v", p["Name"], p["Guid"])
	}
	// 2 is Esc+ in iTerm2's preferences.
	if p["Option Key Sends"] != 2.0 || p["Right Option Key Sends"] != 2.0 {
		t.Errorf("option keys %v %v, want 2 (Esc+)", p["Option Key Sends"], p["Right Option Key Sends"])
	}
	// No parent: the user's default profile, so only the Option keys change.
	for _, k := range []string{"Dynamic Profile Parent Name", "Dynamic Profile Parent GUID"} {
		if _, ok := p[k]; ok {
			t.Errorf("%s set", k)
		}
	}
	if len(p) != 4 {
		t.Errorf("profile sets more than its name and the Option keys: %v", p)
	}
}

func TestProfilePathIsInItermsDynamicProfiles(t *testing.T) {
	if got := ProfilePath("/Users/u"); got != "/Users/u/Library/Application Support/iTerm2/DynamicProfiles/hq.json" {
		t.Fatal(got)
	}
}

func TestSetProfileOnlyInIterm(t *testing.T) {
	for _, tc := range []struct {
		env  map[string]string
		want string
	}{
		{map[string]string{"TERM_PROGRAM": "iTerm.app"}, "\x1b]1337;SetProfile=hq\a"},
		{map[string]string{"LC_TERMINAL": "iTerm2"}, "\x1b]1337;SetProfile=hq\a"}, // through ssh
		{map[string]string{"TERM_PROGRAM": "Apple_Terminal"}, ""},
		{map[string]string{"TERM_PROGRAM": "vscode"}, ""},
		// VS Code started from iTerm2 hands LC_TERMINAL on to its terminal.
		{map[string]string{"TERM_PROGRAM": "vscode", "LC_TERMINAL": "iTerm2"}, ""},
		{map[string]string{"TERM_PROGRAM": "iTerm.app", "LC_TERMINAL": "iTerm2"}, "\x1b]1337;SetProfile=hq\a"},
		{map[string]string{"WT_SESSION": "x"}, ""}, // Windows Terminal on WSL
		{nil, ""},
	} {
		if got := SetProfile(func(k string) string { return tc.env[k] }); got != tc.want {
			t.Errorf("%v: %q, want %q", tc.env, got, tc.want)
		}
	}
}
