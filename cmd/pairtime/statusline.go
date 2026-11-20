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

	"github.com/some-du6e/pairtime/internal/coach/text"
	"github.com/some-du6e/pairtime/pkg/event"
)

const statuslineUsage = `Usage: pairtime statusline [--json] [--timeout DURATION]

Prints one short line with today's time, such as
"you 1h 12m · Claude 2h 40m", for a status line. For Claude Code, put this
in settings.json:

  "statusLine": {"type": "command", "command": "pairtime statusline"}

It never holds the harness up: when the agent isn't running, or doesn't
answer within the timeout, it prints nothing and exits 0.

  --json     Print the line and its numbers as JSON.
  --timeout  How long to wait for the agent (default 40ms).

Exit status:
  0  the line was printed, or nothing was
  2  usage error
`

// statuslineTimeout is how long the status line waits for the agent by
// default. A harness runs the command every time its status line
// refreshes, so a slow answer is worth less than none (PLAN WP-CO0: under
// 50 ms).
const statuslineTimeout = 40 * time.Millisecond

// statuslineAgents is how many agents the line names, the busiest first,
// so it stays short.
const statuslineAgents = 2

func init() {
	register(command{
		name:    "statusline",
		summary: "Print today's time on one line, for a status line",
		run:     runStatusline,
	})
}

// statusJSON is `pairtime statusline --json`: the line, and today's
// numbers across projects that it shows.
type statusJSON struct {
	V           int    `json:"v"`
	Date        string `json:"date"`
	Calibrating bool   `json:"calibrating"`
	// YouS is your time today, absent while calibrating.
	YouS *float64 `json:"you_s,omitempty"`
	// Agents is each agent's time on its own today, the most first.
	Agents []agentJSON `json:"agents"`
	Line   string      `json:"line"`
}

func runStatusline(args []string, stdout, stderr io.Writer) (code int) {
	flags := flag.NewFlagSet("statusline", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { fmt.Fprint(flags.Output(), statuslineUsage) }
	asJSON := flags.Bool("json", false, "print JSON")
	timeout := flags.Duration("timeout", statuslineTimeout, "how long to wait for the agent")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(stderr, "pairtime statusline: takes no arguments, got %q\n", flags.Args())
		return 2
	}
	if *timeout <= 0 {
		fmt.Fprintf(stderr, "pairtime statusline: --timeout must be more than 0, got %v\n", *timeout)
		return 2
	}
	// From here on, nothing a harness could take as a failure: every
	// problem prints nothing, and so does a bug.
	defer func() {
		if recover() != nil {
			code = 0
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	var reply dayReply
	if err := askAgent(ctx, "/v1/days/today", &reply); err != nil {
		return 0
	}
	s := buildStatus(reply)
	if *asJSON {
		b, err := json.Marshal(s)
		if err == nil {
			stdout.Write(append(b, '\n'))
		}
		return 0
	}
	fmt.Fprintln(stdout, s.Line)
	return 0
}

// buildStatus sums the day's projects into the line's numbers.
func buildStatus(r dayReply) statusJSON {
	you := 0.0
	byHarness := map[event.Harness]float64{}
	for _, p := range r.Projects {
		you += p.CreditedS
		for h, s := range p.AgentOnlyByHarness {
			byHarness[h] += s
		}
	}
	out := statusJSON{V: jsonVersion, Date: r.Date, Calibrating: r.Shadow, Agents: agentList(byHarness)}
	if !r.Shadow {
		out.YouS = &you
	}
	clock := make([]text.AgentClock, 0, statuslineAgents)
	for _, a := range out.Agents[:min(len(out.Agents), statuslineAgents)] {
		clock = append(clock, text.AgentClock{Name: a.Name, S: a.S})
	}
	out.Line = text.Clock(you, r.Shadow, clock)
	return out
}
