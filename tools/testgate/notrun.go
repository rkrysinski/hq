package main

import (
	"bufio"
	"cmp"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"path"
	"slices"
	"strconv"
	"strings"
)

// Not run (#275): a changed line of Go code, not a test, that holds a
// statement no test at any level runs, in the tests' own processes or in a
// program of the module a test builds and starts. The lines come from the
// same diff the mutants come from; whether a line ran comes from coverage
// profiles of the tests (levels.go).

// lineRef is a line of a file of the repository, from its root.
type lineRef struct {
	file string
	line int
}

// srcPos is a line and a column of a file, as a coverage profile counts
// them: from 1, the column in bytes.
type srcPos struct{ line, col int }

func (p srcPos) before(q srcPos) bool {
	return p.line < q.line || p.line == q.line && p.col < q.col
}

// judgedFile tells whether a changed file of the repository is one whose
// lines are judged: Go code that is not a test and that the go command
// builds, so not under a testdata directory nor one whose name starts with
// _ or ., which ./... leaves out.
func judgedFile(file string) bool {
	if !strings.HasSuffix(file, ".go") || strings.HasSuffix(file, "_test.go") {
		return false
	}
	dirs := strings.Split(path.Dir(file), "/")
	return !slices.ContainsFunc(dirs, func(d string) bool {
		return d == "testdata" || strings.HasPrefix(d, "_") || strings.HasPrefix(d, ".") && d != "."
	})
}

// addedLines reads the output of git diff with no context lines (-U0) and
// the prefixes a/ and b/, and returns, for each file it judges
// (judgedFile), the lines the new side adds, in order. A deleted file adds
// none. A file's +++ line is read only in its header, before its first
// hunk: an added line whose text starts with "++ " looks the same.
func addedLines(diff string) (map[string][]int, error) {
	added := map[string][]int{}
	file, header := "", false
	for _, l := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(l, "diff --git "):
			file, header = "", true
		case header && strings.HasPrefix(l, "+++ "):
			name := strings.TrimPrefix(l, "+++ ")
			if strings.HasPrefix(name, `"`) {
				unquoted, err := strconv.Unquote(name)
				if err != nil {
					return nil, fmt.Errorf("git diff: a file name it quotes that cannot be read: %s", name)
				}
				name = unquoted
			}
			if rel, ok := strings.CutPrefix(name, "b/"); ok && judgedFile(rel) {
				file = rel
			}
		case strings.HasPrefix(l, "@@ "):
			header = false
			if file == "" {
				continue
			}
			_, first, count := hunkHeader(l)
			for n := first; n < first+count; n++ {
				added[file] = append(added[file], n)
			}
		}
	}
	return added, nil
}

// statementLines returns, for each line of the Go source src that holds a
// statement, the srcPos by which a coverage profile tells whether that
// statement ran: where it starts, for a case or a select's case right after
// its colon, where the profile's block of its body starts. A statement holds
// the lines from its start to its end; a statement with a body (if, for,
// switch, select, a case) only those up to the body's opening brace or its
// colon, and a line inside a function literal or a nested statement belongs
// to the innermost statement that holds it. A block on its own holds no
// line, nor does an empty statement; a declaration outside a function is no
// statement.
func statementLines(filename string, src []byte) (map[int]srcPos, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	lines := map[int]srcPos{}
	at := func(p token.Pos) srcPos {
		pos := fset.Position(p)
		return srcPos{pos.Line, pos.Column}
	}
	hold := func(from, to token.Pos, by srcPos) {
		for l := fset.Position(from).Line; l <= fset.Position(to).Line; l++ {
			lines[l] = by
		}
	}
	var visit func(n ast.Node) bool
	visit = func(n ast.Node) bool {
		s, ok := n.(ast.Stmt)
		if !ok {
			return true
		}
		switch s := s.(type) {
		case *ast.BlockStmt, *ast.EmptyStmt:
		case *ast.IfStmt:
			hold(s.Pos(), s.Body.Lbrace, at(s.Pos()))
		case *ast.ForStmt:
			hold(s.Pos(), s.Body.Lbrace, at(s.Pos()))
		case *ast.RangeStmt:
			hold(s.Pos(), s.Body.Lbrace, at(s.Pos()))
		case *ast.SwitchStmt:
			hold(s.Pos(), s.Body.Lbrace, at(s.Pos()))
		case *ast.TypeSwitchStmt:
			hold(s.Pos(), s.Body.Lbrace, at(s.Pos()))
		case *ast.SelectStmt:
			hold(s.Pos(), s.Body.Lbrace, at(s.Pos()))
		case *ast.CaseClause:
			hold(s.Pos(), s.Colon, at(s.Colon+1))
		case *ast.CommClause:
			hold(s.Pos(), s.Colon, at(s.Colon+1))
			// Its send or receive is part of the case: no block of the
			// profile holds it, the block of the case's body tells.
			for _, body := range s.Body {
				ast.Inspect(body, visit)
			}
			return false
		case *ast.LabeledStmt:
			hold(s.Pos(), s.Colon, at(s.Pos()))
		default:
			hold(s.Pos(), s.End(), at(s.Pos()))
		}
		return true
	}
	ast.Inspect(f, visit)
	return lines, nil
}

