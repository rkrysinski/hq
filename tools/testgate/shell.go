package main

import (
	"regexp"
	"strings"
)

// A small reader of the shell commands Claude Code's Bash tool runs, for the
// prepost gate (#231). It finds every simple command a command line holds,
// also inside $(...), `...`, <(...) and subshells, with its words, its
// redirections and the bodies of its heredocs. It runs and expands nothing:
// a word keeps a parameter or a command substitution as written and is
// marked opaque, since the gate cannot see what the shell would put there.
// The one substitution it sees through is $(cat <<EOF ... EOF), the way
// Claude Code writes a pull request body: the word holds the heredoc's body.

// shellWord is one word of a command, its quotes removed.
type shellWord struct {
	text   string
	opaque bool // holds an expansion whose text the reader cannot see
}

// redirect is one redirection of a command: its operator (">", ">>", "<",
// "<<", "<<<", ...) and its target, which for a heredoc is its body.
type redirect struct {
	op     string
	target shellWord
}

// simpleCommand is a command name with its arguments and redirections.
type simpleCommand struct {
	words     []shellWord
	redirects []*redirect
}

// heredoc is a heredoc whose body follows the next newline.
type heredoc struct {
	r      *redirect
	delim  string
	strip  bool // <<-: leading tabs are removed
	quoted bool // the delimiter was quoted: the body is not expanded
}

type shellReader struct {
	src     string
	pos     int
	cmds    []*simpleCommand
	pending []heredoc
}

// readShell returns the simple commands of src in the order they end, a
// command inside a substitution before the command that holds it.
func readShell(src string) []*simpleCommand {
	r := &shellReader{src: src}
	r.list(0)
	return r.cmds
}

func (r *shellReader) peek(off int) byte {
	if r.pos+off < len(r.src) {
		return r.src[r.pos+off]
	}
	return 0
}

// list reads commands up to the byte stop (0 for the end of the source),
// which it consumes.
func (r *shellReader) list(stop byte) {
	cmd := &simpleCommand{}
	end := func() {
		if len(cmd.words) > 0 || len(cmd.redirects) > 0 {
			r.cmds = append(r.cmds, cmd)
		}
		cmd = &simpleCommand{}
	}
	for r.pos < len(r.src) {
		c := r.src[r.pos]
		switch {
		case stop != 0 && c == stop:
			r.pos++
			end()
			return
		case c == ' ' || c == '\t' || c == '\r':
			r.pos++
		case c == '\\' && r.peek(1) == '\n':
			r.pos += 2
		case c == '\n':
			r.pos++
			end()
			r.heredocBodies()
		case c == '#':
			for r.pos < len(r.src) && r.src[r.pos] != '\n' {
				r.pos++
			}
		case c == ';' || c == ')':
			r.pos++
			end()
		case c == '&' && r.peek(1) == '>':
			r.redirect(cmd)
		case c == '&':
			r.pos++
			if r.peek(0) == '&' {
				r.pos++
			}
			end()
		case c == '|':
			r.pos++
			if r.peek(0) == '|' || r.peek(0) == '&' {
				r.pos++
			}
			end()
		case c == '(':
			r.pos++
			end()
			r.list(')')
		case c == '<' || c == '>':
			r.redirect(cmd)
		default:
			start := r.pos
			w := r.word()
			if (r.peek(0) == '<' || r.peek(0) == '>') && isDigits(r.src[start:r.pos]) {
				r.redirect(cmd) // 2>file: the digits name a file descriptor
				continue
			}
			cmd.words = append(cmd.words, w)
		}
	}
	end()
}

func isDigits(s string) bool {
	return s != "" && strings.Trim(s, "0123456789") == ""
}

