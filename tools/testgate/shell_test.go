package main

import (
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

// render writes the commands readShell returns one per line: each word
// quoted, an opaque one with a leading ~, then each redirection as its
// operator followed by its quoted target.
func render(cmds []*simpleCommand) []string {
	quote := func(w shellWord) string {
		if w.opaque {
			return "~" + strconv.Quote(w.text)
		}
		return strconv.Quote(w.text)
	}
	var out []string
	for _, c := range cmds {
		var parts []string
		for _, w := range c.words {
			parts = append(parts, quote(w))
		}
		for _, rd := range c.redirects {
			parts = append(parts, rd.op+quote(rd.target))
		}
		out = append(out, strings.Join(parts, " "))
	}
	return out
}

// read runs readShell and fails the test when it has not returned within
// five seconds.
func read(t *testing.T, src string) []string {
	t.Helper()
	done := make(chan []string, 1)
	go func() { done <- render(readShell(src)) }()
	select {
	case got := <-done:
		return got
	case <-time.After(5 * time.Second):
		t.Fatalf("readShell(%q) did not return within 5s", src)
		return nil
	}
}

func TestTheReaderSplitsCommandsAtEveryOperator(t *testing.T) {
	got := read(t, "a 1;b&c && d||e|f|&g\nh &i # j k\n(l) ; m")
	want := []string{`"a" "1"`, `"b"`, `"c"`, `"d"`, `"e"`, `"f"`, `"g"`, `"h"`, `"i"`, `"l"`, `"m"`}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

func TestTheReaderRemovesQuotesAndEscapes(t *testing.T) {
	got := read(t, `echo '' 'a "b"' "c 'd' \"e\" \$f \x \\ \`+"`"+`" g\ h $'i\n\'j\x' $"k" lm\
n`)
	want := []string{`"echo" "" "a \"b\"" "c 'd' \"e\" $f \\x \\ ` + "`" + `" "g h" "i\n'j\\x" "k" "lmn"`}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

func TestTheReaderMarksWhatItCannotSeeThrough(t *testing.T) {
	got := read(t, "echo $a $z $A $Z $_ $1 $@ ${x} ${}x ${x:- } $(ls) $((1+(2))) `ls` \"$b\" $ $[ $= <(ls) x$")
	want := []string{`"ls"`, `"ls"`, `"ls"`, `"echo" ~"$a" ~"$z" ~"$A" ~"$Z" ~"$_" ~"$1" ~"$@" ~"${x}" ~"${}x" ~"${x:- }" ~"$(ls)" ~"$((1+(2)))" ~"` + "`ls`" + `" ~"$b" "$" "$[" "$=" ~"ls)" "x$"`}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

func TestTheReaderReadsRedirectionsAndFileDescriptors(t *testing.T) {
	got := read(t, "cmd 2>err >>out 3>|x &>all &>>log <in <>rw 2>&1 <&0 12a>f")
	want := []string{`"cmd" "12a" >"err" >>"out" >|"x" &>"all" &>>"log" <"in" <>"rw" >&"1" <&"0" >"f"`}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

func TestTheReaderReadsHeredocBodies(t *testing.T) {
	got := read(t, "cat <<'A' <<-B <<\"C\" <<D\na $x\nA\n\tb\n\tB\nc\nC\nd $x\nD\nnext\ncat <<E\nunterminated")
	want := []string{`"cat" <<"a $x\n" <<-"b\n" <<"c\n" <<~"d $x\n"`, `"next"`, `"cat" <<"unterminated\n"`}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

func TestTheReaderSeesThroughCatOfAHeredocOnly(t *testing.T) {
	got := read(t, "echo \"$(cat <<'EOF'\nbody\nEOF\n)x\" \"$(cat <<< y)z\" \"$(cat < f)\" \"$(cat a <<< b)\" \"$(cat <<< c <<< d)\"")
	want := []string{`"cat" <<"body\n"`, `"cat" <<<"y"`, `"cat" <"f"`, `"cat" "a" <<<"b"`, `"cat" <<<"c" <<<"d"`,
		`"echo" "body\nx" "yz" ~"$(cat < f)" ~"$(cat a <<< b)" ~"$(cat <<< c <<< d)"`}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

func TestTheReaderFindsCommandsInsideSubstitutions(t *testing.T) {
	got := read(t, "x=`echo \\`a\\` b` y")
	want := []string{`"a"`, `"echo" ~"` + "`a`" + `" "b"`, `~"x=` + "`echo \\\\`a\\\\` b`" + `" "y"`}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

func TestTheReaderStopsAtTheEndOfAnUnterminatedText(t *testing.T) {
	for src, want := range map[string][]string{
		`echo 'abc`:    {`"echo" "abc"`},
		`echo "abc`:    {`"echo" "abc"`},
		"echo `ab\\":   {`"ab"`, `"echo" ~"` + "`ab\\\\" + `"`},
		`echo ${abc`:   {`"echo" ~"${abc"`},
		`echo $'abc`:   {`"echo" "abc"`},
		`echo $((1+2`:  {`"echo" ~"$((1+2"`},
		`echo $(ls -l`: {`"ls" "-l"`, `"echo" ~"$(ls -l"`},
		`echo \`:       {`"echo" ""`},
		"echo x \\":    {`"echo" "x" ""`},
		"a\x00b":       {"\"a\\x00b\""},
	} {
		if got := read(t, src); !reflect.DeepEqual(got, want) {
			t.Errorf("%q: got %q, want %q", src, got, want)
		}
	}
}
