package main

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

// noList is what a push without a pattern list is refused with (#185):
// pushing unchecked is the leak the hook is there to stop.
var noList = fmt.Sprintf("no pattern list: set %s or put the list in \"$(git rev-parse --git-common-dir)/%s\" "+
	"(ask the owner for it); git push --no-verify skips this check", PrivatePatternsEnv, PrivatePatternsFile)

// patchArgs make git show print a commit's patch the way scanPatch reads it:
// no message, no context lines, no a/ b/ prefixes, every added path in full
// (no renames), a merge as its change to the first parent, and no tool that
// rewrites a diff.
var patchArgs = []string{"-c", "core.quotePath=false", "show", "--format=", "-p", "--unified=0",
	"--no-prefix", "--no-renames", "--no-color", "--no-ext-diff", "--no-textconv", "--diff-merges=first-parent"}

// checkPush checks what a push sends, given the lines git hands a pre-push
// hook (local ref, local sha, remote ref, remote sha) and the repository
// dir: the remote refs' names, an annotated tag's message, and the commits
// of each updated ref that the remote does not have yet, or, when its tip
// there is unknown here, that no remote-tracking branch has. A deletion
// sends nothing. Without a list the push is refused. The identities of
// author, committer and tagger are not checked: they are the pusher's own.
func checkPush(list, dir string, refs io.Reader) (string, []string, error) {
	pats, err := parsePatterns(list)
	if err != nil {
		return "", nil, err
	}
	if len(pats) == 0 {
		return "private: no pattern list, push refused", []string{noList}, nil
	}
	var hits []hit
	commits := 0
	lines := bufio.NewScanner(refs)
	for lines.Scan() {
		f := strings.Fields(lines.Text())
		if len(f) != 4 {
			continue
		}
		localSHA, remoteRef, remoteSHA := f[1], f[2], f[3]
		hits = append(hits, scanName("ref "+remoteRef, remoteRef, pats)...)
		if isZero(localSHA) {
			continue
		}
		if kind, _ := gitOutput(dir, "cat-file", "-t", localSHA); kind == "tag\n" {
			tag, err := gitOutput(dir, "cat-file", "tag", localSHA)
			if err != nil {
				return "", nil, err
			}
			_, msg, _ := strings.Cut(tag, "\n\n")
			hits = append(hits, scan(short(localSHA)+" tag message", []byte(msg), pats)...)
		}
		rng := []string{localSHA, "--not", "--remotes"}
		if !isZero(remoteSHA) && gitSucceeds(dir, "cat-file", "-e", remoteSHA+"^{commit}") {
			rng = []string{remoteSHA + ".." + localSHA}
		}
		shas, err := gitOutput(dir, append([]string{"rev-list", "--reverse"}, rng...)...)
		if err != nil {
			return "", nil, err
		}
		for _, sha := range strings.Fields(shas) {
			msg, err := gitOutput(dir, "log", "-1", "--format=%B", sha)
			if err != nil {
				return "", nil, err
			}
			patch, err := gitOutput(dir, append(patchArgs, sha)...)
			if err != nil {
				return "", nil, err
			}
			commits++
			hits = append(hits, scanCommit(sha, msg, patch, pats)...)
		}
	}
	if err := lines.Err(); err != nil {
		return "", nil, err
	}
	var problems []string
	for _, h := range hits {
		problems = append(problems, h.String())
	}
	return fmt.Sprintf("private: %d patterns, %d commits, %d matching lines", len(pats), commits, len(hits)), problems, nil
}

func isZero(sha string) bool { return strings.Trim(sha, "0") == "" }

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// scanCommit reports the hits in what a commit publishes: its message, the
// lines its patch adds (numbered as in the new file) and the paths it adds.
// Removed lines are not reported (they were published before, or are
// checked in the commit that added them), nor binary files (checked by eye).
func scanCommit(sha, msg, patch string, pats []*regexp.Regexp) []hit {
	sha = short(sha)
	hits := scan(sha+" message", []byte(strings.TrimSuffix(msg, "\n")), pats)
	return append(hits, scanPatch(sha, patch, pats)...)
}

// scanPatch reads a patch made with patchArgs. Inside a hunk it counts the
// lines its header announces, so an added line that starts with "++" is
// content, not a file header.
func scanPatch(sha, patch string, pats []*regexp.Regexp) []hit {
	var hits []hit
	file := ""
	next, oldLeft, newLeft := 0, 0, 0
	for _, line := range strings.Split(patch, "\n") {
		if oldLeft > 0 || newLeft > 0 {
			switch {
			case strings.HasPrefix(line, "+"):
				hits = append(hits, lineHits(file, next, line[1:], pats)...)
				next++
				newLeft--
			case strings.HasPrefix(line, "-"):
				oldLeft--
			}
			continue
		}
		switch {
		case strings.HasPrefix(line, "+++ "):
			path := strings.TrimSuffix(line[len("+++ "):], "\t")
			if path == "/dev/null" {
				file = ""
				continue
			}
			file = sha + " " + path
			hits = append(hits, scanName(file+" (path)", path, pats)...)
		case strings.HasPrefix(line, "@@ "):
			oldLeft, next, newLeft = hunkHeader(line)
		}
	}
	return hits
}

// lineHits reports the patterns one line matches, as hits on line n of
// where; line 0 stands for a name, not a line. Unlike scan it reads any
// byte, a NUL included: git has already shown the line as text.
func lineHits(where string, n int, text string, pats []*regexp.Regexp) []hit {
	var hits []hit
	for j, re := range pats {
		if re.MatchString(text) {
			hits = append(hits, hit{where, n, j + 1})
		}
	}
	return hits
}

// scanName reports the patterns a name (a ref, a path) matches.
func scanName(where, name string, pats []*regexp.Regexp) []hit {
	return lineHits(where, 0, name, pats)
}

// hunkHeader reads "@@ -a[,b] +c[,d] @@": the old line count, the first new
// line and the new line count (a count left out is 1).
func hunkHeader(line string) (oldCount, newStart, newCount int) {
	f := strings.Fields(line)
	if len(f) < 3 {
		return 0, 0, 0
	}
	_, oldCount = hunkRange(f[1])
	newStart, newCount = hunkRange(f[2])
	return oldCount, newStart, newCount
}

func hunkRange(r string) (start, count int) {
	s, c, ok := strings.Cut(r[1:], ",")
	start, _ = strconv.Atoi(s)
	count = 1
	if ok {
		count, _ = strconv.Atoi(c)
	}
	return start, count
}
