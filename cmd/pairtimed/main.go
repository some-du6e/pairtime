// SPDX-License-Identifier: AGPL-3.0-or-later

// Command pairtimed is the pairtime server (DESIGN 3.10).
//
// Each subcommand lives in its own file and registers itself in the command
// table from an init function, so a new command is a new file and parallel
// work packages don't edit the same lines (docs/PLAN.md, WP-AG1, WP-SV1).
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/some-du6e/pairtime/internal/server/store"
)

// command is one subcommand.
type command struct {
	name string
	// summary is its line in the help text.
	summary string
	// run executes the command with the arguments after its name and
	// returns the exit code: 0 for success, 1 for a failure, 2 for a usage
	// error.
	run func(ctx context.Context, e *env, args []string) int
}

// env is what a command may use besides its arguments. Tests build their
// own.
type env struct {
	stdout, stderr io.Writer
	// environ is the process environment, as os.Environ returns it.
	environ []string

	// Hooks for tests; nil in a real run.
	//
	// storeOptions edits the options the database is opened with.
	storeOptions func(*store.Options)
	// listening is called once serve's listeners are up, with their
	// addresses.
	listening func(public, ops string)
	// githubURL stands in for https://api.github.com in doctor's
	// witness check.
	githubURL string
	// doctorWriteGap, when positive, replaces doctor's wait between its
	// two write-once puts.
	doctorWriteGap time.Duration
	// diskSpace stands in for backup.DiskSpace in doctor's disk check.
	diskSpace func(path string) (free, total uint64, ok bool, err error)
}

// getenv looks name up in e.environ.
func (e *env) getenv(name string) string {
	for _, kv := range slices.Backward(e.environ) {
		if k, v, ok := strings.Cut(kv, "="); ok && k == name {
			return v
		}
	}
	return ""
}

// commands is the command table, filled by register.
var commands = make(map[string]*command)

// register adds c to the command table. Each command's file calls it from
// init.
func register(c *command) {
	if _, dup := commands[c.name]; dup {
		panic("pairtimed: command " + c.name + " registered twice")
	}
	commands[c.name] = c
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], &env{stdout: os.Stdout, stderr: os.Stderr, environ: os.Environ()})
	stop()
	os.Exit(code)
}

// run executes one command line and returns the process exit code.
func run(ctx context.Context, args []string, e *env) int {
	if len(args) == 0 {
		fmt.Fprint(e.stderr, usage())
		return 2
	}
	name := args[0]
	switch name {
	case "help", "--help", "-h":
		fmt.Fprint(e.stdout, usage())
		return 0
	case "--version":
		name = "version"
	}
	c, ok := commands[name]
	if !ok {
		fmt.Fprintf(e.stderr, "pairtimed: unknown command %q\n\n", args[0])
		fmt.Fprint(e.stderr, usage())
		return 2
	}
	return c.run(ctx, e, args[1:])
}

// usage is the help text, built from the command table.
func usage() string {
	var b strings.Builder
	b.WriteString(`pairtimed is the pairtime server: accounts, projects, rollups and the live
dashboard for time the pairtime client has scored on each device.

Usage:
  pairtimed <command> [flags]

Commands:
`)
	names := make([]string, 0, len(commands)+1)
	for n := range commands {
		names = append(names, n)
	}
	names = append(names, "help")
	slices.Sort(names)
	width := 0
	for _, n := range names {
		width = max(width, len(n))
	}
	for _, n := range names {
		summary := "Show this help"
		if c := commands[n]; c != nil {
			summary = c.summary
		}
		fmt.Fprintf(&b, "  %-*s  %s\n", width, n, summary)
	}
	b.WriteString(`
Run "pairtimed <command> -h" for a command's flags.
Source and status: https://github.com/some-du6e/pairtime
`)
	return b.String()
}
