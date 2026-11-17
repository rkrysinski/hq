package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"
)

// The mutation tool: go-gremlins, pinned here and nowhere else, so every
// agent runs the same one and go.mod names no tool (#204).
const (
	gremlinsPackage = "github.com/go-gremlins/gremlins/cmd/gremlins"
	gremlinsVersion = "v0.6.0"
)

// gremlinsTimeoutCoefficient times the tool's first run of the unit tests
// (with coverage, from an empty build cache) is how long one mutant's run
// of the unit tests may take before it counts as timed out. The first run
// builds the whole module from nothing, so it is never answered by Go's test
// cache, and the limit stays well above what a healthy unit suite needs with
// every worker busy.
const gremlinsTimeoutCoefficient = 10

// gremlinsConfig is the tool's whole configuration: the five kinds of
// mutation it makes by default, named so that no configuration file or
// GREMLINS_ variable elsewhere changes them.
const gremlinsConfig = `mutants:
  arithmetic-base: {enabled: true}
  conditionals-boundary: {enabled: true}
  conditionals-negation: {enabled: true}
  increment-decrement: {enabled: true}
  invert-negatives: {enabled: true}
  invert-assignments: {enabled: false}
  invert-bitwise: {enabled: false}
  invert-bwassign: {enabled: false}
  invert-logical: {enabled: false}
  invert-loopctrl: {enabled: false}
  remove-self-assignments: {enabled: false}
`

// errInterrupted is what a run returns when it was stopped from outside.
var errInterrupted = errors.New("interrupted, nothing left behind")

// mutant is one mutant of the tool's report.
type mutant struct {
	file      string
	line, col int
	kind      string // the tool's name, e.g. CONDITIONALS_BOUNDARY
	status    string // the tool's status, e.g. LIVED
}

// The verdicts the command prints.
const (
	verdictKilled     = "killed"
	verdictAlive      = "alive"
	verdictTimedOut   = "timed out"
	verdictNotCovered = "not covered"
	verdictOutside    = "" // a line the change does not touch
)

// verdict maps a status of the tool to the command's own word. A mutant the
// tool cannot build (NOT VIABLE) counts as killed: no test can pass with it.
func verdict(status string) (string, error) {
	switch status {
	case "KILLED", "NOT VIABLE":
		return verdictKilled, nil
	case "LIVED":
		return verdictAlive, nil
	case "TIMED OUT":
		return verdictTimedOut, nil
	case "NOT COVERED":
		return verdictNotCovered, nil
	case "SKIPPED":
		return verdictOutside, nil
	}
	return "", fmt.Errorf("unknown mutant status %q in the report of go-gremlins %s", status, gremlinsVersion)
}

// parseGremlinsReport reads the report the tool writes with --output.
func parseGremlinsReport(r io.Reader) ([]mutant, error) {
	var rep struct {
		Files []struct {
			Name      string `json:"file_name"`
			Mutations []struct {
				Type   string `json:"type"`
				Status string `json:"status"`
				Line   int    `json:"line"`
				Column int    `json:"column"`
			} `json:"mutations"`
		} `json:"files"`
	}
	if err := json.NewDecoder(r).Decode(&rep); err != nil {
		return nil, fmt.Errorf("reading the report of go-gremlins: %w", err)
	}
	var ms []mutant
	for _, f := range rep.Files {
		for _, m := range f.Mutations {
			ms = append(ms, mutant{filepath.ToSlash(f.Name), m.Line, m.Column, m.Type, m.Status})
		}
	}
	return ms, nil
}

// mutationReport is the command's output for the mutants of a run: one line
// per alive and per timed-out mutant, in file and line order, and the last
// line with the counts, the base and the head. failed is true when a mutant
// is alive or timed out. head is HEAD's full sha, with " with uncommitted
// changes" when the tree is not clean, as the full build's last line has it.
func mutationReport(ms []mutant, base, head string) (lines []string, failed bool, err error) {
	return report(ms, base, head, nil, false)
}

// partsReport is the command's output for the mutants of a run in parts:
// mutationReport's, with one line for each changed production file in no
// part, noPart, after the mutants', and their number in the last line,
// also when it is 0. Files in no part never fail the run.
func partsReport(ms []mutant, noPart []string, base, head string) (lines []string, failed bool, err error) {
	return report(ms, base, head, noPart, true)
}

