package cli

import (
	"fmt"
	"slices"
)

// flagSet is the flags one command declares (command.flags).
type flagSet struct {
	cmd   string
	names []string
}

func (c command) flagSet() flagSet { return flagSet{cmd: c.name, names: c.flags} }

// flagsOf is the flags the command name declares in commands().
func flagsOf(name string) flagSet {
	for _, c := range commands() {
		if c.name == name {
			return c.flagSet()
		}
	}
	return flagSet{cmd: name}
}

// is reports whether argument a is one of flags, each of which the command
// must declare: a parser that recognises a flag its command does not
// declare would take a flag hq help may not name, a mistake of hq's own,
// so it panics, naming both, and that command's tests fail.
func (f flagSet) is(a string, flags ...string) bool {
	for _, flag := range flags {
		if !slices.Contains(f.names, flag) {
			panic(fmt.Sprintf("hq %s recognises %s, which commands() does not declare for it", f.cmd, flag))
		}
	}
	return slices.Contains(flags, a)
}

// The options of hq send, hq wait and hq read, read one way with one
// wording: an option's value follows it as the next argument or after "="
// (optionValue), an option the command does not take is an unknown option,
// and a word it has no place for an unexpected argument; each error names
// the command's usage, given without "usage: ".

// optionValue is the value of option flag, the argument at args[*i]: value
// when it came after "=" (inline), else the next argument, which *i moves
// on to.
func optionValue(args []string, i *int, flag, value string, inline bool, usage string) (string, error) {
	if inline {
		return value, nil
	}
	if *i+1 == len(args) {
		return "", usageErr("%s needs a value (usage: %s)", flag, usage)
	}
	*i++
	return args[*i], nil
}

// unknownOption is the refusal of an option a, which the command of usage
// does not take.
func unknownOption(a, usage string) error {
	return usageErr("unknown option '%s' (usage: %s)", a, usage)
}

// unexpectedArgument is the refusal of a word a, which the command of
// usage has no place for.
func unexpectedArgument(a, usage string) error {
	return usageErr("unexpected argument '%s' (usage: %s)", a, usage)
}
