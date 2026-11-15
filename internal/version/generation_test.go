package version_test

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/rkrysinski/hq/internal/state"
	"github.com/rkrysinski/hq/internal/version"
)

// pinned is the generation the forms of what agents report and of how hq
// keeps it were last pinned at, with a fingerprint of each (ADR 0020). It
// keeps the update rule from depending on memory: a change to one of the
// forms fails TestTheGenerationFollowsTheForms until it is pinned again.
//
// To pin again, set generation and the changed fingerprints to what the
// test prints. Raise version.Generation with them when what agents report,
// or the form hq keeps it in, changed; an hq of one generation cannot read
// an agent of another, and hq update refuses to cross generations while
// agents exist. A renewal that keeps the generation says in the pull
// request why what agents report is unchanged.
var pinned = struct {
	generation int
	forms      map[string]string
}{
	generation: 3,
	forms: map[string]string{
		"the hook's script": "ddc8a1343f7f2d58",
		"the record":        "7d2c1be547e68487",
	},
}

// forms are the forms the generation stands for, each as text whose
// fingerprint changes with it: the hook set and script hq injects into
// every agent it starts (state.Settings, ADR 0009), and hq's record of an
// agent for hq restore (state.Record).
func forms() map[string]string {
	return map[string]string{
		"the hook's script": state.Settings("NAME", "ID", "NOTIFY"),
		"the record":        form(reflect.TypeOf(state.Record{})),
	}
}

// form describes a type as it is kept: each field of a struct with its
// type and its tag, nested types included (a struct once, by name after).
func form(t reflect.Type) string { return formOf(t, map[reflect.Type]bool{}) }

func formOf(t reflect.Type, seen map[reflect.Type]bool) string {
	switch t.Kind() {
	case reflect.Pointer:
		return "*" + formOf(t.Elem(), seen)
	case reflect.Slice:
		return "[]" + formOf(t.Elem(), seen)
	case reflect.Map:
		return "map[" + formOf(t.Key(), seen) + "]" + formOf(t.Elem(), seen)
	case reflect.Struct:
		if seen[t] {
			return t.Name()
		}
		seen[t] = true
		var b strings.Builder
		b.WriteString(t.Name() + "{")
		for i := range t.NumField() {
			f := t.Field(i)
			fmt.Fprintf(&b, "%s %s %q;", f.Name, formOf(f.Type, seen), f.Tag)
		}
		return b.String() + "}"
	}
	return t.String()
}

func fingerprint(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}

// The generation follows the forms: the test fails until a change to one
// of them is pinned, with the generation raised or the renewal explained
// (story 50 of #165).
func TestTheGenerationFollowsTheForms(t *testing.T) {
	var changed []string
	now := forms()
	names := make([]string, 0, len(now))
	for name := range now {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if got := fingerprint(now[name]); got != pinned.forms[name] {
			changed = append(changed, fmt.Sprintf("%s (now %q)", name, got))
		}
	}
	if len(pinned.forms) != len(now) {
		t.Errorf("pinned %d forms, there are %d: pin every form of forms()", len(pinned.forms), len(now))
	}
	if version.Generation != pinned.generation {
		t.Errorf("version.Generation is %d, the forms are pinned at %d: pin them at %d", version.Generation, pinned.generation, version.Generation)
	}
	if len(changed) > 0 {
		t.Errorf("changed since generation %d was pinned: %s; raise version.Generation and pin the forms again, or pin them again and say in the pull request why what agents report is unchanged",
			pinned.generation, strings.Join(changed, ", "))
	}
}

// The fingerprint changes with any change to a form: a byte of the hook's
// script, a field of a kept type, its type or its tag.
func TestAFormChangesItsFingerprint(t *testing.T) {
	type kept struct {
		ID   string `json:"id"`
		Size int    `json:"size,omitempty"`
		Next *kept
	}
	type renamed struct {
		ID   string `json:"id"`
		Size int    `json:"bytes,omitempty"`
		Next *kept
	}
	type retyped struct {
		ID   string `json:"id"`
		Size int64  `json:"size,omitempty"`
		Next *kept
	}
	base := fingerprint(form(reflect.TypeOf(kept{})))
	for name, other := range map[string]string{
		"tag":    form(reflect.TypeOf(renamed{})),
		"type":   form(reflect.TypeOf(retyped{})),
		"nested": form(reflect.TypeOf([]map[string]*kept{})),
	} {
		if fingerprint(other) == base {
			t.Errorf("%s: same fingerprint", name)
		}
	}
	script := state.Settings("NAME", "ID", "NOTIFY")
	if fingerprint(script) == fingerprint(script+" ") {
		t.Error("a byte more of the script keeps its fingerprint")
	}
}