func report(ms []mutant, base, head string, noPart []string, parts bool) (lines []string, failed bool, err error) {
	type survivor struct {
		verdict string
		mutant
	}
	counts := map[string]int{}
	var shown []survivor
	for _, m := range ms {
		v, err := verdict(m.status)
		if err != nil {
			return nil, false, err
		}
		if v == verdictOutside {
			continue
		}
		counts[v]++
		if v == verdictAlive || v == verdictTimedOut {
			shown = append(shown, survivor{v, m})
		}
	}
	slices.SortFunc(shown, func(a, b survivor) int {
		return cmp.Or(strings.Compare(a.file, b.file), cmp.Compare(a.line, b.line), cmp.Compare(a.col, b.col), strings.Compare(a.kind, b.kind))
	})
	for _, s := range shown {
		lines = append(lines, fmt.Sprintf("%-10s %s:%d:%d %s", s.verdict, s.file, s.line, s.col, kindName(s.kind)))
	}
	noPartCount := ""
	if parts {
		for _, f := range noPart {
			lines = append(lines, fmt.Sprintf("%-10s %s", "no part", f))
		}
		noPartCount = fmt.Sprintf(", %d changed files in no part", len(noPart))
		if len(noPart) == 1 {
			noPartCount = ", 1 changed file in no part"
		}
	}
	if len(counts) == 0 {
		return append(lines, fmt.Sprintf("mutation: no mutants in the lines changed%s against %s on %s", noPartCount, base, head)), false, nil
	}
	lines = append(lines, fmt.Sprintf("mutation: %d killed, %d alive, %d timed out, %d not covered%s against %s on %s",
		counts[verdictKilled], counts[verdictAlive], counts[verdictTimedOut], counts[verdictNotCovered], noPartCount, base, head))
	return lines, len(shown) > 0, nil
}

// kindName is how a line names the tool's kind of mutation:
// CONDITIONALS_BOUNDARY is "conditionals boundary".
func kindName(kind string) string {
	return strings.ToLower(strings.ReplaceAll(kind, "_", " "))
}

// hasMutableGo tells whether any of the changed paths is Go code that is not
// a test: only those can hold a mutant.
func hasMutableGo(paths []string) bool {
	for _, p := range paths {
		if strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, "_test.go") {
			return true
		}
	}
	return false
}

// mutationEnv is the environment of every command a run starts inside its
// temporary directory tmp: the build cache, the temporary files and the
// installed tool all live there, Go's own flags and git's and the tool's
// variables from outside are dropped, and colours are off.
func mutationEnv(environ []string, tmp string) []string {
	var env []string
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		switch {
		case strings.HasPrefix(name, "GIT_"), strings.HasPrefix(name, "GREMLINS_"):
			continue
		case name == "GOCACHE", name == "GOFLAGS", name == "GOBIN", name == "TMPDIR", name == "GOTMPDIR", name == "NO_COLOR":
			continue
		}
		env = append(env, kv)
	}
	return append(env,
		"GOCACHE="+filepath.Join(tmp, "cache"),
		"GOBIN="+filepath.Join(tmp, "bin"),
		"TMPDIR="+filepath.Join(tmp, "tmp"),
		"GOFLAGS=",
		"NO_COLOR=1",
	)
}

// gremlinsArgs are the tool's arguments: every mutant is judged by the unit
// tests of the whole module (--integration runs go test ./... for each), a
// line counts as covered when a unit test of any package runs it, and only
// the lines changed against base count.
func gremlinsArgs(config, report, base string) []string {
	return []string{"unleash",
		"--config", config,
		"--integration",
		"--coverpkg", "./...",
		"--diff", base,
		"--timeout-coefficient", fmt.Sprint(gremlinsTimeoutCoefficient),
		"--output", report,
	}
}

// part is a part of a run: the Go files of the repository that one PATH
// names, and only those, get mutants in their changed lines.
type part struct {
	path  string   // the PATH as given
	files []string // its Go files, tests included, from the repository's root
}

