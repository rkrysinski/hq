package main

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

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

		{args: []string{"pyramid"}, want: invocation{"pyramid", []string{"."}}},
		{args: []string{"pyramid", "sub"}, want: invocation{"pyramid", []string{"sub"}}},
		{args: []string{"pyramid", "sub", "extra"}, refused: usage},

		{args: []string{"coverage", "cover.out"}, want: invocation{"coverage", []string{"cover.out"}}},
		{args: []string{"coverage"}, refused: usage},
		{args: []string{"coverage", "cover.out", "extra"}, refused: usage},

		{args: []string{"private"}, want: invocation{"private", []string{}}},
		{args: []string{"private", "a"}, want: invocation{"private", []string{"a"}}},
		{args: []string{"private", "a", "b", "c"}, want: invocation{"private", []string{"a", "b", "c"}}},

		{args: []string{"prepush", "/repo"}, want: invocation{"prepush", []string{"/repo"}}},
		{args: []string{"prepush"}, refused: usage},
		{args: []string{"prepush", "/repo", "extra"}, refused: usage},

		{args: []string{"prepost"}, want: invocation{"prepost", []string{}}},
		{args: []string{"prepost", "extra"}, refused: usage},

		{args: []string{"platform"}, want: invocation{"platform", []string{"."}}},
		{args: []string{"platform", "sub"}, want: invocation{"platform", []string{"sub"}}},
		{args: []string{"platform", "sub", "extra"}, refused: usage},

		{args: []string{"timebound"}, want: invocation{"timebound", []string{"."}}},
		{args: []string{"timebound", "sub"}, want: invocation{"timebound", []string{"sub"}}},
		{args: []string{"timebound", "sub", "extra"}, refused: usage},

		{args: []string{"mutation", "origin/dev"}, want: invocation{"mutation", []string{"origin/dev"}}},
		{args: []string{"mutation", "origin/dev", "internal/state", "internal/cli/cursor.go"},
			want: invocation{"mutation", []string{"origin/dev", "internal/state", "internal/cli/cursor.go"}}},
		{args: []string{"mutation"}, refused: mutationRefused},
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

// usageForms reads the usage line into each command's least and most number
// of arguments, anyNumber for a list.
func usageForms(t *testing.T) map[string][2]int {
	t.Helper()
	line, ok := strings.CutPrefix(usage, "usage: testgate ")
	if !ok {
		t.Fatalf("usage %q does not start with \"usage: testgate \"", usage)
	}
	forms := map[string][2]int{}
	for _, f := range strings.Split(line, " | ") {
		words := strings.Fields(f)
		least, most := 0, 0
		for _, w := range words[1:] {
			switch {
			case strings.HasPrefix(w, "[") && strings.HasSuffix(w, "...]"):
				most = anyNumber
			case strings.HasPrefix(w, "["):
				most++
			default:
				least++
				most++
			}
		}
		forms[words[0]] = [2]int{least, most}
	}
	return forms
}

func TestTheUsageLineNamesTheFormsReadArgsReads(t *testing.T) {
	read := map[string][2]int{}
	for _, f := range forms {
		read[f.name] = [2]int{f.least, f.most}
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
