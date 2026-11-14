package main

import (
	"bufio"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// noList is what a push without a pattern list is refused with (#185):
// pushing unchecked is the leak the hook is there to stop.
var noList = fmt.Sprintf("no pattern list: set %s or put the list in \"$(git rev-parse --git-common-dir)/%s\" "+
	"(ask the owner for it); git push --no-verify skips this check", PrivatePatternsEnv, PrivatePatternsFile)

// logArgs make git log print what scanLog reads, oldest commit first: no
// context lines, no a/ b/ prefixes, every added path in full (no renames), a
// merge as its change to the first parent, and no tool that rewrites a diff.
var logArgs = []string{"-c", "core.quotePath=false", "log", "--reverse", "--format=%x01%H%n%B%x02",
	"-p", "--unified=0", "--no-prefix", "--no-renames", "--no-color", "--no-ext-diff", "--no-textconv",
	"--diff-merges=first-parent"}

// checkPush checks what a push sends, given the lines git hands a pre-push
// hook (local ref, local sha, remote ref, remote sha) and the repository
// dir: the remote refs' names, and the commits of each updated ref that the
// remote does not have yet, or, when its tip there is unknown here, that no
// remote-tracking branch has. A deletion sends nothing. Without a list the
// push is refused.
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
		hits = append(hits, scan("ref "+remoteRef, []byte(remoteRef), pats)...)
		if isZero(localSHA) {
			continue
		}
		rng := []string{localSHA, "--not", "--remotes"}
		if !isZero(remoteSHA) && git(dir, "cat-file", "-e", remoteSHA+"^{commit}") == nil {
			rng = []string{remoteSHA + ".." + localSHA}
		}
		cmd := exec.Command("git", append(append(append([]string{}, logArgs...), rng...), "--")...)
		cmd.Dir = dir
		out, err := cmd.Output()
		if err != nil {
			return "", nil, fmt.Errorf("git log %s: %v", strings.Join(rng, " "), err)
		}
		commits += strings.Count(string(out), "\x01")
		hits = append(hits, scanLog(string(out), pats)...)
	}
	if err := lines.Err(); err != nil {
		return "", nil, err
	}
	for i := range hits { // a name, not a line
		if strings.HasPrefix(hits[i].file, "ref ") {
			hits[i].line = 0
		}
	}
	var problems []string
	for _, h := range hits {
		problems = append(problems, h.String())
	}
	return fmt.Sprintf("private: %d patterns, %d commits, %d matching lines", len(pats), commits, len(hits)), problems, nil
}

func isZero(sha string) bool { return strings.Trim(sha, "0") == "" }

func git(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	return cmd.Run()
}

// scanLog reports the hits in what a push publishes, given git log's output
// for the commits it sends (logArgs): each commit's message, the lines its
// patch adds (numbered as in the new file) and the paths it adds. Removed
// lines are not reported (they were published before, or are checked in the
// commit that added them), nor binary files (checked by eye).
func scanLog(log string, pats []*regexp.Regexp) []hit {
	var hits []hit
	for _, rec := range strings.Split(log, "\x01")[1:] {
		head, patch, _ := strings.Cut(rec, "\x02")
		sha, msg, _ := strings.Cut(head, "\n")
		if len(sha) > 7 {
			sha = sha[:7]
		}
		hits = append(hits, scan(sha+" message", []byte(strings.TrimSuffix(msg, "\n")), pats)...)
		hits = append(hits, scanPatch(sha, patch, pats)...)
	}
	return hits
}

// scanPatch reads a patch made with no context lines and no prefixes. Inside
// a hunk it counts the lines its header announces, so an added line that
// starts with "++" is content, not a file header.
func scanPatch(sha, patch string, pats []*regexp.Regexp) []hit {
	var hits []hit
	file := ""
	next, oldLeft, newLeft := 0, 0, 0
	for _, line := range strings.Split(patch, "\n") {
		if oldLeft > 0 || newLeft > 0 {
			switch {
			case strings.HasPrefix(line, "+"):
				for _, h := range scan(file, []byte(line[1:]), pats) {
					h.line = next
					hits = append(hits, h)
				}
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
			for _, h := range scan(file+" (path)", []byte(path), pats) {
				h.line = 0
				hits = append(hits, h)
			}
		case strings.HasPrefix(line, "@@ "):
			oldLeft, next, newLeft = hunkHeader(line)
		}
	}
	return hits
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
