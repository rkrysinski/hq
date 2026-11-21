package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strings"
)

// The full build runs its test steps with go test -json (scripts/check.sh,
// #270): skipLines names each test a passed step skipped, and plainOutput
// gives a failed step's output as go test prints it without -json.

// A testEvent is an event of go test -json (go doc test2json).
type testEvent struct {
	Action  string
	Package string
	Test    string
	Output  string
}

// A testLine is a line of go test -json's output: an event, or a line that
// is none (one of the go command's own, a message of the script's).
type testLine struct {
	event *testEvent
	text  string
	test  *goTest // the run of the test the event is of, nil for none
}

// A goTest is a test or a subtest of a run, with what it printed and its
// subtests in the order they came. Each run of a test that go test -count N
// runs N times is a goTest of its own (#276).
type goTest struct {
	name    string
	parent  *goTest
	depth   int // its parents
	items   []testItem
	outcome string // pass, fail or skip; none for a test that never ended
	last    int    // the index of its last event in the run's lines
}

// A testItem is a piece of a test's output or one of its subtests, at the
// place where the subtest started.
type testItem struct {
	text string
	sub  *goTest
}

// A goPackage is a package of a run: its tests and how it ended.
type goPackage struct {
	tests   map[string]*goTest // the latest run of each test
	runs    []*goTest          // every run of a top-level test, in the order they started
	outcome string             // pass, fail or skip (no test files); none for a package that never ended
}

// A testRun is the go test -json output of one run.
type testRun struct {
	lines    []testLine
	packages map[string]*goPackage
	order    []string // the packages, in the order they started
}

// indentStep is how far go test indents a test's log under its name.
const indentStep = "    "

// position is the test file and line go test puts before a test's log.
var position = regexp.MustCompile(`^[^\s:]+\.go:\d+: `)

