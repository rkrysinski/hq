package main

import (
	"errors"
	"flag"
	"slices"
)

// anyNumber is the most arguments of a form whose last argument is a list.
const anyNumber = -1

// A form is a command of the usage line and how many arguments it takes,
// after its flags when it takes flags.
type form struct {
	name        string
	least, most int            // most is anyNumber for a list
	dflt        string         // the value of an optional argument not given, "" for none
	missing     string         // the sentence before the usage line when an argument is missing, "" for none
	options     func() options // its flags, nil for a command that takes none
}

// options are the flags of a command: the set that reads them, each flag
// left out reading its default, the flags that must be given, and what the
// flags read once the set has parsed them, or why it is refused.
type options struct {
	set      *flag.FlagSet
	required []string
	read     func() (any, error)
}

// forms are the commands of the usage line, one row each: a command added to
// testgate adds its row here and its form to usage, and a test fails while
// the two differ (args_test.go).
var forms = []form{
	{name: "pyramid", most: 1, dflt: "."},
	{name: "coverage", least: 1, most: 1},
	{name: "private", most: anyNumber},
	{name: "prepush", least: 1, most: 1},
	{name: "prepost"},
	{name: "platform", most: 1, dflt: "."},
	{name: "timebound", most: 1, dflt: "."},
	{name: "sees", most: 1, dflt: "."},
	{name: "mutation", least: 1, most: anyNumber,
		missing: "mutation needs the base the pull request goes into, for example: go run ./tools/testgate mutation origin/dev"},
	{name: "skips", least: 2, most: 2},
	{name: "plain", least: 1, most: 1},
	{name: "load", least: 1, most: 1, options: loadFlags},
}

// An invocation is what a command line asks for: the command and its values,
// an optional argument not given filled in with its default, and what its
// flags read, nil for a command that takes none.
type invocation struct {
	name string
	args []string
	opts any
}

// readArgs reads the arguments after the program's name into the invocation
// they ask for, or refuses them with what to print on stderr: the usage line,
// after the form's own sentence when it has one for a missing argument, or
// after why its flags are refused. It accepts exactly the forms of the usage
// line; a command's flags come before its arguments.
func readArgs(args []string) (invocation, error) {
	if len(args) == 0 {
		return invocation{}, errors.New(usage)
	}
	i := slices.IndexFunc(forms, func(f form) bool { return f.name == args[0] })
	if i < 0 {
		return invocation{}, errors.New(usage)
	}
	f, values := forms[i], slices.Clone(args[1:])
	var opts any
	if f.options != nil {
		o := f.options()
		refused := func(why string) (invocation, error) {
			return invocation{}, errors.New("testgate: " + f.name + ": " + why + "\n" + usage)
		}
		if err := o.set.Parse(values); err != nil {
			return refused(err.Error())
		}
		given := map[string]bool{}
		o.set.Visit(func(fl *flag.Flag) { given[fl.Name] = true })
		for _, name := range o.required {
			if !given[name] {
				return refused("-" + name + " is missing")
			}
		}
		read, err := o.read()
		if err != nil {
			return refused(err.Error())
		}
		opts, values = read, o.set.Args()
	}
	if len(values) < f.least {
		if f.missing != "" {
			return invocation{}, errors.New("testgate: " + f.missing + "\n" + usage)
		}
		return invocation{}, errors.New(usage)
	}
	if f.most != anyNumber && len(values) > f.most {
		return invocation{}, errors.New(usage)
	}
	if len(values) == 0 && f.dflt != "" {
		values = []string{f.dflt}
	}
	return invocation{f.name, values, opts}, nil
}
