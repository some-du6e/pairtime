// SPDX-License-Identifier: AGPL-3.0-or-later

// Command pairtime is the pairtime client.
//
// main.go handles the built-in version and help. Every other command lives
// in a file of its own and adds itself to the command table in commands.go,
// so a new command is a new file.
package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/some-du6e/pairtime/internal/release"
	"github.com/some-du6e/pairtime/internal/version"
)

// usage() puts the command list between these two.
const usageHead = `pairtime counts the hours you spend building with AI: thinking, steering,
reading, testing and fixing. It skips the hours your agent runs on its own.

Usage:
  pairtime <command>

Commands:
`

const usageTail = `
Start with pairtime install. To see your hours online too, run pairtime login.
Source and status: https://github.com/some-du6e/pairtime
`

func main() {
	os.Exit(start(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// start runs the command line args, with stdin the process's standard
// input. Started with no arguments, pairtime is more often a harness's
// hook than a person's command. cursor-agent loads the hooks install
// writes to Claude Code's settings.json and runs each one's "command"
// without its "args" (internal/install, ClaudeCode), so the binary starts
// alone with the event on a pipe. The usage error it would give then exits
// 2, which Cursor and Claude Code take as blocking the prompt or the tool
// call the hook fired for, and its text could be read as the hook's answer.
// So with no arguments and stdin from a program, pairtime exits 0 and
// prints nothing, as `pairtime hook` does (DESIGN 3.2; CP 6.2). A person
// who types pairtime alone has a terminal on stdin and gets the usage.
func start(args []string, stdin *os.File, stdout, stderr io.Writer) int {
	if len(args) == 0 && fromProgram(stdin) {
		return 0
	}
	return run(args, stdout, stderr)
}

// fromProgram reports whether f, standard input, comes from a program: a
// pipe, a file, or a handle Stat can't read. A terminal is a character
// device, and so is /dev/null.
func fromProgram(f *os.File) bool {
	info, err := f.Stat()
	return err != nil || info.Mode()&os.ModeCharDevice == 0
}

// run executes one command line and returns the process exit code:
// 0 on success, 2 for a usage error, or a status the command documents.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage())
		return 2
	}
	switch args[0] {
	case "version", "--version":
		if len(args) > 1 {
			fmt.Fprintf(stderr, "pairtime: version takes no arguments, got %q\n", args[1:])
			return 2
		}
		fmt.Fprintf(stdout, "pairtime %s\n", version.Get())
		fmt.Fprintln(stdout, releaseKeys())
		return 0
	case "help", "--help", "-h":
		fmt.Fprint(stdout, usage())
		return 0
	default:
		if c, ok := commands[args[0]]; ok {
			return c.run(args[1:], stdout, stderr)
		}
		fmt.Fprintf(stderr, "pairtime: unknown command %q\n\n", args[0])
		fmt.Fprint(stderr, usage())
		return 2
	}
}

// releaseKeys names the release keys compiled into this binary, the only
// keys whose signed update manifests it accepts
// (docs/spec/update-manifest.md).
func releaseKeys() string {
	keys, err := release.TrustedKeys()
	if err != nil {
		return "release keys: unreadable: " + err.Error()
	}
	if len(keys) == 0 {
		return "release keys: none"
	}
	ids := make([]string, len(keys))
	for i, k := range keys {
		ids[i] = k.ID()
	}
	return "release keys: " + strings.Join(ids, ", ")
}
