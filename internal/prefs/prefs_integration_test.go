//go:build integration

package prefs

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// hq processes changing the file at once (the list keeping its modes, the
// update check) never lose each other's changes, and a reader never sees
// half a file.
func TestUpdatesAtOnceLoseNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hq", "preferences.json")
	const n = 40
	var wg sync.WaitGroup
	stop := make(chan struct{})
	torn := make(chan string, 1)
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			if data, err := os.ReadFile(path); err == nil && len(data) == 0 {
				select {
				case torn <- "empty file read":
				default:
				}
			}
		}
	}()
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := Update(path, func(p *Prefs) bool {
				if p.other == nil {
					p.other = map[string]json.RawMessage{}
				}
				p.other[fmt.Sprintf("k%02d", i)] = json.RawMessage("1")
				return true
			})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	close(stop)
	select {
	case msg := <-torn:
		t.Fatal(msg)
	default:
	}
	if p := Load(path); len(p.other) != n {
		t.Fatalf("%d of %d changes kept", len(p.other), n)
	}
	// Leaving the file as it is writes nothing.
	before, _ := os.Stat(path)
	if err := Update(path, func(*Prefs) bool { return false }); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.Stat(path); !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("unchanged preferences rewritten")
	}
}