// coverBlock is a block of a coverage profile: a file by its import path,
// and where the block starts and ends. Whether a run counted it is its
// value in ranBlocks.
type coverBlock struct {
	file       string
	start, end srcPos
}

// ranBlocks is what the profiles read so far say ran: every block they
// list, true when one of them counted it. Profiles of several runs and
// programs list the same block when they build the same source.
type ranBlocks map[coverBlock]bool

// read adds the blocks of a profile in go test -coverprofile's text form,
// or go tool covdata textfmt's, to c.
func (c ranBlocks) read(r io.Reader) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "mode:") {
			continue
		}
		b, count, err := profileLine(line)
		if err != nil {
			return err
		}
		c[b] = c[b] || count > 0
	}
	return sc.Err()
}

// profileLine reads a line of a profile: file:l.c,l.c statements count.
func profileLine(line string) (coverBlock, int, error) {
	bad := fmt.Errorf("a coverage profile line it cannot read: %q", line)
	f := strings.Fields(line)
	if len(f) != 3 {
		return coverBlock{}, 0, bad
	}
	i := strings.LastIndexByte(f[0], ':')
	if i < 0 {
		return coverBlock{}, 0, bad
	}
	var b coverBlock
	b.file = f[0][:i]
	if _, err := fmt.Sscanf(f[0][i+1:], "%d.%d,%d.%d", &b.start.line, &b.start.col, &b.end.line, &b.end.col); err != nil {
		return coverBlock{}, 0, bad
	}
	if _, err := strconv.Atoi(f[1]); err != nil {
		return coverBlock{}, 0, bad
	}
	count, err := strconv.Atoi(f[2])
	if err != nil {
		return coverBlock{}, 0, bad
	}
	return b, count, nil
}

// ran tells whether the statement of file (an import path and a file name)
// at p ran: whether a profile counted the innermost block that holds p,
// the one that starts last, and of those the one that ends first. No block
// holds p of a file no profile lists: a file of another platform, or one
// no test builds.
func (c ranBlocks) ran(file string, p srcPos) bool {
	var inner *coverBlock
	for b := range c {
		if b.file != file || p.before(b.start) || b.end.before(p) {
			continue
		}
		if inner == nil || inner.start.before(b.start) || inner.start == b.start && b.end.before(inner.end) {
			inner = &b
		}
	}
	return inner != nil && c[*inner]
}

// notRunLines are the changed lines (added, per file of the repository)
// that hold a statement (statements, per file) no profile of c counted, in
// file and line order. module is the module's path, which a profile puts
// before a file's directory.
func notRunLines(added map[string][]int, statements map[string]map[int]srcPos, module string, c ranBlocks) []lineRef {
	var notRun []lineRef
	for file, lines := range added {
		for _, l := range lines {
			p, ok := statements[file][l]
			if ok && !c.ran(module+"/"+file, p) {
				notRun = append(notRun, lineRef{file, l})
			}
		}
	}
	slices.SortFunc(notRun, func(a, b lineRef) int {
		return cmp.Or(strings.Compare(a.file, b.file), cmp.Compare(a.line, b.line))
	})
	return slices.Compact(notRun)
}
