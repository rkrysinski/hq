package main

import (
	"flag"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// loadRefused is what readArgs prints for a command line of load it refuses
// for the reason why.
func loadRefused(why string) string { return "testgate: load: " + why + "\n" + usage }

const mutationRefused = "testgate: mutation needs the base the pull request goes into, for example: go run ./tools/testgate mutation origin/dev\n" + usage

// TestReadArgsAcceptsTheFormsOfTheUsageLineAndRefusesTheRest holds every
// command of the usage line: a command another ticket adds gets its rows here.
func TestReadArgsAcceptsTheFormsOfTheUsageLineAndRefusesTheRest(t *testing.T) {
	for _, c := range []struct {
		args    []string
		want    invocation
		refused string // what is printed on stderr, empty when accepted
	}{
		{args: nil, refused: usage},
		{args: []string{"unknown"}, refused: usage},
		{args: []string{"Pyramid"}, refused: usage},
		{args: []string{""}, refused: usage},

		{args: []string{"pyramid"}, want: invocation{"pyramid", []string{"."}, nil}},
		{args: []string{"pyramid", "sub"}, want: invocation{"pyramid", []string{"sub"}, nil}},
		{args: []string{"pyramid", "sub", "extra"}, refused: usage},

		{args: []string{"coverage", "cover.out"}, want: invocation{"coverage", []string{"cover.out"}, nil}},
		{args: []string{"coverage"}, refused: usage},
		{args: []string{"coverage", "cover.out", "extra"}, refused: usage},

		{args: []string{"private"}, want: invocation{"private", []string{}, nil}},
		{args: []string{"private", "a"}, want: invocation{"private", []string{"a"}, nil}},
		{args: []string{"private", "a", "b", "c"}, want: invocation{"private", []string{"a", "b", "c"}, nil}},

		{args: []string{"prepush", "/repo"}, want: invocation{"prepush", []string{"/repo"}, nil}},
		{args: []string{"prepush"}, refused: usage},
		{args: []string{"prepush", "/repo", "extra"}, refused: usage},

		{args: []string{"prepost"}, want: invocation{"prepost", []string{}, nil}},
		{args: []string{"prepost", "extra"}, refused: usage},

		{args: []string{"platform"}, want: invocation{"platform", []string{"."}, nil}},
		{args: []string{"platform", "sub"}, want: invocation{"platform", []string{"sub"}, nil}},
		{args: []string{"platform", "sub", "extra"}, refused: usage},

		{args: []string{"timebound"}, want: invocation{"timebound", []string{"."}, nil}},
		{args: []string{"timebound", "sub"}, want: invocation{"timebound", []string{"sub"}, nil}},
		{args: []string{"timebound", "sub", "extra"}, refused: usage},
		{args: []string{"sees"}, want: invocation{"sees", []string{"."}, nil}},
		{args: []string{"sees", "sub"}, want: invocation{"sees", []string{"sub"}, nil}},
		{args: []string{"sees", "sub", "extra"}, refused: usage},

		{args: []string{"mutation", "origin/dev"}, want: invocation{"mutation", []string{"origin/dev"}, nil}},
		{args: []string{"mutation", "origin/dev", "internal/state", "internal/cli/cursor.go"},
			want: invocation{"mutation", []string{"origin/dev", "internal/state", "internal/cli/cursor.go"}, nil}},
		{args: []string{"mutation"}, refused: mutationRefused},

		{args: []string{"skips", "unit", "out.json"}, want: invocation{"skips", []string{"unit", "out.json"}, nil}},
		{args: []string{"skips", "unit"}, refused: usage},
		{args: []string{"skips", "unit", "out.json", "extra"}, refused: usage},

		{args: []string{"plain", "out.json"}, want: invocation{"plain", []string{"out.json"}, nil}},
		{args: []string{"plain"}, refused: usage},
		{args: []string{"plain", "out.json", "extra"}, refused: usage},

		{args: []string{"load", "-count", "3", "-run", "TestX", "./pkg"},
			want: invocation{"load", []string{"./pkg"}, loadOptions{procs: 1, timeout: loadTimeout, count: 3, run: "TestX"}}},
		{args: []string{"load", "-p", "3", "-tags", "integration", "-timeout", "2m", "-o", "out", "-count", "150", "-run", "^TestX$", "./pkg"},
			want: invocation{"load", []string{"./pkg"}, loadOptions{procs: 3, tags: "integration", timeout: 2 * time.Minute, dir: "out", count: 150, run: "^TestX$"}}},
		{args: []string{"load", "-run=TestX", "-count=1", "-p=1", "./pkg"},
			want: invocation{"load", []string{"./pkg"}, loadOptions{procs: 1, timeout: loadTimeout, count: 1, run: "TestX"}}},
		{args: []string{"load"}, refused: loadRefused("-count is missing")},
		{args: []string{"load", "-count", "3", "./pkg"}, refused: loadRefused("-run is missing")},
		{args: []string{"load", "-run", "TestX", "./pkg"}, refused: loadRefused("-count is missing")},
		{args: []string{"load", "./pkg", "-count", "3", "-run", "TestX"}, refused: loadRefused("-count is missing")},
		{args: []string{"load", "-count", "3", "-run", "TestX"}, refused: usage},
		{args: []string{"load", "-count", "3", "-run", "TestX", "./a", "./b"}, refused: usage},
		{args: []string{"load", "-count", "0", "-run", "TestX", "./pkg"}, refused: loadRefused("-count must be 1 or more")},
		{args: []string{"load", "-count", "-2", "-run", "TestX", "./pkg"}, refused: loadRefused("-count must be 1 or more")},
		{args: []string{"load", "-p", "0", "-count", "3", "-run", "TestX", "./pkg"}, refused: loadRefused("-p must be 1 or more")},
		{args: []string{"load", "-timeout", "0s", "-count", "3", "-run", "TestX", "./pkg"}, refused: loadRefused("-timeout must be longer than 0s")},
		{args: []string{"load", "-timeout", "-1m", "-count", "3", "-run", "TestX", "./pkg"}, refused: loadRefused("-timeout must be longer than 0s")},
		{args: []string{"load", "-count", "3", "-run", "", "./pkg"}, refused: loadRefused("-run must name the tests to run")},
		{args: []string{"load", "-count", "x", "-run", "TestX", "./pkg"},
			refused: loadRefused(`invalid value "x" for flag -count: parse error`)},
		{args: []string{"load", "-x", "-count", "3", "-run", "TestX", "./pkg"}, refused: loadRefused("flag provided but not defined: -x")},
	} {
		got, err := readArgs(c.args)
		switch {
		case c.refused != "":
			if err == nil || err.Error() != c.refused {
				t.Errorf("%q: got %v, err %v; want refused with %q", c.args, got, err, c.refused)
			}
		case err != nil || !reflect.DeepEqual(got, c.want):
			t.Errorf("%q: got %v, err %v; want %v", c.args, got, err, c.want)
		}
	}
}

// A usageForm is what the usage line says a command takes: its least and
// most number of arguments, anyNumber for a list, and its flags, each with
// whether it must be given.
type usageForm struct {
	least, most int
	flags       map[string]bool
}

// usageForms reads the usage line into each command's form: "[-p N]" is a
// flag that may be left out, "-count N" one that must be given, "[ROOT]" an
// argument that may be left out, "[PATH...]" a list and "FILE" an argument.
func usageForms(t *testing.T) map[string]usageForm {
	t.Helper()
	line, ok := strings.CutPrefix(usage, "usage: testgate ")
	if !ok {
		t.Fatalf("usage %q does not start with \"usage: testgate \"", usage)
	}
	forms := map[string]usageForm{}
	for _, f := range strings.Split(line, " | ") {
		words := strings.Fields(f)
		form := usageForm{flags: map[string]bool{}}
		for i := 1; i < len(words); i++ {
			w := words[i]
			switch {
			case strings.HasPrefix(w, "[-"):
				form.flags[w[2:]] = false
				i++ // its value
			case strings.HasPrefix(w, "-"):
				form.flags[w[1:]] = true
				i++ // its value
			case strings.HasPrefix(w, "[") && strings.HasSuffix(w, "...]"):
				form.most = anyNumber
			case strings.HasPrefix(w, "["):
				form.most++
			default:
				form.least++
				form.most++
			}
		}
		forms[words[0]] = form
	}
	return forms
}

func TestUsageFormsReadsFlagsArgumentsAndLists(t *testing.T) {
	got := usageForms(t)
	for name, want := range map[string]usageForm{
		"pyramid":  {most: 1, flags: map[string]bool{}},
		"coverage": {least: 1, most: 1, flags: map[string]bool{}},
		"mutation": {least: 1, most: anyNumber, flags: map[string]bool{}},
		"load": {least: 1, most: 1, flags: map[string]bool{
			"p": false, "tags": false, "timeout": false, "o": false, "count": true, "run": true}},
	} {
		if !reflect.DeepEqual(got[name], want) {
			t.Errorf("%s: got %v, want %v", name, got[name], want)
		}
	}
}

// The check of an optional argument with no default holds for every row: a
// command's flags are no arguments of the row (its least and most count what
// follows them), and a flag left out reads its default from the flag set.
func TestTheUsageLineNamesTheFormsReadArgsReads(t *testing.T) {
	read := map[string]usageForm{}
	for _, f := range forms {
		form := usageForm{least: f.least, most: f.most, flags: map[string]bool{}}
		if f.options != nil {
			o := f.options()
			o.set.VisitAll(func(fl *flag.Flag) { form.flags[fl.Name] = slices.Contains(o.required, fl.Name) })
		}
		read[f.name] = form
		if f.most != anyNumber && f.most > f.least && f.dflt == "" {
			t.Errorf("%s takes an optional argument with no default: its gate would read a value not there", f.name)
		}
	}
	if named := usageForms(t); !reflect.DeepEqual(named, read) {
		t.Errorf("the usage line names %v, readArgs reads %v", named, read)
	}
}

func TestThePackageCommentListsTheFormsOfTheUsageLine(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{}
	for _, l := range strings.Split(string(src), "\n") {
		if rest, ok := strings.CutPrefix(l, "//\tgo run ./tools/testgate "); ok {
			form, _, _ := strings.Cut(rest, "#")
			listed[strings.Join(strings.Fields(form), " ")] = true
		}
	}
	named := map[string]bool{}
	for _, f := range strings.Split(strings.TrimPrefix(usage, "usage: testgate "), " | ") {
		named[f] = true
	}
	if !reflect.DeepEqual(listed, named) {
		t.Errorf("the package comment lists %v, the usage line names %v", listed, named)
	}
}
