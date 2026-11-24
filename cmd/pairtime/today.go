// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/doctor"
	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/ipc"
	"github.com/some-du6e/pairtime/internal/agent/projects"
	"github.com/some-du6e/pairtime/internal/coach/text"
)

const todayUsage = `Usage: pairtime today [--date YYYY-MM-DD] [--json]

Shows today's time for each project you worked on: your time, and what
you spent it on; each agent's time working on its own; and the other time
pairtime saw, described for what it was. It reads the day the agent has
stored, so it shows what was tracked even while the agent is stopped.
For today it also asks the running agent whether it can see which window
is in front, and says so when it can't.

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
	if day != "" {
		if t, err := time.Parse(time.DateOnly, day); err != nil || t.Format(time.DateOnly) != day {
			fmt.Fprintf(stderr, "pairtime today: --date wants a day such as 2026-11-24, got %q\n", day)
			return 2
		}
	}
	dirs, err := home.Default()
	if err != nil {
		sayStoreFailure(stderr, err)
		return 1
	}
	if day == "" {
		day = dayStore{dirs.Days()}.today()
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
	nameProjects(dirs, d.Projects)
	out := buildToday(d, shadow)
	if *asJSON {
		if err := writeJSON(stdout, out); err != nil {
			fmt.Fprintf(stderr, "pairtime today: %v\n", err)
			return 1
		}
		return 0
	}
	var focus []string
	if *date == "" && !agentStopped(dirs) {
		focus = focusLines(dirs)
	}
	writeToday(stdout, out, *date == "", focus)
	return 0
}

// focusAskWait bounds how long today waits for the agent to say whether
// it can see which window is in front. The answer only adds a sentence,
// so a slow agent doesn't hold the day up long. Tests shorten it.
var focusAskWait = time.Second

// focusLines is what today says about a desktop that hides which window is
// in front: DESIGN 5.9's teen-facing sentence while the running agent's GET
// /v1/status says focus is unknown, the signal doctor's focus line reads
// too, and nothing otherwise. As on the dashboard's Today page
// (web/src/local/copy.ts), the sentence stops at "in front" unless the
// status says such time counts at level C (focus_level_c, which the agent
// sets while it reads idle time; WP-SC8), and on GNOME the line the status's gnome_extension calls for
// follows. today asks over the per-user socket, as recap does, so no token
// goes anywhere. An agent that doesn't answer in time, or is older than the
// question, says nothing, and today prints none of it.
func focusLines(dirs home.Dirs) []string {
	ep, err := ipc.EndpointFor(dirs)
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), focusAskWait)
	defer cancel()
	ans, err := ipc.Ask(ctx, ep, ipc.AskStatus)
	if err != nil {
		return nil
	}
	var st struct {
		Signals struct {
			Focus          doctor.Signal `json:"focus"`
			FocusLevelC    bool          `json:"focus_level_c"`
			GnomeExtension string        `json:"gnome_extension"`
		} `json:"signals"`
	}
	if err := json.Unmarshal(ans, &st); err != nil {
		return nil
	}
	sig := st.Signals
	if sig.Focus.Via != "" || sig.Focus.Unknown == "" {
		return nil
	}
	lines := []string{text.Phrase(text.KeyFocusUnknown)}
	if sig.FocusLevelC {
		lines[0] = text.Phrase(text.KeyFocusUnknownLevelC)
	}
	switch sig.GnomeExtension {
	case "needs_login":
		lines = append(lines, text.Phrase(text.KeyGnomeNeedsLogin))
	case "unticked":
		lines = append(lines, text.Phrase(text.KeyGnomeUnticked))
	}
	return lines
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
// changes what an empty day says. focus, the lines focusLines gives, end
// the view: they explain time pairtime doesn't count on a desktop that
// hides which window is in front.
func writeToday(w io.Writer, d todayJSON, today bool, focus []string) {
	fmt.Fprintln(w, dayTitle(d.Date))
	if len(d.Projects) == 0 {
		fmt.Fprintln(w)
		if today {
			fmt.Fprintln(w, text.Phrase(text.KeyNothingToday))
		} else {
			fmt.Fprintln(w, text.Phrase(text.KeyNothingOn))
		}
	}
	for _, p := range d.Projects {
		fmt.Fprintln(w)
		fmt.Fprintln(w, p.title()+": "+p.clock())
		writeRows(w, p.rows())
	}
	if d.Calibrating && len(d.Projects) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, text.Phrase(text.KeyCalibratingNote))
	}
	if len(focus) > 0 {
		fmt.Fprintln(w)
		for _, l := range focus {
			fmt.Fprintln(w, l)
		}
	}
}

// nameProjects gives each project of a day that its rollup names no name
// the one pairtime shows for it on this computer: its repo's folder, from
// the git adapter's project map in state.json, which it reads without
// changing (projects.LocalNames). A project the map doesn't keep stays
// unnamed, and the views show its key.
func nameProjects(dirs home.Dirs, ps []dayProject) {
	m, err := projects.ReadMap(dirs.StateFile())
	if err != nil {
		return
	}
	local := projects.LocalNames(m)
	for i := range ps {
		if ps[i].Name == "" {
			ps[i].Name = local[ps[i].Project]
		}
	}
}
