package cli

import (
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/a1s/sr"
)

// newFlags builds a flag set that reports errors instead of exiting,
// and whose help is this package's rather than the flag package's.
func newFlags(name string) *flag.FlagSet {
	set := flag.NewFlagSet(name, flag.ContinueOnError)
	set.SetOutput(io.Discard)
	set.Usage = func() {}
	return set
}

// parse reads the flags and returns the positional arguments.
//
// Flags may follow positional arguments, which the flag package on its own
// does not allow: it stops at the first argument that is not a flag, so
// "sr render out.srp.jsonl -o out.pdf" would leave -o unparsed.
// Each round takes one positional and parses on from there.
//
// A -h or --help anywhere prints the command's help and returns errHelp.
//
// Because parsing resumes after each positional, "--" ends flag parsing only
// as far as the next argument: in "sr validate -- a.kdl -t b.kdl" the -t is
// still read as a flag. No command here takes an argument that could start
// with a dash -- a path that does is the one case, and it is spelled ./-x --
// so the general end-of-flags marker is not worth a second parser.
func parse(env Env, set *flag.FlagSet, args []string) ([]string, error) {
	var rest []string
	for {
		if err := set.Parse(args); err != nil {
			if err == flag.ErrHelp {
				out := newStream(env.Out)
				out.printf("%s", usageByCommand[set.Name()])
				if out.err != nil {
					return nil, out.err
				}
				return nil, errHelp
			}
			return nil, usageError{command: set.Name(), message: err.Error()}
		}
		if set.NArg() == 0 {
			return rest, nil
		}
		rest = append(rest, set.Arg(0))
		args = set.Args()[1:]
	}
}

// errHelp means the help was printed and there is nothing left to do.
var errHelp = fmt.Errorf("help printed")

// stringFlag defines a string flag under a long name and a short alias.
//
// Both names write to the same variable, which is how "-t" and "--template"
// become one flag rather than two that can disagree.
func stringFlag(set *flag.FlagSet, target *string, long, short, help string) {
	set.StringVar(target, long, *target, help)
	if short != "" {
		set.StringVar(target, short, *target, help)
	}
}

// boolFlag defines a boolean flag under a long name and a short alias.
func boolFlag(set *flag.FlagSet, target *bool, long, short, help string) {
	set.BoolVar(target, long, *target, help)
	if short != "" {
		set.BoolVar(target, short, *target, help)
	}
}

// paramList collects repeated --param NAME=VALUE flags.
type paramList struct {
	values map[string]string
}

// String renders the flag's value for the flag package.
func (list *paramList) String() string {
	if list == nil || len(list.values) == 0 {
		return ""
	}
	names := make([]string, 0, len(list.values))
	for name := range list.values {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, name+"="+list.values[name])
	}
	return strings.Join(parts, ",")
}

// Set records one NAME=VALUE pair.
//
// A name given twice is an error rather than a last-wins, because a repeated
// flag in a generated command line is a mistake worth hearing about, and the
// value that would have won is not the one the caller wrote first.
func (list *paramList) Set(text string) error {
	name, value, ok := strings.Cut(text, "=")
	if !ok || name == "" {
		return fmt.Errorf("--param expects NAME=VALUE, and got %q", text)
	}
	if list.values == nil {
		list.values = map[string]string{}
	}
	if _, seen := list.values[name]; seen {
		return fmt.Errorf("--param %s given twice", name)
	}
	list.values[name] = value
	return nil
}

// names lists the parameter names in a fixed order,
// so that two runs of one command line behave identically.
func (list *paramList) names() []string {
	names := make([]string, 0, len(list.values))
	for name := range list.values {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// paramFlag registers the repeatable --param flag.
func paramFlag(set *flag.FlagSet, list *paramList) {
	set.Var(list, "param", "a report parameter, NAME=VALUE")
}

// checkParams refuses a --param that names nothing the template declares.
//
// A misspelled name is the mistake a generated command line actually makes,
// and it is the one that would otherwise pass: the value goes nowhere, the
// report builds with the default in its place, and nothing says so. The
// engine refuses it too; what this adds is the exit code, since a name that
// does not exist is a mistake in the command line rather than in the work --
// and a check, which binds leniently, would not reach the engine's refusal.
func checkParams(command string, list *paramList, info sr.Info) error {
	declared := make(map[string]bool, len(info.Parameters))
	names := make([]string, 0, len(info.Parameters))
	for _, param := range info.Parameters {
		declared[param.Name] = true
		names = append(names, param.Name)
	}
	var unknown []string
	for _, name := range list.names() {
		if !declared[name] {
			unknown = append(unknown, "--param "+name)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	if len(names) == 0 {
		return usagef(command, "%s names no parameter, and the template declares none",
			strings.Join(unknown, ", "))
	}
	return usagef(command, "%s names no parameter; the template declares %s",
		strings.Join(unknown, ", "), strings.Join(names, ", "))
}