// redirect reads a redirection at the current position into cmd.
func (r *shellReader) redirect(cmd *simpleCommand) {
	op := ""
	for _, o := range []string{"&>>", "&>", "<<<", "<<-", "<<", "<>", "<&", "<(", "<", ">>", ">|", ">&", ">(", ">"} {
		if strings.HasPrefix(r.src[r.pos:], o) {
			op = o
			break
		}
	}
	r.pos += len(op)
	if op == "<(" || op == ">(" { // a process substitution is a word
		raw := r.substitution()
		cmd.words = append(cmd.words, shellWord{text: raw, opaque: true})
		return
	}
	for r.peek(0) == ' ' || r.peek(0) == '\t' {
		r.pos++
	}
	start := r.pos
	target := r.word()
	rd := &redirect{op: op, target: target}
	cmd.redirects = append(cmd.redirects, rd)
	if op == "<<" || op == "<<-" {
		quoted := strings.ContainsAny(r.src[start:r.pos], `'"\`)
		r.pending = append(r.pending, heredoc{r: rd, delim: target.text, strip: op == "<<-", quoted: quoted})
		rd.target = shellWord{}
	}
}

// heredocBodies reads the bodies of the pending heredocs, which start at
// the current position, the line after their redirections.
func (r *shellReader) heredocBodies() {
	for _, h := range r.pending {
		var body strings.Builder
		for r.pos < len(r.src) {
			line, rest, _ := strings.Cut(r.src[r.pos:], "\n")
			r.pos = len(r.src) - len(rest)
			if h.strip {
				line = strings.TrimLeft(line, "\t")
			}
			if line == h.delim {
				break
			}
			body.WriteString(line + "\n")
		}
		text := body.String()
		h.r.target = shellWord{text: text, opaque: !h.quoted && expansion.MatchString(text)}
	}
	r.pending = nil
}

// expansion matches a parameter or a command substitution in a text the
// shell expands: the body of an unquoted heredoc.
var expansion = regexp.MustCompile("`|\\$[A-Za-z_{(@*#?$!0-9-]")

// isNameStart reports whether c starts a parameter's name.
func isNameStart(c byte) bool {
	return c == '_' || 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z'
}

// word reads one word at the current position.
func (r *shellReader) word() shellWord {
	var b strings.Builder
	opaque := false
	for r.pos < len(r.src) {
		c := r.src[r.pos]
		switch c {
		case ' ', '\t', '\r', '\n', ';', '&', '|', '(', ')', '<', '>':
			return shellWord{text: b.String(), opaque: opaque}
		case '\\':
			if r.peek(1) != '\n' && r.pos+1 < len(r.src) {
				b.WriteByte(r.src[r.pos+1])
			}
			r.pos = min(r.pos+2, len(r.src))
		case '\'':
			end := strings.IndexByte(r.src[r.pos+1:], '\'')
			if end < 0 {
				end = len(r.src) - r.pos - 1
			}
			b.WriteString(r.src[r.pos+1 : r.pos+1+end])
			r.pos = min(r.pos+end+2, len(r.src))
		case '"':
			r.pos++
			opaque = r.doubleQuoted(&b) || opaque
		case '$':
			opaque = r.dollar(&b) || opaque
		case '`':
			b.WriteString(r.backtick())
			opaque = true
		default:
			b.WriteByte(c)
			r.pos++
		}
	}
	return shellWord{text: b.String(), opaque: opaque}
}

// doubleQuoted reads up to the closing double quote, the opening one read,
// and reports whether it held an expansion.
func (r *shellReader) doubleQuoted(b *strings.Builder) bool {
	opaque := false
	for r.pos < len(r.src) {
		c := r.src[r.pos]
		switch {
		case c == '"':
			r.pos++
			return opaque
		case c == '\\' && strings.IndexByte("$`\"\\\n", r.peek(1)) >= 0:
			if r.peek(1) != '\n' {
				b.WriteByte(r.peek(1))
			}
			r.pos += 2
		case c == '$':
			opaque = r.dollar(b) || opaque
		case c == '`':
			b.WriteString(r.backtick())
			opaque = true
		default:
			b.WriteByte(c)
			r.pos++
		}
	}
	return opaque
}

// dollar reads what starts with $ at the current position into b and
// reports whether it is an expansion the reader cannot see through.
func (r *shellReader) dollar(b *strings.Builder) bool {
	start := r.pos
	switch c := r.peek(1); {
	case c == '(' && r.peek(2) == '(': // arithmetic
		r.pos += 3
		for depth := 2; r.pos < len(r.src) && depth > 0; r.pos++ {
			switch r.src[r.pos] {
			case '(':
				depth++
			case ')':
				depth--
			}
		}
	case c == '(':
		r.pos += 2
		first := len(r.cmds)
		raw := r.substitution()
		if body, ok := catHeredoc(r.cmds[first:]); ok {
			b.WriteString(body.text)
			return body.opaque
		}
		b.WriteString("$(" + raw)
		return true
	case c == '{':
		r.pos += 2
		end := strings.IndexByte(r.src[r.pos:], '}')
		if end < 0 {
			end = len(r.src) - r.pos - 1
		}
		r.pos += end + 1
	case c == '\'':
		r.pos += 2
		r.ansiQuoted(b)
		return false
	case c == '"':
		r.pos++ // $"..." is read as "..."
		return false
	case isNameStart(c) || strings.IndexByte("@*#?$!-0123456789", c) >= 0:
		r.pos += 2 // the rest of a name reads as plain text: the word is opaque all the same
	default:
		b.WriteByte('$')
		r.pos++
		return false
	}
	b.WriteString(r.src[start:r.pos])
	return true
}

// substitution reads the commands of a substitution up to its closing
// parenthesis, the opening one read, and returns its source from there.
func (r *shellReader) substitution() string {
	start := r.pos
	r.list(')')
	return r.src[start:r.pos]
}

// catHeredoc reports whether the commands of a substitution are just cat
// fed by a heredoc or a here-string, and returns that text.
func catHeredoc(cmds []*simpleCommand) (shellWord, bool) {
	if len(cmds) != 1 || len(cmds[0].redirects) != 1 || len(cmds[0].words) != 1 || cmds[0].words[0].text != "cat" {
		return shellWord{}, false
	}
	rd := cmds[0].redirects[0]
	if rd.op != "<<" && rd.op != "<<-" && rd.op != "<<<" {
		return shellWord{}, false
	}
	return rd.target, true
}

// ansiQuoted reads a $'...' string, the opening quote read.
func (r *shellReader) ansiQuoted(b *strings.Builder) {
	escapes := map[byte]string{'n': "\n", 't': "\t", '\\': "\\", '\'': "'", '"': "\""}
	for r.pos < len(r.src) {
		c := r.src[r.pos]
		if c == '\'' {
			r.pos++
			return
		}
		if e, ok := escapes[r.peek(1)]; c == '\\' && ok {
			b.WriteString(e)
			r.pos += 2
			continue
		}
		b.WriteByte(c)
		r.pos++
	}
}

// backtick reads a `...` substitution, its commands included, and returns
// its source.
func (r *shellReader) backtick() string {
	start := r.pos
	r.pos++
	var inner strings.Builder
	for r.pos < len(r.src) && r.src[r.pos] != '`' {
		if r.src[r.pos] == '\\' && r.pos+1 < len(r.src) {
			r.pos++
		}
		inner.WriteByte(r.src[r.pos])
		r.pos++
	}
	r.pos = min(r.pos+1, len(r.src))
	r.cmds = append(r.cmds, readShell(inner.String())...)
	return r.src[start:r.pos]
}
