// Package cli is the command line, per doc/cli.md.
//
// It decides nothing about layout or rendering: every flag maps to a
// library option, every diagnostic comes from the engine or the renderer.
// What lives here is argument parsing, the choice of output format, the
// two reports -- a template check and a printout dump -- and the exit code.
//
// Run takes its streams as arguments so that a test drives a whole command
// the way a shell does.
package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/a1s/sr/meta"
)

// Env is the process one run reads and writes.
type Env struct {
	// Args are the arguments after the program name.
	Args []string
	In   io.Reader
	Out  io.Writer
	Err  io.Writer
}

// Exit codes, as doc/cli.md specifies them.
const (
	// ExitOK is success. Warnings do not change it.
	ExitOK = 0
	// ExitFail is a run that did not produce its document.
	ExitFail = 1
	// ExitUsage is a mistake in the command line.
	ExitUsage = 2
)

// usageError is a mistake in the command line rather than in the work.
//
// It exists to separate the two exit codes: a template that will not load
// is the run failing, and a flag that does not exist is the caller failing.
type usageError struct {
	command string
	message string
}

// Error renders the message.
func (err usageError) Error() string { return err.message }

// usagef reports a command-line mistake in command.
func usagef(command, format string, args ...any) error {
	return usageError{command: command, message: fmt.Sprintf(format, args...)}
}

// Run executes one command line and returns the exit code.
func Run(env Env) int {
	err := dispatch(env)
	if err == nil || errors.Is(err, errHelp) {
		return ExitOK
	}
	notes := newStream(env.Err)
	var usage usageError
	if errors.As(err, &usage) {
		notes.line("sr: %s", usage.message)
		if usage.command == "" {
			notes.line("Run \"sr help\" for usage.")
		} else {
			notes.line("Run \"sr help %s\" for usage.", usage.command)
		}
		return ExitUsage
	}
	notes.line("sr: %s", err)
	return ExitFail
}

// dispatch picks the subcommand.
func dispatch(env Env) error {
	if len(env.Args) == 0 {
		return usagef("", "a command is required")
	}
	name, args := env.Args[0], env.Args[1:]
	switch name {
	case "build":
		return cmdBuild(env, args)
	case "validate":
		return cmdValidate(env, args)
	case "render":
		return cmdRender(env, args)
	case "inspect":
		return cmdInspect(env, args)
	case "version", "--version", "-V":
		return version(env, args)
	case "help", "--help", "-h":
		return help(env, args)
	}
	if strings.HasPrefix(name, "-") {
		return usagef("", "unknown flag %q; flags come after the command", name)
	}
	return usagef("", "unknown command %q", name)
}

// version prints the version.
//
// It takes no arguments and says so rather than ignoring them, which is what
// every other command does: an argument a caller expected to matter, dropped
// in silence, is how a wrong document gets built.
func version(env Env, args []string) error {
	out := newStream(env.Out)
	for _, arg := range args {
		if arg == "-h" || arg == "--help" {
			out.printf("%s", usageVersion)
			if out.err != nil {
				return out.err
			}
			return errHelp
		}
	}
	if len(args) > 0 {
		return usagef("version", "version takes no arguments, and got %q", args[0])
	}
	out.line("%s %s", meta.App, meta.Version)
	return out.err
}

// help prints the general usage, or one command's.
func help(env Env, args []string) error {
	out := newStream(env.Out)
	if len(args) == 0 {
		out.printf("%s", usageAll)
		return out.err
	}
	text, ok := usageByCommand[args[0]]
	if !ok {
		return usagef("", "unknown command %q", args[0])
	}
	out.printf("%s", text)
	return out.err
}
