// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/some-du6e/pairtime/internal/adapters"
	"github.com/some-du6e/pairtime/internal/agent/doctor"
	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/ipc"
	"github.com/some-du6e/pairtime/internal/version"
)

const doctorUsage = `Usage: pairtime doctor [--json | --report]

Checks that pairtime works on this computer and prints one line per
check: ok, warn, FAIL or skip, what was checked, and what doctor found.
When something is wrong, the line says the one thing to do about it. The
idle and focus lines say what the running agent reads, not what this
terminal can reach. doctor sends nothing anywhere.

  --json    Print the checks as JSON, as described in
            docs/schema/cli-doctor.schema.json.
  --report  Print the footprint report instead: how much memory, CPU and
            disk time pairtime takes on this computer, with nothing in it
            that names you or this computer, so you can choose to share
            it.

Exit status:
  0  no check failed
  1  a check failed, or the report couldn't be made
  2  usage error
`

func init() {
	register(command{
		name:    "doctor",
		summary: "Check that pairtime works here, one line per check",
		run:     runDoctor,
	})
}

// doctorJSON is `pairtime doctor --json`, which
// docs/schema/cli-doctor.schema.json describes.
type doctorJSON struct {
	V       int            `json:"v"`
	Checks  []doctor.Check `json:"checks"`
	Summary doctor.Summary `json:"summary"`
}

// doctorWait bounds how long doctor waits for the agent's status. The
// agent answers from what it holds, so a slower answer means it's stuck.
// Tests shorten it.
var doctorWait = 3 * time.Second

func runDoctor(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("doctor", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { fmt.Fprint(flags.Output(), doctorUsage) }
	asJSON := flags.Bool("json", false, "print JSON")
	report := flags.Bool("report", false, "print the footprint report")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(stderr, "pairtime doctor: takes no arguments, got %q\n", flags.Args())
		return 2
	}
	if *asJSON && *report {
		fmt.Fprintln(stderr, "pairtime doctor: --json and --report don't go together")
		return 2
	}
	dirs, err := home.Default()
	if err != nil {
		fmt.Fprintf(stderr, "pairtime doctor: can't find pairtime's folders: %v\n", err)
		return 1
	}
	facts := gatherFacts(dirs)
	checks := doctor.Checks(facts)
	if *asJSON {
		if err := writeJSON(stdout, doctorJSON{V: jsonVersion, Checks: checks, Summary: doctor.Summarize(checks)}); err != nil {
			fmt.Fprintf(stderr, "pairtime doctor: %v\n", err)
			return 1
		}
	} else if err := doctor.Write(stdout, checks); err != nil {
		fmt.Fprintf(stderr, "pairtime doctor: %v\n", err)
		return 1
	}
	if doctor.Summarize(checks).Fail > 0 {
		return 1
	}
	return 0
}

// gatherFacts finds out what doctor's checks need: the running agent's
// status, over its socket, and what this computer holds.
func gatherFacts(dirs home.Dirs) doctor.Facts {
	f := doctor.Facts{Version: version.Get().Version, AskWait: doctorWait, Now: viewNow()}
	for _, h := range adapters.Default.Harnesses() {
		f.Adapters = append(f.Adapters, string(h))
	}
	st, err := askStatus(dirs)
	switch {
	case err == nil:
		f.Reach, f.Agent = doctor.Reached, &st
	case errors.Is(err, errAgentDown):
		f.Reach = doctor.Down
	case errors.Is(err, errAgentOld):
		f.Reach = doctor.Old
	case errors.Is(err, errAgentSlow):
		f.Reach = doctor.Slow
	default:
		f.Reach, f.AgentError = doctor.Unreached, err.Error()
	}
	return f
}

// askStatus asks the agent for its status over the socket
// (ipc.AskStatus), which checks that the agent runs as this user before
// it writes anything, so doctor sends the bearer token nowhere (#120).
func askStatus(dirs home.Dirs) (doctor.Status, error) {
	ep, err := ipc.EndpointFor(dirs)
	if err != nil {
		return doctor.Status{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), doctorWait)
	defer cancel()
	ans, err := ipc.Ask(ctx, ep, ipc.AskStatus)
	if err != nil {
		return doctor.Status{}, askFailure(ctx, err)
	}
	var st doctor.Status
	if err := json.Unmarshal(ans, &st); err != nil {
		return doctor.Status{}, fmt.Errorf("the agent's status didn't parse: %w", err)
	}
	return st, nil
}