// readTestRun reads the go test -json output r.
func readTestRun(r io.Reader) (*testRun, error) {
	run := &testRun{packages: map[string]*goPackage{}}
	br := bufio.NewReader(r)
	for {
		s, err := br.ReadString('\n')
		if s != "" {
			s = strings.TrimSuffix(s, "\n")
			var e testEvent
			if json.Unmarshal([]byte(s), &e) == nil && e.Action != "" {
				run.lines = append(run.lines, testLine{event: &e})
			} else {
				run.lines = append(run.lines, testLine{text: s})
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	for i, l := range run.lines {
		e := l.event
		if e == nil || e.Package == "" && e.Test == "" { // a build's events have no package
			continue
		}
		p := run.pkg(e.Package)
		if e.Test == "" {
			if ends(e.Action) {
				p.outcome = e.Action
			}
			continue
		}
		t := p.test(e.Test, e.Action == "run")
		run.lines[i].test = t
		t.last = i
		switch {
		case e.Action == "output":
			t.items = append(t.items, testItem{text: e.Output})
		case ends(e.Action):
			t.outcome = e.Action
		}
	}
	return run, nil
}

// ends reports whether an event's action ends its test or package.
func ends(action string) bool {
	return action == "fail" || passed(action)
}

// passed reports whether a test or package that ended so did not fail.
func passed(outcome string) bool {
	return outcome == "pass" || outcome == "skip"
}

func (run *testRun) pkg(name string) *goPackage {
	p := run.packages[name]
	if p == nil {
		p = &goPackage{tests: map[string]*goTest{}}
		run.packages[name] = p
		run.order = append(run.order, name)
	}
	return p
}

// test returns the latest run of the test of the name, a new one the first
// time and when the event starts the test again after that run ended, under
// the latest run of the nearest of its parents the run has seen.
func (p *goPackage) test(name string, starts bool) *goTest {
	if t := p.tests[name]; t != nil && !(starts && t.outcome != "") {
		return t
	}
	t := &goTest{name: name}
	if !strings.Contains(name, "/") {
		p.runs = append(p.runs, t)
	}
	for i := strings.LastIndex(name, "/"); i != -1; i = strings.LastIndex(name[:i], "/") {
		if parent := p.tests[name[:i]]; parent != nil {
			t.parent, t.depth = parent, parent.depth+1
			parent.items = append(parent.items, testItem{sub: t})
			break
		}
	}
	p.tests[name] = t
	return t
}

// skipLines returns, in the order they came, a line for each test or
// subtest that the go test -json output r skipped: "check: skip <step>
// <package> <test>: <reason>", the reason being what the test printed, on
// one line, without the file and line of each log. A package go test skips
// for having no test files has no line.
func skipLines(step string, r io.Reader) ([]string, error) {
	run, err := readTestRun(r)
	if err != nil {
		return nil, err
	}
	var lines []string
	for _, l := range run.lines {
		if e := l.event; e != nil && e.Test != "" && e.Action == "skip" {
			lines = append(lines, fmt.Sprintf("check: skip %s %s %s: %s", step, e.Package, e.Test, l.test.reason()))
		}
	}
	return lines, nil
}

// reason returns what the test printed, on one line.
func (t *goTest) reason() string {
	var words []string
	for _, l := range splitLines(t.ownText()) {
		if framing(l) || resultLine(l) {
			continue
		}
		if l = position.ReplaceAllString(strings.TrimSpace(l), ""); l != "" {
			words = append(words, l)
		}
	}
	if len(words) == 0 {
		return "no reason given"
	}
	return strings.Join(words, " ")
}

// ownText returns what the test printed, not its subtests.
func (t *goTest) ownText() string {
	var b strings.Builder
	for _, it := range t.items {
		b.WriteString(it.text)
	}
	return b.String()
}

// plainOutput returns the lines that go test prints without -json of the
// run whose go test -json output is r: the build's output and every line
// that is no event where they came; of each package, in the order they
// started, once it and every package before it ended, its result line when
// it passed, or else its output, each failed test with its "--- FAIL" line
// above its log, indented under its parent as without -json, and what a
// test that passed printed itself (not its log); then "FAIL" when a
// package failed. A package or test that never ended (killed by a timeout)
// shows as one that failed.
func plainOutput(r io.Reader) ([]string, error) {
	run, err := readTestRun(r)
	if err != nil {
		return nil, err
	}
	return run.plainLines(), nil
}

// plainLines are the lines of plainOutput of the run.
func (run *testRun) plainLines() []string {
	// What plainOutput has read of each package: its output that is no
	// test's, what go test prints of it when it does not pass, and whether
	// it ended.
	type reading struct {
		own, block strings.Builder
		done       bool
	}
	read := map[string]*reading{}
	for _, name := range run.order {
		read[name] = &reading{}
	}
	var b strings.Builder
	printed := 0 // the packages of run.order printed so far
	flush := func(all bool) {
		for ; printed < len(run.order); printed++ {
			name := run.order[printed]
			p := read[name]
			if !all && !p.done {
				return
			}
			if passed(run.packages[name].outcome) {
				if own := splitLines(p.own.String()); len(own) > 0 {
					b.WriteString(own[len(own)-1] + "\n")
				}
				continue
			}
			b.WriteString(p.block.String())
		}
	}
	for i, l := range run.lines {
		e := l.event
		switch {
		case e == nil:
			b.WriteString(l.text + "\n")
		case e.Action == "build-output":
			b.WriteString(e.Output)
		case e.Package == "" && e.Test == "":
		case e.Test != "":
			if t := l.test; t.parent == nil && i == t.last {
				t.plain(&read[e.Package].block)
			}
		case e.Action == "output":
			p := read[e.Package]
			p.own.WriteString(e.Output)
			p.block.WriteString(e.Output)
		case ends(e.Action):
			read[e.Package].done = true
			flush(false)
		}
	}
	flush(true)
	if slices.ContainsFunc(run.order, func(name string) bool { return run.packages[name].outcome == "fail" }) {
		b.WriteString("FAIL\n")
	}
	return splitLines(b.String())
}

// plain writes what go test prints of the test without -json: when it
// failed, its "--- FAIL" line and its log under it, indented by its depth,
// with its subtests where they started; when it passed or was skipped,
// only what it printed itself, which go test does not indent.
func (t *goTest) plain(b *strings.Builder) {
	failed := !passed(t.outcome)
	if failed {
		for _, l := range splitLines(t.ownText()) {
			if resultLine(l) {
				b.WriteString(indent(t.depth) + strings.TrimLeft(l, " ") + "\n")
				break
			}
		}
	}
	var text strings.Builder
	lines := func() {
		for _, l := range splitLines(text.String()) {
			log := strings.HasPrefix(l, indentStep)
			switch {
			case framing(l) || resultLine(l):
			case failed && log:
				b.WriteString(indent(t.depth+1) + l[len(indentStep):] + "\n")
			case !log:
				b.WriteString(l + "\n")
			}
		}
		text.Reset()
	}
	for _, it := range t.items {
		if it.sub == nil {
			text.WriteString(it.text)
			continue
		}
		lines()
		it.sub.plain(b)
	}
	lines()
}

// framing reports whether a line is one of those go test -json adds to say
// which test runs.
func framing(l string) bool {
	for _, f := range []string{"=== RUN", "=== PAUSE", "=== CONT", "=== NAME"} {
		if strings.HasPrefix(l, f) {
			return true
		}
	}
	return false
}

// resultLine reports whether a line is a test's result line.
func resultLine(l string) bool {
	l = strings.TrimLeft(l, " ")
	return strings.HasPrefix(l, "--- PASS: ") || strings.HasPrefix(l, "--- FAIL: ") || strings.HasPrefix(l, "--- SKIP: ")
}

func indent(depth int) string { return strings.Repeat(indentStep, depth) }

// splitLines splits text into its lines, a last one without its newline
// included.
func splitLines(text string) []string {
	if text == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(text, "\n"), "\n")
}

// testOutput runs skips or plain: it reads the go test -json output of the
// file the invocation names and writes its lines to w.
func testOutput(inv invocation, w io.Writer) error {
	f, err := os.Open(inv.args[len(inv.args)-1])
	if err != nil {
		return err
	}
	defer f.Close()
	var lines []string
	if inv.name == "skips" {
		lines, err = skipLines(inv.args[0], f)
	} else {
		lines, err = plainOutput(f)
	}
	for _, l := range lines {
		fmt.Fprintln(w, l)
	}
	return err
}
