// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/some-du6e/pairtime/internal/agent"
	"github.com/some-du6e/pairtime/internal/agent/home"
)

// exitAlreadyRunning is the exit status of `pairtime agent` when another
// agent holds the data directory's lock. Whoever started the agent, such as
// a hook that found it down, can take it as success.
const exitAlreadyRunning = 3

const agentUsage = `Usage: pairtime agent

Runs the pairtime agent in the foreground until it is stopped. Logging in
starts it, and so does any hook that finds it down, so you rarely need to
run it yourself. One agent runs per data directory, and a second one exits
at once. Set ` + home.EnvHome + ` to an absolute path to keep every pairtime
file there instead of in the usual places.

Exit status:
  0  the agent stopped cleanly
  1  the agent couldn't start, or failed
  2  usage error
  3  another agent is already running for this data directory
`

func init() {
	register(command{
		name:    "agent",
		summary: "Run the background agent (login and hooks start it for you)",
		run:     runAgent,
	})
}

func runAgent(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("agent", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { fmt.Fprint(flags.Output(), agentUsage) }
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(stderr, "pairtime agent: takes no arguments, got %q\n", flags.Args())
		return 2
	}
	dirs, err := home.Default()
	if err != nil {
		fmt.Fprintf(stderr, "pairtime agent: %v\n", err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// After the first signal starts a clean stop, a second one kills the
	// agent the usual way.
	context.AfterFunc(ctx, stop)

	err = agent.Run(ctx, agent.Options{Dirs: dirs, Log: slog.New(slog.NewTextHandler(stderr, nil))})
	switch {
	case errors.Is(err, agent.ErrAlreadyRunning):
		fmt.Fprintf(stderr, "pairtime agent: already running for %s\n", dirs.Data)
		return exitAlreadyRunning
	case err != nil:
		fmt.Fprintf(stderr, "pairtime agent: %v\n", err)
		return 1
	}
	return 0
}
