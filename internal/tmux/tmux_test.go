package tmux

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestAtLeast(t *testing.T) {
	for v, ok := range map[string]bool{"3.4": true, "3.7c": true, "3.3a": false, "3.2a": false, "4.0": true, "next-3.6": true, "master": true, "2.9": false, "junk": false} {
		if AtLeast(v, "3.4") != ok {
			t.Errorf("AtLeast(%q) != %v", v, ok)
		}
	}
}

// The terminal's height is the window's and the status lines tmux takes
// from it (#79): an 80x24 terminal holds a 23-line window.
func TestTerminalHeightAddsTheStatusLines(t *testing.T) {
	for out, want := range map[string]int{"23 on\n": 24, "24 off\n": 24, "21 3\n": 24, "22 2": 24} {
		if got, err := terminalHeight(out); err != nil || got != want {
			t.Errorf("%q: %d %v, want %d", out, got, err, want)
		}
	}
	for _, out := range []string{"", "23", "x on", "23 maybe"} {
		if _, err := terminalHeight(out); err == nil {
			t.Errorf("%q: no error", out)
		}
	}
}

func TestTheSlotTitleEndsWhereTheSlotEnds(t *testing.T) {
	// tmux 3.6 draws a pane's status two columns wider: the two past the
	// slot take the surround's colour.
	for v, wide := range map[string]bool{"3.4": false, "3.5a": false, "3.6": true, "3.7c": true, "next-3.8": true, "master": true} {
		f := borderFormat(v)
		if got := strings.Contains(f, "align=right bg="+surroundColour+"]  "); got != wide {
			t.Errorf("tmux %s: surround at the end %v, want %v: %s", v, got, wide, f)
		}
		if !strings.Contains(f, slotTitleStyle+" ▸ "+slotTitle) {
			t.Errorf("tmux %s: %s", v, f)
		}
	}
}

// The title row takes the slot's look (#128): the terminal's background and
// the accent while the docked session, the active pane, has the keys; the
// surround and a dim title otherwise, the placeholder (role slot) always.
func TestTheSlotTitleFollowsTheKeys(t *testing.T) {
	focused := "#[fill=terminal bg=terminal fg=#A78BFA]"
	unfocused := "#[fill=#16181D bg=#16181D fg=#5C626C]"
	want := "#{?#{&&:#{pane_active},#{!=:#{@hq_role},slot}}," + focused + "," + unfocused + "}"
	if !strings.Contains(slotTitleStyle, want) {
		t.Fatalf("title style %s, want %s", slotTitleStyle, want)
	}
	if !strings.HasPrefix(slotTitleStyle, "#{P:#{?"+inSlot+",") {
		t.Errorf("title style not taken from the pane in the slot: %s", slotTitleStyle)
	}
}

// recorder is a tmux that answers -V and records every other call.
type recorder struct{ calls [][]string }

func (r *recorder) Run(name string, args ...string) ([]byte, error) {
	if len(args) == 1 && args[0] == "-V" {
		return []byte("tmux 3.7c\n"), nil
	}
	r.calls = append(r.calls, args)
	return nil, nil
}

// set is what calls set with set-option on target, option by option, the
// scope flag (-w, -p or none) kept with the option.
func set(calls [][]string, target string) map[string]string {
	got := map[string]string{}
	for _, call := range calls {
		var cmds [][]string
		var cmd []string
		for _, a := range call {
			if a == ";" {
				cmds, cmd = append(cmds, cmd), nil
				continue
			}
			cmd = append(cmd, a)
		}
		for _, cmd := range append(cmds, cmd) {
			if len(cmd) == 6 && cmd[0] == "set-option" && cmd[2] == "-t" && cmd[3] == target {
				got[cmd[1]+" "+cmd[4]] = cmd[5]
			}
		}
	}
	return got
}

// The dashboard window's styles are the slot's (#128): a docked agent's pane
// takes them in the slot and nothing of them travels to its home window.
// The pane with the keys has the terminal's background; without them the
// surround, its text dimmed.
func TestTheSlotLooksFocusedOnlyWithTheKeys(t *testing.T) {
	r := &recorder{}
	if err := (Client{Run: r}).style("@1"); err != nil {
		t.Fatal(err)
	}
	got := set(r.calls, "@1")
	for opt, want := range map[string]string{"-w window-style": "bg=#16181D,fg=#9CA3AF", "-w window-active-style": "bg=terminal"} {
		if got[opt] != want {
			t.Errorf("%s = %q, want %q", opt, got[opt], want)
		}
	}
}

// The list's own styles flip with the keys; the margins keep the surround;
// the placeholder, which never holds the keys, looks unfocused either way.
func TestEachPaneOfTheDashboardTakesItsLook(t *testing.T) {
	for name, tc := range map[string]struct {
		cmds             [][]string
		inactive, active string
	}{
		"list":        {listPane("%1"), "bg=#16181D", "bg=terminal"},
		"margin":      {surroundPane("%1"), "bg=#16181D", "bg=#16181D"},
		"placeholder": {placeholderPane("%1"), "bg=#16181D,fg=#9CA3AF", "bg=#16181D,fg=#9CA3AF"},
	} {
		got := set(tc.cmds, "%1")
		if got["-p window-style"] != tc.inactive || got["-p window-active-style"] != tc.active {
			t.Errorf("%s: %v, want %q without the keys and %q with them", name, got, tc.inactive, tc.active)
		}
	}
}

func TestChunksNeverCutACharacter(t *testing.T) {
	s := strings.Repeat("ab", 3) + "żółw" // ż, ó and ł are two bytes each
	for n := 2; n <= len(s); n++ {
		parts := chunks(s, n)
		if strings.Join(parts, "") != s {
			t.Fatalf("n=%d: %q", n, parts)
		}
		for _, p := range parts {
			if len(p) > n || !utf8.ValidString(p) {
				t.Fatalf("n=%d: part %q", n, p)
			}
		}
	}
	if got := chunks("", 4); len(got) != 1 || got[0] != "" {
		t.Fatalf("%q", got)
	}
}

func TestAsStoredUndoesWhatTmux34DoesToADollarName(t *testing.T) {
	for _, c := range []struct{ name, out, want string }{
		{"tmux 3.4: the backslash before each $name goes", "/w/my\\$repo::hq::a \\${b} \\$_c\n\\$hq\n", "/w/my$repo::hq::a ${b} $_c\n"},
		{"tmux 3.4: a $ that starts no name was left alone", "5$ $1 $ \\n $\n\\$hq\n", "5$ $1 $ \\n $\n"},
		{"tmux 3.4: a stored backslash before $name keeps one", "a \\\\$x\n\\$hq\n", "a \\$x\n"},
		{"tmux 3.4: nothing but the probe", "\\$hq\n", ""},
		{"newer tmux: as it is, a stored backslash too", "/w/my$repo a \\$x\n$hq\n", "/w/my$repo a \\$x\n"},
		{"newer tmux: nothing but the probe", "$hq\n", ""},
		{"no probe: untouched", "a \\$x\n", "a \\$x\n"},
		{"the probe's text inside a value is no probe", "my$hq\n", "my$hq\n"},
		{"empty", "", ""},
	} {
		if got := asStored(c.out); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}
