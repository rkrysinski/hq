package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// The prepost gate (#231) runs as Claude Code's PreToolUse hook on a Bash
// command (scripts/claude-hooks/prepost): it finds the text the command
// would post to GitHub through gh and checks it against the same pattern
// list as the private gate, before anything is posted. The text is what a
// posting gh command is given in a flag (--title, --body, -f body=...) and
// in the files and heredocs it reads (--body-file, -F body=@file, --input).
// A text the gate cannot see (a variable, $(cat file), a pipe, a file that
// is not a regular one, does not exist yet or that the same command writes)
// is refused, and so is every posting command without a list: nothing goes
// out unchecked. It
// reads the command as written and runs nothing, so a gh command hidden in
// a variable or a script file is not seen: it stops an accident, not
// someone set on getting around it.

// noPostList is what a posting command is refused with without a list.
var noPostList = "no pattern list: " + listWhere + "; a text that has to go out anyway is posted by the operator outside Claude Code"

// postRefused ends the output of a refused command.
const postRefused = "refused: take the private detail out (write <org>, ~/work/app and the like, docs/hq/testing.md, Private details), " +
	"or put a text the hook cannot see in a file it can; a text that has to go out anyway is posted by the operator outside Claude Code"

// hookInput is what Claude Code hands a PreToolUse hook, as far as the gate
// reads it.
type hookInput struct {
	Cwd       string `json:"cwd"`
	ToolInput struct {
		Command string `json:"command"`
	} `json:"tool_input"`
}

// source is one text a command posts: written in the command (text) or read
// from a file (path), or why the gate cannot see it (unseen). Its name is
// what a finding calls it.
type source struct {
	name   string
	text   string
	path   string
	unseen string
}

// checkPost checks the Bash command of a PreToolUse hook's input against the
// list and returns a summary and a problem for every matching line and every
// text it cannot see. A command that posts nothing passes silently, with or
// without a list. home is what ~ stands for.
func checkPost(list string, input io.Reader, home string) (string, []string, error) {
	var in hookInput
	if err := json.NewDecoder(input).Decode(&in); err != nil {
		return "", nil, fmt.Errorf("the hook's input is not a tool call's JSON: %v", err)
	}
	sources := postedSources(in.ToolInput.Command, in.Cwd, home)
	if len(sources) == 0 {
		return "", nil, nil
	}
	pats, err := parsePatterns(list)
	if err != nil {
		return "", nil, err
	}
	if len(pats) == 0 {
		return "prepost: no pattern list, refused", []string{noPostList}, nil
	}
	var problems []string
	matching := 0
	for _, s := range sources {
		body := []byte(s.text)
		if s.path != "" {
			body, s.unseen = readPosted(s.path)
		}
		if s.unseen != "" {
			problems = append(problems, s.name+": "+s.unseen)
			continue
		}
		for _, h := range scan(s.name, body, pats) {
			problems = append(problems, h.String())
			matching++
		}
	}
	return fmt.Sprintf("prepost: %d patterns, %d texts, %d matching lines", len(pats), len(sources), matching), problems, nil
}

// readPosted returns the text of the file at path, or why the gate cannot
// read it before gh does. Only a regular file is read: a pipe or a device
// would not hold the text gh will read, or would block the hook.
func readPosted(path string) ([]byte, string) {
	info, err := os.Stat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, "does not exist yet: write it first, then post it in a second command"
	case err == nil && !info.Mode().IsRegular():
		return nil, "is not a regular file, which the hook cannot read ahead of gh"
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, "cannot be read: " + err.Error()
	}
	return body, ""
}

// postedSources returns the texts command, run in the directory cwd, would
// post through gh.
func postedSources(command, cwd, home string) []source {
	cmds := readShell(command)
	dirs := make([]string, len(cmds))
	written := map[string]bool{} // the files the command writes
	dir := cwd
	for i, c := range cmds {
		dirs[i] = dir
		for _, w := range writtenFiles(c) {
			if p, ok := resolve(w, dir, home); ok {
				written[p] = true
			}
		}
		if len(c.words) > 0 && (c.words[0].text == "cd" || c.words[0].text == "pushd") {
			dir = cdTarget(c.words[1:], dir, home)
		}
	}
	var sources []source
	for i, c := range cmds {
		sources = append(sources, commandSources(c, dirs[i], home, written)...)
	}
	return sources
}

// writtenFiles returns the words that name a file the command writes: the
// target of a redirection that writes, every file tee names, the last file
// cp, mv, install, ln or rsync names, and every file sed or perl names with
// -i. The gate runs nothing, so a file another program changes is checked
// as it was before.
func writtenFiles(c *simpleCommand) []shellWord {
	var files []shellWord
	for _, rd := range c.redirects {
		if rd.writes() {
			files = append(files, rd.target)
		}
	}
	for j, w := range c.words {
		var args []shellWord
		inPlace := false
		for _, a := range c.words[j+1:] {
			switch {
			case strings.HasPrefix(a.text, "--in-place"):
				inPlace = true
			case strings.HasPrefix(a.text, "--"):
			case strings.HasPrefix(a.text, "-"):
				inPlace = inPlace || strings.Contains(a.text, "i")
			default:
				args = append(args, a)
			}
		}
		switch filepath.Base(w.text) {
		case "tee":
			return append(files, args...)
		case "cp", "mv", "install", "ln", "rsync":
			return append(files, args[max(0, len(args)-1):]...)
		case "sed", "perl":
			if inPlace {
				return append(files, args...)
			}
			return files
		}
	}
	return files
}

