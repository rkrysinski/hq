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
	counts := map[string]int{}
	var shown []mutant
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
			m.status = v
			shown = append(shown, m)
		}
	}
	slices.SortFunc(shown, func(a, b mutant) int {
		return cmp.Or(strings.Compare(a.file, b.file), cmp.Compare(a.line, b.line), cmp.Compare(a.col, b.col), strings.Compare(a.kind, b.kind))
	})
	for _, m := range shown {
		lines = append(lines, fmt.Sprintf("%-10s %s:%d:%d %s", m.status, m.file, m.line, m.col, kindName(m.kind)))
	}
	if len(counts) == 0 {
		return append(lines, noMutantsLine(base, head)), false, nil
	}
	lines = append(lines, fmt.Sprintf("mutation: %d killed, %d alive, %d timed out, %d not covered against %s on %s",
		counts[verdictKilled], counts[verdictAlive], counts[verdictTimedOut], counts[verdictNotCovered], base, head))
	return lines, len(shown) > 0, nil
}

func noMutantsLine(base, head string) string {
	return fmt.Sprintf("mutation: no mutants in the lines changed against %s on %s", base, head)
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

// runMutation runs the mutants of the lines the tree of repo changes against
// its merge base with base, writes the command's output to out and progress
// to progress, and reports whether a mutant is alive or timed out. Every
// file it makes, the build cache included, lives in one temporary directory
// that it removes when it ends, also when it fails or ctx is cancelled.
func runMutation(ctx context.Context, repo, base string, out, progress io.Writer) (failed bool, err error) {
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
	if !hasMutableGo(changed) {
		fmt.Fprintln(out, noMutantsLine(base, head))
		return false, nil
	}

	tmp, err := os.MkdirTemp("", "hq-mutation-")
	if err != nil {
		return false, err
	}
	defer os.RemoveAll(tmp)
	ms, err := runGremlins(ctx, top, mergeBase, tmp, progress)
	if ctx.Err() != nil {
		return false, errInterrupted
	}
	if err != nil {
		return false, err
	}
	lines, failed, err := mutationReport(ms, base, head)
	if err != nil {
		return false, err
	}
	for _, l := range lines {
		fmt.Fprintln(out, l)
	}
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

// runGremlins stages the tree of top in tmp, installs the tool there and
// runs it against mergeBase, and returns the mutants of its report.
func runGremlins(ctx context.Context, top, mergeBase, tmp string, progress io.Writer) ([]mutant, error) {
	env := mutationEnv(os.Environ(), tmp)
	if err := os.Mkdir(filepath.Join(tmp, "tmp"), 0o755); err != nil {
		return nil, err
	}
	src := filepath.Join(tmp, "src")
	fmt.Fprintf(progress, "mutation: staging the tree in %s\n", tmp)
	if err := stageTree(ctx, top, mergeBase, src, filepath.Join(tmp, "git"), env); err != nil {
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
	report := filepath.Join(tmp, "report.json")
	fmt.Fprintln(progress, "mutation: running the unit tests once, then once per mutant")
	start := time.Now()
	out, err := command(ctx, src, env, filepath.Join(tmp, "bin", "gremlins"), gremlinsArgs(config, report, mergeBase)...).CombinedOutput()
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
// src, whose HEAD is mergeBase and whose index holds every copied file. Its
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