// resolveParts makes a part of each PATH, relative to dir: a directory
// names the Go files directly in it, not those of the directories below,
// and a file names itself. files are the repository's files that git tracks
// or would track, from its root top, so an ignored file is in no part.
func resolveParts(top, dir string, paths, files []string) ([]part, error) {
	isGo := map[string]bool{}
	inDir := map[string][]string{}
	for _, f := range files {
		if strings.HasSuffix(f, ".go") {
			isGo[f] = true
			inDir[path.Dir(f)] = append(inDir[path.Dir(f)], f)
		}
	}
	realTop, err := filepath.EvalSymlinks(top)
	if err != nil {
		return nil, err
	}
	coveredBy := map[string]string{}
	var parts []part
	for _, p := range paths {
		abs := p
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(dir, p)
		}
		info, err := os.Stat(abs)
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("the part %s does not exist", p)
		}
		if err != nil {
			return nil, fmt.Errorf("the part %s: %w", p, err)
		}
		resolved, err := filepath.EvalSymlinks(abs)
		if err != nil {
			return nil, fmt.Errorf("the part %s: %w", p, err)
		}
		rel, err := filepath.Rel(realTop, resolved)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("the part %s lies outside the repository %s", p, top)
		}
		rel = filepath.ToSlash(rel)
		var covered []string
		switch {
		case info.IsDir():
			if covered = slices.Clone(inDir[rel]); len(covered) == 0 {
				return nil, fmt.Errorf("the part %s holds no Go file of the repository", p)
			}
			slices.Sort(covered)
		case isGo[rel]:
			covered = []string{rel}
		default:
			return nil, fmt.Errorf("the part %s is not a Go file of the repository", p)
		}
		for _, f := range covered {
			if other, ok := coveredBy[f]; ok {
				return nil, fmt.Errorf("the parts %s and %s both cover %s", other, p, f)
			}
			coveredBy[f] = p
		}
		parts = append(parts, part{p, covered})
	}
	return parts, nil
}

// changedSources are the changed production files of a change, in path
// order: the Go files, not tests, among the changed paths that are among
// the repository's files, so not a deleted one.
func changedSources(changed, files []string) []string {
	exists := map[string]bool{}
	for _, f := range files {
		exists[f] = true
	}
	var sources []string
	for _, f := range changed {
		if strings.HasSuffix(f, ".go") && !strings.HasSuffix(f, "_test.go") && exists[f] {
			sources = append(sources, f)
		}
	}
	slices.Sort(sources)
	return slices.Compact(sources)
}

// inNoPart are the sources that no part covers, in their order.
func inNoPart(sources []string, parts []part) []string {
	var none []string
	for _, f := range sources {
		if !slices.ContainsFunc(parts, func(p part) bool { return slices.Contains(p.files, f) }) {
			none = append(none, f)
		}
	}
	return none
}

// coversAny tells whether the part covers one of the sources: a part that
// does not gets no run of the tool.
func (p part) coversAny(sources []string) bool {
	return slices.ContainsFunc(p.files, func(f string) bool { return slices.Contains(sources, f) })
}

// mutationArgs reads the mutation command's arguments: the base, then the
// PATHs of the parts, if any.
func mutationArgs(args []string) (base string, paths []string, err error) {
	if len(args) == 0 {
		return "", nil, errors.New("mutation needs the base the pull request goes into, for example: go run ./tools/testgate mutation origin/dev")
	}
	return args[0], args[1:], nil
}

// runMutation runs the mutants of the lines the tree of repo changes against
// its merge base with base, writes the command's output to out and progress
// to progress, and reports whether a mutant is alive or timed out. Every
// file it makes, the build cache included, lives in one temporary directory
// that it removes when it ends, also when it fails or ctx is cancelled.
func runMutation(ctx context.Context, repo, base string, out, progress io.Writer) (failed bool, err error) {
	return runMutationParts(ctx, repo, base, nil, out, progress)
}

