package journal

import "testing"

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
