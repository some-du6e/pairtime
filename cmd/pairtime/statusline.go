// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/coach/text"
	"github.com/some-du6e/pairtime/pkg/event"
)

const statuslineUsage = `Usage: pairtime statusline [--json] [--timeout DURATION]

Prints one short line with today's time, such as
"you 1h 12m · Claude 2h 40m", for a status line. For Claude Code, put this
in settings.json:

  "statusLine": {"type": "command", "command": "pairtime statusline"}

It reads only the day the agent has stored, and never holds the harness
up: when the agent isn't running, or the day can't be read within the
timeout, it prints nothing and exits 0.

  --json     Print the line and its numbers as JSON.
  --timeout  How long reading the day may take (default 40ms).

Exit status:
  0  the line was printed, or nothing was
  2  usage error
`

// statuslineTimeout is how long the status line may take to read the day
// by default. A harness runs the command every time its status line
// refreshes, so a slow line is worth less than none (PLAN WP-CO0: under
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
	// YouByLevel splits YouS by evidence level, with only the levels that
	// have time; the line labels level C (DESIGN 6.2 D11). Absent while
	// calibrating, or with no time yet.
	YouByLevel map[event.Evidence]float64 `json:"you_by_level,omitempty"`
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
	// The day is read on a goroutine of its own, so a slow disk or home
	// directory can't hold the line past the timeout.
	read, done := statusRead, make(chan *statusJSON, 1)
	go func() {
		var s *statusJSON
		defer func() {
			recover()
			done <- s
		}()
		s, _ = read()
	}()
	timer := time.NewTimer(*timeout)
	defer timer.Stop()
	var s *statusJSON
	select {
	case s = <-done:
	case <-timer.C:
		// A disk or a home directory that is slow to answer.
	}
	if s == nil {
		return 0
	}
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

// statusRead reads today's numbers for the line, or nil with the reason
// when there is nothing to print. Tests replace it.
var statusRead = readStatus

// readStatus reads today's rollup and display.shadow, and makes the line:
// nothing while the agent is known to be stopped, and "you 0m" before its
// first minute today.
func readStatus() (*statusJSON, error) {
	dirs, err := home.Default()
	if err != nil {
		return nil, err
	}
	if agentStopped(dirs) {
		return nil, errAgentDown
	}
	shadow, err := readShadow(dirs)
	if err != nil {
		return nil, err
	}
	d, _, err := dayStore{dirs.Days()}.day(localToday())
	if err != nil {
		return nil, err
	}
	s := buildStatus(d, shadow)
	return &s, nil
}

// buildStatus sums the day's projects into the line's numbers. With shadow
// set, your time is calibrating.
func buildStatus(d dayFile, shadow bool) statusJSON {
	you := 0.0
	byLevel := map[event.Evidence]float64{}
	byHarness := map[event.Harness]float64{}
	for _, p := range d.Projects {
		you += p.CreditedS
		for _, l := range levels {
			if s := p.CreditedByEvidence[l]; s > 0 {
				byLevel[l] += s
			}
		}
		for h, s := range p.AgentOnlyByHarness {
			byHarness[h] += s
		}
	}
	out := statusJSON{V: jsonVersion, Date: d.Date, Calibrating: shadow, Agents: agentList(byHarness)}
	if !shadow {
		out.YouS, out.YouByLevel = &you, byLevel
	}
	clock := make([]text.AgentClock, 0, statuslineAgents)
	for _, a := range out.Agents[:min(len(out.Agents), statuslineAgents)] {
		clock = append(clock, text.AgentClock{Name: a.Name, S: a.S})
	}
	out.Line = text.Clock(you, byLevel[event.EvidenceC], shadow, clock)
	return out
}
