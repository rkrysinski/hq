package main

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// seesSrc is a test file of package a whose one test holds body, which
// starts on line 7.
func seesSrc(body string) []byte {
	return []byte("package a\n\nimport \"testing\"\n\nfunc TestX(t *testing.T) {\n\tvar f, other fold\n" + body + "\n}\n")
}

// helperSrc is the helper's file with the helper asking ScreenMayTell
// before it calls Sees, and a test that calls Sees itself on line 13.
const helperSrc = "package state\n\nimport \"testing\"\n\n" +
	"func seesWhenItMayTell(t *testing.T, f Fold, screen string, now int) ([]Event, bool) {\n" +
	"\tif !f.ScreenMayTell(now) {\n\t\tt.Fatal()\n\t}\n\treturn f.Sees(screen, now)\n}\n\n" +
	"func TestX(t *testing.T) {\n\tFold{}.Sees(\"\", 0)\n\tseesWhenItMayTell(t, Fold{}, \"\", 0)\n}\n"

func TestSeesFindsEveryUseOfAMethodNamedSees(t *testing.T) {
	for _, tc := range []struct {
		body string
		line int
	}{
		{"\tf.Sees(\"screen\", now)", 7},
		{"\tseen, resting := f.Sees(\"screen\", now)\n\t_, _ = seen, resting", 7},
		{"\tother.Sees(\"screen\", now)", 7},
		{"\tFold{}.With().Sees(\"screen\", now)", 7},
		{"\t_ = 1\n\tif seen, _ := f.Sees(\"screen\", now); seen != nil {\n\t}", 8},
		{"\tlook := f.Sees\n\t_ = look", 7},
		{"\tlook := Fold.Sees\n\t_ = look", 7},
		{"\tlook := (*Fold).Sees\n\t_ = look", 7},
		{"\tfunc() {\n\t\tf.Sees(\"screen\", now)\n\t}()", 8},
	} {
		hits, helper, err := scanSees("internal/a/a_test.go", seesSrc(tc.body))
		if err != nil {
			t.Fatal(err)
		}
		if want := []seesHit{{file: "internal/a/a_test.go", line: tc.line}}; !reflect.DeepEqual(hits, want) || helper.found {
			t.Errorf("%q: hits %v helper %+v, want %v", tc.body, hits, helper, want)
		}
	}
}

func TestSeesIgnoresWhatIsMerelyNamedSees(t *testing.T) {
	for _, src := range []string{
		"package a\n\nfunc Sees() {}\n\nfunc use() { Sees() }\n",
		"package a\n\nfunc use() {\n\tSees := func() {}\n\tSees()\n}\n",
		"package a\n\nvar Sees = 1\n",
		"package a\n\nimport \"example.com/look\"\n\nfunc use() { look.Sees() }\n",
		"package a\n\nimport other \"example.com/look\"\n\nfunc use() { other.Sees() }\n",
		"package a\n\n// f.Sees(screen, now) is said in a comment.\nfunc use() { _ = \"f.Sees(screen, now)\" }\n",
		"package a\n\nfunc use() { f.ScreenMayTell(now); f.SeesNot(); f.sees() }\n",
	} {
		hits, _, err := scanSees("internal/a/a_test.go", []byte(src))
		if err != nil {
			t.Fatal(err)
		}
		if len(hits) != 0 {
			t.Errorf("%q: hits %v, want none", src, hits)
		}
	}
}

