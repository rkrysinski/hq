package main

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// PrivatePatternsEnv names the variable that holds the patterns of private
// details (#127): one regular expression per line, matched ignoring case;
// blank lines and lines starting with # are skipped. The list lives only
// there (a CI secret, a local shell profile), never in a tracked file, so
// the check does not publish what it looks for.
const PrivatePatternsEnv = "HQ_PRIVATE_PATTERNS"

// hit is a line that matches a pattern. It names the pattern by its number,
// counting patterns only (not blank or comment lines), and never repeats the
// text: CI logs of a public repository are public.
type hit struct {
	file    string
	line    int
	pattern int
}

func (h hit) String() string {
	return fmt.Sprintf("%s:%d matches private pattern %d", h.file, h.line, h.pattern)
}

func parsePatterns(list string) ([]*regexp.Regexp, error) {
	var pats []*regexp.Regexp
	for _, l := range strings.Split(list, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		re, err := regexp.Compile("(?i)" + l)
		if err != nil {
			// Not err: it quotes the expression.
			return nil, fmt.Errorf("%s: pattern %d is not a valid regular expression", PrivatePatternsEnv, len(pats)+1)
		}
		pats = append(pats, re)
	}
	return pats, nil
}

// scan reports every line of a text file that matches a pattern. Binary
// content (a NUL byte) is skipped: images are checked by eye.
func scan(name string, body []byte, pats []*regexp.Regexp) []hit {
	if bytes.IndexByte(body, 0) >= 0 {
		return nil
	}
	var hits []hit
	for i, line := range strings.Split(string(body), "\n") {
		for j, re := range pats {
			if re.MatchString(line) {
				hits = append(hits, hit{name, i + 1, j + 1})
			}
		}
	}
	return hits
}

// scanPaths scans files and, recursively, directories, leaving out .git.
func scanPaths(paths []string, pats []*regexp.Regexp) ([]hit, error) {
	var hits []hit
	for _, p := range paths {
		err := filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.Name() == ".git" { // a directory, or a worktree's file
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if !d.Type().IsRegular() {
				return nil
			}
			body, err := os.ReadFile(path)
			if errors.Is(err, fs.ErrNotExist) { // tracked, deleted, not yet staged
				return nil
			}
			if err != nil {
				return err
			}
			hits = append(hits, scan(path, body, pats)...)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return hits, nil
}

// checkPrivate scans paths, or the tracked files under the current
// directory when there are none, against the pattern list, and returns a
// summary line and a problem for every matching line. An empty list checks
// nothing and passes.
func checkPrivate(list string, paths []string) (string, []string, error) {
	pats, err := parsePatterns(list)
	if err != nil {
		return "", nil, err
	}
	if len(pats) == 0 {
		return fmt.Sprintf("private: %s is not set, nothing checked", PrivatePatternsEnv), nil, nil
	}
	if len(paths) == 0 {
		if paths, err = trackedFiles("."); err != nil {
			return "", nil, err
		}
	}
	hits, err := scanPaths(paths, pats)
	if err != nil {
		return "", nil, err
	}
	var problems []string
	for _, h := range hits {
		problems = append(problems, h.String())
	}
	return fmt.Sprintf("private: %d patterns, %d matching lines", len(pats), len(hits)), problems, nil
}

// trackedFiles lists the files git tracks under root, as paths under root.
func trackedFiles(root string) ([]string, error) {
	cmd := exec.Command("git", "ls-files", "-z")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %v", err)
	}
	var files []string
	for _, f := range strings.Split(strings.TrimRight(string(out), "\x00"), "\x00") {
		if f != "" {
			files = append(files, filepath.Join(root, f))
		}
	}
	return files, nil
}
