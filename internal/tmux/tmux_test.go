package tmux

import "testing"

func TestAtLeast(t *testing.T) {
	for v, ok := range map[string]bool{"3.4": true, "3.7c": true, "3.3a": false, "3.2a": false, "4.0": true, "next-3.6": true, "master": true, "2.9": false, "junk": false} {
		if AtLeast(v, "3.4") != ok {
			t.Errorf("AtLeast(%q) != %v", v, ok)
		}
	}
}