// runMutationParts is runMutation over the parts that paths, relative to
// repo, name (resolveParts): one after another, in their order, each with
// the mutants of its own files' changed lines and a build cache of its own
// that is removed before the next part starts; then it names the changed
// production files that no part covers (partsReport). Without paths it is
// runMutation.
func runMutationParts(ctx context.Context, repo, base string, paths []string, out, progress io.Writer) (failed bool, err error) {
	top, err := gitOutput(repo, "rev-parse", "--show-toplevel")
	if err != nil {
		return false, fmt.Errorf("%s is not in a git repository", repo)
	}
	top = strings.TrimSpace(top)
	if !gitSucceeds(top, "rev-parse", "--verify", "--quiet", base+"^{commit}") {
		return false, fmt.Errorf("git does not know the base %q: pass the branch the pull request goes into, "+
			"origin/dev for a single issue (git fetch origin dev first)", base)
	}
	mergeBase, err := gitOutput(top, "merge-base", base, "HEAD")
	if err != nil {
		return false, fmt.Errorf("%s and HEAD have no common commit", base)
	}
	mergeBase = strings.TrimSpace(mergeBase)
	head, err := gitOutput(top, "rev-parse", "HEAD")
	if err != nil {
		return false, err
	}
	head = strings.TrimSpace(head)
	if status, err := gitOutput(top, "status", "--porcelain"); err != nil {
		return false, err
	} else if status != "" {
		head += " with uncommitted changes"
	}
	changed, err := changedPaths(top, mergeBase)
	if err != nil {
		return false, err
	}
	printLines := func(lines []string) {
		for _, l := range lines {
			fmt.Fprintln(out, l)
		}
	}

	var toRun []part
	var noPart []string
	if len(paths) == 0 {
		if !hasMutableGo(changed) {
			lines, _, err := mutationReport(nil, base, head)
			printLines(lines)
			return false, err
		}
	} else {
		files, err := repoFiles(top)
		if err != nil {
			return false, err
		}
		dir, err := filepath.Abs(repo)
		if err != nil {
			return false, err
		}
		parts, err := resolveParts(top, dir, paths, files)
		if err != nil {
			return false, err
		}
		sources := changedSources(changed, files)
		noPart = inNoPart(sources, parts)
		for _, p := range parts {
			if p.coversAny(sources) {
				toRun = append(toRun, p)
			} else {
				fmt.Fprintf(progress, "mutation: the part %s changes no production file, nothing to run\n", p.path)
			}
		}
		if len(toRun) == 0 {
			lines, _, err := partsReport(nil, noPart, base, head)
			printLines(lines)
			return false, err
		}
	}

	tmp, err := os.MkdirTemp("", "hq-mutation-")
	if err != nil {
		return false, err
	}
	defer os.RemoveAll(tmp)
	ms, err := runGremlins(ctx, top, mergeBase, tmp, toRun, progress)
	if ctx.Err() != nil {
		return false, errInterrupted
	}
	if err != nil {
		return false, err
	}
	var lines []string
	if len(paths) == 0 {
		lines, failed, err = mutationReport(ms, base, head)
	} else {
		lines, failed, err = partsReport(ms, noPart, base, head)
	}
	if err != nil {
		return false, err
	}
	printLines(lines)
	return failed, nil
}

