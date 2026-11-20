// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/coach/text"
)

const todayUsage = `Usage: pairtime today [--date YYYY-MM-DD] [--json]

Shows today's time for each project you worked on: your time, and what
you spent it on; each agent's time working on its own; and the other time
pairtime saw, described for what it was. It reads the day the agent has
stored, so it shows what was tracked even while the agent is stopped.

  --date  Show another day, such as 2026-11-24.
  --json  Print the same numbers as JSON, as described in
          docs/schema/cli-today.schema.json.

Exit status:
  0  the day was shown
  1  the agent isn't running and has stored nothing for today, or
     pairtime couldn't read what it stored
  2  usage error
`

func init() {
	register(command{
		name:    "today",
		summary: "Show today's time: yours, your agent's, and the rest",
		run:     runToday,
	})
}

// todayJSON is `pairtime today --json`, which
// docs/schema/cli-today.schema.json describes.
type todayJSON struct {
	V    int    `json:"v"`
	Date string `json:"date"`
	// Calibrating is display.shadow: while it is on, no project has "you".
	Calibrating bool          `json:"calibrating"`
	Projects    []projectJSON `json:"projects"`
}

func runToday(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("today", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { fmt.Fprint(flags.Output(), todayUsage) }
	date := flags.String("date", "", "the day to show, YYYY-MM-DD")
	asJSON := flags.Bool("json", false, "print JSON")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(stderr, "pairtime today: takes no arguments, got %q\n", flags.Args())
		return 2
	}
	day := *date
	if day == "" {
		day = localToday()
	} else if t, err := time.Parse(time.DateOnly, day); err != nil || t.Format(time.DateOnly) != day {
		fmt.Fprintf(stderr, "pairtime today: --date wants a day such as 2026-11-24, got %q\n", day)
		return 2
	}
	dirs, err := home.Default()
	if err != nil {
		sayStoreFailure(stderr, err)
		return 1
	}
	shadow, err := readShadow(dirs)
	if err != nil {
		sayStoreFailure(stderr, err)
		return 1
	}
	d, found, err := dayStore{dirs.Days()}.day(day)
	if err != nil {
		sayStoreFailure(stderr, err)
		return 1
	}
	if !found && *date == "" && agentStopped(dirs) {
		// Nothing tracked yet today because nothing is tracking.
		sayAgentFailure(stderr, errAgentDown)
		return 1
	}
	out := buildToday(d, shadow)
	if *asJSON {
		if err := writeJSON(stdout, out); err != nil {
			fmt.Fprintf(stderr, "pairtime today: %v\n", err)
			return 1
		}
		return 0
	}
	writeToday(stdout, out, *date == "")
	return 0
}

// buildToday turns the agent's day into the view, projects with the most
// of your time first, then with the most agent time, then by name. While
// calibrating (shadow), the order leaves your time out too, so it can't
// say which project scored more.
func buildToday(d dayFile, shadow bool) todayJSON {
	out := todayJSON{V: jsonVersion, Date: d.Date, Calibrating: shadow, Projects: []projectJSON{}}
	credit := map[string]float64{}
	agentTime := map[string]float64{}
	for _, p := range d.Projects {
		out.Projects = append(out.Projects, buildProject(p.Project, p.Name, p.tally, shadow))
		if !shadow {
			credit[p.Project] += p.CreditedS
		}
		agentTime[p.Project] += p.AgentOnlyS
	}
	slices.SortStableFunc(out.Projects, func(a, b projectJSON) int {
		if c := most(credit[a.Project], credit[b.Project], "", ""); c != 0 {
			return c
		}
		return most(agentTime[a.Project], agentTime[b.Project], a.title(), b.title())
	})
	return out
}

// writeToday prints the day: its date, then each project's chess clock
// and rows. today says whether the day was asked for as today, which
// changes what an empty day says.
func writeToday(w io.Writer, d todayJSON, today bool) {
	fmt.Fprintln(w, dayTitle(d.Date))
	if len(d.Projects) == 0 {
		fmt.Fprintln(w)
		if today {
			fmt.Fprintln(w, text.Phrase(text.KeyNothingToday))
		} else {
			fmt.Fprintln(w, text.Phrase(text.KeyNothingOn))
		}
		return
	}
	for _, p := range d.Projects {
		fmt.Fprintln(w)
		fmt.Fprintln(w, p.title()+": "+p.clock())
		writeRows(w, p.rows())
	}
	if d.Calibrating {
		fmt.Fprintln(w)
		fmt.Fprintln(w, text.Phrase(text.KeyCalibratingNote))
	}
}
