// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"fmt"
	"io"
	"slices"
	"strings"
)

// command is one `pairtime <name>` subcommand. Each lives in its own file
// and registers itself from an init function, so a work package that adds a
// command adds a file, and leaves main.go and the other commands alone
// (docs/PLAN.md, WP-AG1). A command with subcommands of its own, such as
// `fixtures scrub`, registers its first word and parses the rest.
type command struct {
	name    string // the word after pairtime
	summary string // one line for the help text
	// run executes the command with the arguments after its name and
	// returns the exit status: 0 for success, 2 for a usage error, and
	// any other status the command documents.
	run func(args []string, stdout, stderr io.Writer) int
}

// commands is the command table, filled by register.
var commands = map[string]command{}

// builtins are the commands main.go handles itself.
var builtins = []command{
	{name: "version", summary: "Print the version, build details and release keys"},
	{name: "help", summary: "Show this help"},
}

// register adds c to the command table. It panics on a bad or repeated
// name, which fails every test of the package, so two work packages that
// pick the same name find out in CI.
func register(c command) {
	switch {
	case !validName(c.name):
		panic(fmt.Sprintf("pairtime: command name %q isn't lowercase letters, digits and dashes", c.name))
	case slices.ContainsFunc(builtins, func(b command) bool { return b.name == c.name }):
		panic(fmt.Sprintf("pairtime: %q is a built-in command", c.name))
	case commands[c.name].run != nil:
		panic(fmt.Sprintf("pairtime: command %q is registered twice", c.name))
	case c.summary == "" || c.run == nil:
		panic(fmt.Sprintf("pairtime: command %q needs a summary and a run function", c.name))
	}
	commands[c.name] = c
}

func validName(name string) bool {
	if name == "" || name[0] < 'a' || name[0] > 'z' {
		return false
	}
	for _, r := range name {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return false
		}
	}
	return true
}

// usage is the help text, with every registered command in it.
func usage() string {
	var list []command
	for _, c := range commands {
		list = append(list, c)
	}
	slices.SortFunc(list, func(a, b command) int { return strings.Compare(a.name, b.name) })
	list = append(list, builtins...)
	width := 0
	for _, c := range list {
		width = max(width, len(c.name))
	}
	var b strings.Builder
	b.WriteString(usageHead)
	for _, c := range list {
		fmt.Fprintf(&b, "  %-*s  %s\n", width, c.name, c.summary)
	}
	b.WriteString(usageTail)
	return b.String()
}