// cdTarget is the directory cd with args leads to from dir, or "" when the
// gate cannot tell.
func cdTarget(args []shellWord, dir, home string) string {
	for _, a := range args {
		if a.text == "-" {
			return ""
		}
		if strings.HasPrefix(a.text, "-") {
			continue
		}
		p, _ := resolve(a, dir, home)
		return p
	}
	return home
}

// resolve returns the absolute path a word names from dir, unless the gate
// cannot tell it.
func resolve(w shellWord, dir, home string) (string, bool) {
	p := w.text
	switch {
	case w.opaque:
		return "", false
	case p == "~" || strings.HasPrefix(p, "~/"):
		if home == "" {
			return "", false
		}
		return filepath.Join(home, p[1:]), true
	case strings.HasPrefix(p, "~"): // another user's home
		return "", false
	case filepath.IsAbs(p):
		return filepath.Clean(p), true
	case dir == "":
		return "", false
	}
	return filepath.Join(dir, p), true
}

// shells are the programs whose -c argument is read as a command.
var shells = map[string]bool{"bash": true, "sh": true, "zsh": true, "dash": true, "ksh": true}

// commandSources returns the texts one simple command posts: those of the
// gh command it runs, also behind a wrapper (timeout 30 gh ...) or through
// a shell's -c or heredoc or eval. The words after the first of these are
// its own.
func commandSources(c *simpleCommand, dir, home string, written map[string]bool) []source {
	for i, w := range c.words {
		switch {
		case w.text == "gh" || strings.HasSuffix(w.text, "/gh"):
			return ghSources(c, c.words[i+1:], dir, home, written)
		case shells[filepath.Base(w.text)]:
			if script, ok := shellScript(c.words[i+1:]); ok {
				return postedSources(script, dir, home)
			}
			for _, rd := range c.redirects {
				if rd.feedsText() { // bash <<'EOF' runs its heredoc
					return postedSources(rd.target.text, dir, home)
				}
			}
			return nil
		case w.text == "eval":
			var script []string
			for _, a := range c.words[i+1:] {
				script = append(script, a.text)
			}
			return postedSources(strings.Join(script, " "), dir, home)
		}
	}
	return nil
}

// shellScript returns the command a shell's arguments give it with -c.
func shellScript(args []shellWord) (string, bool) {
	sawC := false
	for i := 0; i < len(args); i++ {
		a := args[i].text
		switch {
		case a == "-o" || a == "+o":
			i++
		case strings.HasPrefix(a, "--"):
		case strings.HasPrefix(a, "-"):
			sawC = sawC || strings.Contains(a, "c")
		case sawC:
			return a, true
		default:
			return "", false
		}
	}
	return "", false
}

// postingSubcommands are the gh subcommands that post a text, beside gh api.
// scripts/claude-hooks/prepost names the same ones to tell which commands
// start the gate.
var postingSubcommands = map[string]map[string]bool{
	"pr":      {"create": true, "new": true, "edit": true, "comment": true, "review": true, "merge": true, "close": true},
	"issue":   {"create": true, "new": true, "edit": true, "comment": true, "close": true},
	"release": {"create": true, "new": true, "edit": true},
}

type flagKind int

const (
	textFlag  flagKind = iota // its value is the text
	fileFlag                  // its value names the file the text is read from, - for standard input
	fieldFlag                 // gh api -F key=value: the value is the text, @file reads it from a file
)

var subcommandFlags = map[string]flagKind{
	"--title": textFlag, "-t": textFlag, "--body": textFlag, "-b": textFlag, "--comment": textFlag, "-c": textFlag,
	"--notes": textFlag, "-n": textFlag, "--subject": textFlag,
	"--body-file": fileFlag, "--notes-file": fileFlag, "-F": fileFlag,
}

// reviewFlags are those of gh pr review, where -c and --comment choose the
// kind of review and take no text.
var reviewFlags = func() map[string]flagKind {
	m := map[string]flagKind{}
	for name, kind := range subcommandFlags {
		if name != "-c" && name != "--comment" {
			m[name] = kind
		}
	}
	return m
}()

var apiFlags = map[string]flagKind{"-f": textFlag, "--raw-field": textFlag, "-F": fieldFlag, "--field": fieldFlag, "--input": fileFlag}

