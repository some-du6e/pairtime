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
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/some-du6e/pairtime/internal/agent"
	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/keys"
)

// agentKeyStores is the agent's agent.Options.KeyStores: nil in the real
// binary, which keeps K_dev in the OS's secret store. The command's tests
// set it to an empty list in TestMain, which runs in every process they
// start, so the agents they run keep K_dev in a key file in their data
// directory and leave nothing in the Keychain or the Secret Service of
// the machine they run on.
var agentKeyStores []keys.Store

// exitAlreadyRunning is the exit status of `pairtime agent` when another
// agent holds the data directory's lock. Whoever started the agent, such as
// a hook that found it down, can take it as success.
const exitAlreadyRunning = 3

const agentUsage = `Usage: pairtime agent [--detach]

Runs the pairtime agent in the foreground until it is stopped. Logging in
starts it, and so does any hook that finds it down, so you rarely need to
run it yourself. One agent runs per data directory, and a second one exits
at once. Set ` + home.EnvHome + ` to an absolute path to keep every pairtime
file there instead of in the usual places.

  --detach  Start the agent in a process of its own, with no console
            window, print its process ID and return at once. The login
            item runs this on Windows, so logging in leaves no window open.

Exit status:
  0  the agent stopped cleanly, or --detach started it
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
	detach := flags.Bool("detach", false, "start the agent in a process of its own and return")
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
	if *detach {
		return detachAgent(stdout, stderr)
	}
	// A copy of the binary that an install moved aside on Windows while
	// something ran it goes once nothing does (commitBinary).
	if exe, err := os.Executable(); err == nil {
		removeOldBinaries(filepath.Dir(exe))
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

	err = runAgentMain(ctx, agent.Options{Dirs: dirs, Log: slog.New(slog.NewTextHandler(stderr, nil)), KeyStores: agentKeyStores,
		Lookback: startLookback})
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

// detachAgent starts `pairtime agent` in a new process that outlives this
// one and has no console window (detachedAttr), and returns without
// waiting for it.
//
// pairtime.exe is a console program, so when the Run value starts it at
// login, Windows opens a console window for it, which would stay open,
// full of the agent's log, for as long as the agent ran, and closing it
// would stop the agent. Started with --detach, this process ends at once
// and its window closes with it, while the agent runs on without one. The
// new agent has this process's environment, so it finds the same data
// directory, and it exits by itself when an agent already runs there.
func detachAgent(stdout, stderr io.Writer) int {
	pid, err := startAgent("")
	if err != nil {
		fmt.Fprintf(stderr, "pairtime agent: starting the agent: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "pairtime agent: started the agent, process %d\n", pid)
	return 0
}

// startAgent starts `pairtime agent` in a new process that outlives this
// one and has no console window (detachedAttr), and returns its process ID
// without waiting for it. `agent --detach` starts the agent this way, and
// so does a hook that finds it down (DESIGN 3.2). dir, when not "", is the
// agent's working directory: a hook runs in the harness's project folder,
// which Windows wouldn't let anyone delete while the agent worked in it.
func startAgent(dir string) (int, error) {
	exe, err := os.Executable()
	if err != nil {
		return 0, err
	}
	cmd := exec.Command(exe, "agent")
	cmd.SysProcAttr = detachedAttr()
	cmd.Dir = dir
	// With no Stdin, Stdout or Stderr the agent gets the null device for
	// all three, so it holds on to nothing of this process's console or
	// pipes.
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	cmd.Process.Release()
	return pid, nil
}
