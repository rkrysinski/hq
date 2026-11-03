package kills

import "testing"

func TestPathIsBesideThePreferencesFile(t *testing.T) {
	env := map[string]string{"HOME": "/home/u"}
	if got := Path(func(k string) string { return env[k] }); got != "/home/u/.config/hq/kills.log" {
		t.Fatalf("default: %s", got)
	}
	env["XDG_CONFIG_HOME"] = "/cfg"
	if got := Path(func(k string) string { return env[k] }); got != "/cfg/hq/kills.log" {
		t.Fatalf("XDG: %s", got)
	}
}

func TestKeepNewestKeepsWholeLinesFromTheEnd(t *testing.T) {
	for _, c := range []struct {
		data string
		max  int
		want string
	}{
		{"a\nb\n", 10, "a\nb\n"},
		{"aaa\nbbb\nccc\n", 6, "ccc\n"},
		{"aaa\nbbb\nccc\n", 8, "bbb\nccc\n"},
		{"aaa\nbbb\nccc\n", 7, "ccc\n"},
		{"aaa\nbbb\nccc\n", 9, "bbb\nccc\n"},
		{"aaaaaaaaaa\n", 4, ""},
	} {
		if got := string(keepNewest([]byte(c.data), c.max)); got != c.want {
			t.Errorf("keepNewest(%q, %d) = %q, want %q", c.data, c.max, got, c.want)
		}
	}
}