// ghSources returns the texts a gh command with args posts.
func ghSources(c *simpleCommand, args []shellWord, dir, home string, written map[string]bool) []source {
	if len(args) == 0 {
		return nil
	}
	call, flags := "gh api", apiFlags
	switch {
	case args[0].text == "api":
		args = args[1:]
		if method, ok := flagValue(args, map[string]flagKind{"-X": textFlag, "--method": textFlag}); ok && strings.EqualFold(method.text, "GET") {
			return nil
		}
	case postingSubcommands[args[0].text] != nil:
		group := args[0].text
		args = args[1:]
		for n := repoFlag(args); n > 0; n = repoFlag(args) { // gh pr -R owner/repo create
			args = args[n:]
		}
		if len(args) == 0 || !postingSubcommands[group][args[0].text] {
			return nil
		}
		call, flags = "gh "+group+" "+args[0].text, subcommandFlags
		if call == "gh pr review" {
			flags = reviewFlags
		}
		args = args[1:]
	default:
		return nil
	}
	var sources []source
	for i := 0; i < len(args) && args[i].text != "--"; i++ {
		name, kind, value, attached := splitFlag(args[i], flags)
		if name == "" {
			continue
		}
		if !attached {
			if i+1 == len(args) {
				break // gh refuses a flag without its value
			}
			i++
			value = args[i]
		}
		label := call + " " + name
		switch {
		case kind == textFlag:
			sources = append(sources, textSource(label, value))
		case kind == fieldFlag && strings.Contains(value.text, "=@"):
			_, file, _ := strings.Cut(value.text, "=@")
			sources = append(sources, fileSource(call, shellWord{text: file, opaque: value.opaque}, c, dir, home, written))
		case kind == fieldFlag:
			sources = append(sources, textSource(label, value))
		default:
			sources = append(sources, fileSource(call, value, c, dir, home, written))
		}
	}
	return sources
}

// repoFlag returns how many of the leading words of args are a -R or --repo
// flag with its value.
func repoFlag(args []shellWord) int {
	switch t := args[0:min(1, len(args))]; {
	case len(t) == 0:
		return 0
	case (t[0].text == "-R" || t[0].text == "--repo") && len(args) > 1:
		return 2
	case strings.HasPrefix(t[0].text, "--repo=") || strings.HasPrefix(t[0].text, "-R") && len(t[0].text) > 2:
		return 1
	}
	return 0
}

// flagValue returns the value of the first of flags in args.
func flagValue(args []shellWord, flags map[string]flagKind) (shellWord, bool) {
	for i, a := range args {
		if name, _, value, attached := splitFlag(a, flags); name != "" {
			if attached {
				return value, true
			}
			if i+1 < len(args) {
				return args[i+1], true
			}
		}
	}
	return shellWord{}, false
}

// splitFlag reports whether w is one of flags, as --name, --name=value, -x,
// -xvalue or -yx (y a flag without a value), and returns its value when it
// is attached.
func splitFlag(w shellWord, flags map[string]flagKind) (string, flagKind, shellWord, bool) {
	t := w.text
	if strings.HasPrefix(t, "--") {
		name, value, attached := strings.Cut(t, "=")
		if kind, ok := flags[name]; ok {
			return name, kind, shellWord{text: value, opaque: w.opaque}, attached
		}
		return "", 0, shellWord{}, false
	}
	for j := 1; strings.HasPrefix(t, "-") && j < len(t); j++ {
		if kind, ok := flags["-"+t[j:j+1]]; ok {
			value := t[j+1:]
			return "-" + t[j:j+1], kind, shellWord{text: value, opaque: w.opaque}, value != ""
		}
	}
	return "", 0, shellWord{}, false
}

// textSource is a text written in the command.
func textSource(name string, w shellWord) source {
	if w.opaque {
		return source{name: name, unseen: "holds $..., $(...) or `...`, whose text the hook cannot see: write the text to a file and pass the file (--body-file, -F body=@file)"}
	}
	return source{name: name, text: w.text}
}

// stdinNames are the names gh reads standard input by.
var stdinNames = map[string]bool{"-": true, "/dev/stdin": true, "/dev/fd/0": true, "/proc/self/fd/0": true}

// fileSource is a text the gh command call reads from the file w names, or
// from its standard input for - and the like.
func fileSource(call string, w shellWord, c *simpleCommand, dir, home string, written map[string]bool) source {
	if !stdinNames[w.text] || w.opaque {
		return pathSource(w, dir, home, written)
	}
	for i := len(c.redirects) - 1; i >= 0; i-- {
		switch rd := c.redirects[i]; {
		case rd.feedsText():
			return textSource(call+" stdin", rd.target)
		case rd.op == "<":
			return pathSource(rd.target, dir, home, written)
		}
	}
	return source{name: call + " stdin", unseen: "reads its text from standard input, which here is not a heredoc or a file: pass the text in a file"}
}

// pathSource is the text of the file w names.
func pathSource(w shellWord, dir, home string, written map[string]bool) source {
	p, ok := resolve(w, dir, home)
	switch {
	case w.opaque:
		return source{name: w.text, unseen: "names its file with $..., $(...) or `...`, which the hook cannot see: write the path out"}
	case !ok:
		return source{name: w.text, unseen: "is a relative path in a directory the hook cannot tell: give the full path"}
	case written[p]:
		return source{name: w.text, unseen: "is written by this same command: write it first, then post it in a second command"}
	}
	return source{name: w.text, path: p}
}