// A package-level variable of another file is no import, even when a call
// on it looks like one.
func TestSeesFindsACallOnAVariableThatIsNoImport(t *testing.T) {
	src := "package a\n\nimport \"example.com/look\"\n\nvar _ = look.X\n\nfunc use() { fold.Sees() }\n"
	hits, _, err := scanSees("internal/a/a_test.go", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if want := []seesHit{{file: "internal/a/a_test.go", line: 7}}; !reflect.DeepEqual(hits, want) {
		t.Fatalf("hits %v, want %v", hits, want)
	}
}

func TestSeesExemptsTheHelperInItsFileOnly(t *testing.T) {
	hits, helper, err := scanSees(seesHelperFile, []byte(helperSrc))
	if err != nil {
		t.Fatal(err)
	}
	if want := []seesHit{{file: seesHelperFile, line: 13}}; !reflect.DeepEqual(hits, want) {
		t.Errorf("hits %v, want %v", hits, want)
	}
	if want := (seesHelper{found: true, callsScreenMayTell: true, line: 5}); helper != want {
		t.Errorf("helper %+v, want %+v", helper, want)
	}
	// The same function in another file is no helper.
	hits, helper, err = scanSees("internal/state/other_test.go", []byte(helperSrc))
	if err != nil {
		t.Fatal(err)
	}
	if want := []seesHit{{file: "internal/state/other_test.go", line: 9}, {file: "internal/state/other_test.go", line: 13}}; !reflect.DeepEqual(hits, want) || helper.found {
		t.Errorf("another file: hits %v helper %+v, want %v", hits, helper, want)
	}
	// Nor a method of that name in the helper's file.
	method := strings.Replace(helperSrc, "func seesWhenItMayTell(", "func (Fold) seesWhenItMayTell(", 1)
	if hits, helper, _ := scanSees(seesHelperFile, []byte(method)); len(hits) != 2 || helper.found {
		t.Errorf("a method: hits %v helper %+v", hits, helper)
	}
	// A helper that does not ask ScreenMayTell is found as one that does not.
	silent := strings.Replace(helperSrc, "f.ScreenMayTell(now)", "true", 1)
	if _, helper, _ := scanSees(seesHelperFile, []byte(silent)); helper != (seesHelper{found: true, line: 5}) {
		t.Errorf("not asking: helper %+v", helper)
	}
	// Taking ScreenMayTell as a value is no asking.
	value := strings.Replace(helperSrc, "!f.ScreenMayTell(now)", "f.ScreenMayTell == nil", 1)
	if _, helper, _ := scanSees(seesHelperFile, []byte(value)); helper.callsScreenMayTell {
		t.Errorf("a value: helper %+v", helper)
	}
}

func TestCheckSeesReadsTestFilesOfEveryTagAndOnlyThem(t *testing.T) {
	root := t.TempDir()
	use := "\nfunc use() { f.Sees(\"\", 0) }\n"
	for name, content := range map[string]string{
		seesHelperFile:                     helperSrc,
		"internal/a/a_test.go":             "package a\n" + use,
		"internal/a/a_integration_test.go": "//go:build integration\n\npackage a\n" + use,
		"e2e/journey_test.go":              "//go:build e2e\n\npackage e2e\n" + use,
		"tools/x/x_test.go":                "package x\n" + use,
		"internal/a/a.go":                  "package a\n" + use,
		"internal/a/testdata/t_test.go":    "package t\n" + use,
		".claude/worktrees/w/b_test.go":    "package b\n" + use,
		"internal/a/notes.txt":             "f.Sees(screen, now)",
	} {
		write(t, root, filepath.FromSlash(name), content)
	}
	hits, checked, err := checkSees(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []seesHit{
		{file: "e2e/journey_test.go", line: 5},
		{file: "internal/a/a_integration_test.go", line: 5},
		{file: "internal/a/a_test.go", line: 3},
		{file: seesHelperFile, line: 13},
		{file: "tools/x/x_test.go", line: 3},
	}
	if !reflect.DeepEqual(hits, want) || checked != 5 {
		t.Fatalf("hits %v in %d files, want %v in 5", hits, checked, want)
	}

	write(t, root, "internal/a/bad_test.go", "package a\nfunc {")
	if _, _, err := checkSees(root); err == nil || !strings.Contains(err.Error(), "bad_test.go") {
		t.Fatalf("err %v, want one naming the file that does not parse", err)
	}
	if _, _, err := checkSees(filepath.Join(root, "missing")); err == nil {
		t.Fatal("a root that does not exist: no error")
	}
}

func TestCheckSeesFailsWhenTheHelperIsNotThereOrDoesNotAsk(t *testing.T) {
	root := t.TempDir()
	write(t, root, "internal/a/a_test.go", "package a\n")
	hits, checked, err := checkSees(root)
	if err != nil {
		t.Fatal(err)
	}
	if want := []seesHit{{file: seesHelperFile, why: "has no function seesWhenItMayTell"}}; !reflect.DeepEqual(hits, want) || checked != 1 {
		t.Fatalf("no helper: hits %v in %d files, want %v", hits, checked, want)
	}

	write(t, root, seesHelperFile, strings.Replace(helperSrc, "f.ScreenMayTell(now)", "true", 1))
	hits, _, err = checkSees(root)
	if err != nil {
		t.Fatal(err)
	}
	if want := []seesHit{{file: seesHelperFile, line: 13}, {file: seesHelperFile, line: 5, why: "seesWhenItMayTell does not call ScreenMayTell"}}; !reflect.DeepEqual(hits, want) {
		t.Fatalf("not asking: hits %v, want %v", hits, want)
	}
}

func TestSeesHitSaysWhatToDoInstead(t *testing.T) {
	use := seesHit{file: "internal/a/a_test.go", line: 7}.String()
	if want := "internal/a/a_test.go:7 uses Sees outside seesWhenItMayTell: get what a look sees through seesWhenItMayTell (internal/state/seen_test.go), which first requires ScreenMayTell at that moment"; use != want {
		t.Errorf("a use: %q", use)
	}
	missing := seesHit{file: seesHelperFile, why: "has no function seesWhenItMayTell"}.String()
	if want := "internal/state/seen_test.go has no function seesWhenItMayTell: the gate exempts it as the one place a test calls Sees, so it must be there and call ScreenMayTell"; missing != want {
		t.Errorf("the helper missing: %q", missing)
	}
	silent := seesHit{file: seesHelperFile, line: 5, why: "seesWhenItMayTell does not call ScreenMayTell"}.String()
	if want := "internal/state/seen_test.go:5 seesWhenItMayTell does not call ScreenMayTell: the gate exempts it as the one place a test calls Sees, so it must be there and call ScreenMayTell"; silent != want {
		t.Errorf("the helper not asking: %q", silent)
	}
}