// changedPaths lists the paths the working tree of top changes against the
// commit mergeBase, uncommitted and untracked files included.
func changedPaths(top, mergeBase string) ([]string, error) {
	diff, err := gitOutput(top, "diff", "-z", "--name-only", "--no-renames", mergeBase)
	if err != nil {
		return nil, err
	}
	untracked, err := gitOutput(top, "ls-files", "-z", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	return strings.Split(diff+untracked, "\x00"), nil
}

// repoFiles lists the files of top's working tree that git tracks or would
// track, from its root: not the ignored ones, nor a tracked file that is
// deleted.
func repoFiles(top string) ([]string, error) {
	list, err := gitOutput(top, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	var files []string
	for _, f := range strings.Split(list, "\x00") {
		if f == "" {
			continue
		}
		if _, err := os.Lstat(filepath.Join(top, f)); err == nil {
			files = append(files, f)
		}
	}
	return files, nil
}

// runGremlins stages the tree of top in tmp, installs the tool there and
// runs it against mergeBase, and returns the mutants of its report. With
// parts it runs the tool once for each part, in their order, each run in a
// directory of its own under tmp, which holds its build cache and
// temporary files and is removed before the next part starts; a part sees
// only its own files changed (partCommit).
func runGremlins(ctx context.Context, top, mergeBase, tmp string, parts []part, progress io.Writer) ([]mutant, error) {
	env := mutationEnv(os.Environ(), tmp)
	if err := os.Mkdir(filepath.Join(tmp, "tmp"), 0o755); err != nil {
		return nil, err
	}
	src, gitDir := filepath.Join(tmp, "src"), filepath.Join(tmp, "git")
	fmt.Fprintf(progress, "mutation: staging the tree in %s\n", tmp)
	if err := stageTree(ctx, top, mergeBase, src, gitDir, env); err != nil {
		return nil, fmt.Errorf("staging the tree: %w", err)
	}
	fmt.Fprintf(progress, "mutation: installing go-gremlins %s\n", gremlinsVersion)
	if out, err := command(ctx, tmp, env, "go", "install", gremlinsPackage+"@"+gremlinsVersion).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("installing go-gremlins %s: %v\n%s", gremlinsVersion, err, out)
	}
	config := filepath.Join(tmp, "gremlins.yaml")
	if err := os.WriteFile(config, []byte(gremlinsConfig), 0o644); err != nil {
		return nil, err
	}
	if len(parts) == 0 {
		return runTool(ctx, src, tmp, config, mergeBase, env, progress)
	}
	// The install's cache is no part's: the disk a run needs is that of
	// its largest part.
	if err := os.RemoveAll(filepath.Join(tmp, "cache")); err != nil {
		return nil, err
	}
	var ms []mutant
	for i, p := range parts {
		fmt.Fprintf(progress, "mutation: part %d of %d, %s\n", i+1, len(parts), p.path)
		dir := filepath.Join(tmp, fmt.Sprintf("part-%d", i+1))
		partEnv := mutationEnv(os.Environ(), dir)
		if err := os.MkdirAll(filepath.Join(dir, "tmp"), 0o755); err != nil {
			return nil, err
		}
		ref, err := partCommit(ctx, src, gitDir, dir, mergeBase, p.files, partEnv)
		if err != nil {
			return nil, fmt.Errorf("the part %s: %w", p.path, err)
		}
		got, err := runTool(ctx, src, dir, config, ref, partEnv, progress)
		if err != nil {
			return nil, fmt.Errorf("the part %s: %w", p.path, err)
		}
		ms = append(ms, got...)
		if err := os.RemoveAll(dir); err != nil {
			return nil, err
		}
	}
	return ms, nil
}

// runTool runs the installed tool in src against the commit diffRef, with
// its report in dir, and returns the mutants of the report.
func runTool(ctx context.Context, src, dir, config, diffRef string, env []string, progress io.Writer) ([]mutant, error) {
	report := filepath.Join(dir, "report.json")
	gremlins := filepath.Join(filepath.Dir(src), "bin", "gremlins")
	fmt.Fprintln(progress, "mutation: running the unit tests once, then once per mutant")
	start := time.Now()
	out, err := command(ctx, src, env, gremlins, gremlinsArgs(config, report, diffRef)...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("go-gremlins failed (do the unit tests pass?): %v\n%s", err, out)
	}
	fmt.Fprintf(progress, "mutation: done in %s\n", time.Since(start).Round(time.Second))
	f, err := os.Open(report)
	if errors.Is(err, fs.ErrNotExist) && strings.Contains(string(out), "No results to report") {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("go-gremlins wrote no report: %v\n%s", err, out)
	}
	defer f.Close()
	return parseGremlinsReport(f)
}

// partCommit makes the staging repository's HEAD a commit, on top of
// mergeBase, that holds every staged file as it is in the working tree,
// except the part's files and the files the tree deletes, which are as
// they are in mergeBase (absent when mergeBase has none), and returns it:
// the tool, which mutates the lines changed against the commit it is
// given, then sees only the part's files changed. A deleted file is put
// back so that git still finds a renamed file's source, and the part gets
// a mutant only in its lines that changed, as the run over the whole tree
// does. The index it builds the commit from is a copy, in dir.
func partCommit(ctx context.Context, src, gitDir, dir, mergeBase string, files []string, env []string) (string, error) {
	index := filepath.Join(dir, "index")
	if err := copyEntry(filepath.Join(gitDir, "index"), index); err != nil {
		return "", err
	}
	env = append(slices.Clip(env), "GIT_INDEX_FILE="+index,
		"GIT_AUTHOR_NAME=testgate", "GIT_AUTHOR_EMAIL=testgate@localhost",
		"GIT_COMMITTER_NAME=testgate", "GIT_COMMITTER_EMAIL=testgate@localhost")
	gitIn := func(stdin string, args ...string) (string, error) {
		cmd := command(ctx, src, env, "git", args...)
		cmd.Stdin = strings.NewReader(stdin)
		out, err := cmd.Output()
		if err != nil {
			return "", fmt.Errorf("git %s: %v", args[0], err)
		}
		return strings.TrimSpace(string(out)), nil
	}
	git := func(args ...string) (string, error) { return gitIn("", args...) }
	deleted, err := git("diff", "-z", "--cached", "--name-only", "--no-renames", "--diff-filter=D", mergeBase)
	if err != nil {
		return "", err
	}
	reset := strings.Join(files, "\x00") + "\x00" + deleted
	if _, err := gitIn(reset, "--literal-pathspecs", "reset", "-q", "--pathspec-from-file=-", "--pathspec-file-nul", mergeBase); err != nil {
		return "", err
	}
	tree, err := git("write-tree")
	if err != nil {
		return "", err
	}
	commit, err := git("commit-tree", tree, "-p", mergeBase, "-m", "the files outside the part as changed")
	if err != nil {
		return "", err
	}
	if _, err := git("update-ref", "HEAD", commit); err != nil {
		return "", err
	}
	return commit, nil
}

// command is a command of a run: in its own process group, so that a
// cancelled ctx ends it with every process it started (go test and the test
// binaries), not only the first.
func command(ctx context.Context, dir string, env []string, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 5 * time.Second
	return cmd
}

// stageTree makes src a copy of the files of top's working tree that git
// tracks or would track (not the ignored ones, so neither other worktrees
// nor build output), with a git repository of its own in gitDir, outside
// src, whose HEAD is mergeBase, whose index holds every copied file and
// whose diffs have no context lines, so the tool sees every changed line. Its
// objects are top's, borrowed through an alternate, so nothing of top's git
// data is copied; the tool copies src once per worker, and src holds only a
// one-line .git file.
func stageTree(ctx context.Context, top, mergeBase, src, gitDir string, env []string) error {
	common := gitCommonDir(top)
	if common == "" {
		return fmt.Errorf("no git directory for %s", top)
	}
	list, err := gitOutput(top, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return err
	}
	git := func(args ...string) error {
		if out, err := command(ctx, src, env, "git", args...).CombinedOutput(); err != nil {
			return fmt.Errorf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return nil
	}
	if err := os.MkdirAll(src, 0o755); err != nil {
		return err
	}
	if err := git("init", "-q", "--separate-git-dir", gitDir, src); err != nil {
		return err
	}
	// The tool reads each hunk of git diff as one run of added lines, from
	// its first after the leading context: a changed line after an
	// unchanged one in the same hunk would get no mutant. With no context
	// lines every hunk is one run of changed lines.
	if err := git("config", "diff.context", "0"); err != nil {
		return err
	}
	alternates := filepath.Join(gitDir, "objects", "info", "alternates")
	if err := os.WriteFile(alternates, []byte(filepath.Join(common, "objects")+"\n"), 0o644); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, rel := range strings.Split(list, "\x00") {
		if rel == "" || seen[rel] || strings.HasSuffix(rel, "/") { // a trailing slash is a nested repository
			continue
		}
		seen[rel] = true
		if err := copyEntry(filepath.Join(top, rel), filepath.Join(src, rel)); err != nil {
			return err
		}
	}
	if err := git("update-ref", "HEAD", mergeBase); err != nil {
		return err
	}
	return git("add", "--all", "--force")
}

// copyEntry copies one file of the working tree: a regular file with its
// mode, a symlink as a symlink. A path that is gone (deleted, not yet
// staged) or is a directory (a submodule) is left out.
func copyEntry(from, to string) error {
	info, err := os.Lstat(from)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	switch {
	case info.Mode()&fs.ModeSymlink != 0:
		target, err := os.Readlink(from)
		if err != nil {
			return err
		}
		return os.Symlink(target, to)
	case info.Mode().IsRegular():
		data, err := os.ReadFile(from)
		if err != nil {
			return err
		}
		return os.WriteFile(to, data, info.Mode().Perm())
	}
	return nil
}
