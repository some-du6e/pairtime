// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/coach/text"
)

const recapUsage = `Usage: pairtime recap [--json]

Shows your last work session: your time, and what you spent it on; each
agent's time working on its own; the other time pairtime saw; and how
pairtime saw your time. A work session ends after 20 minutes with nothing
counted, or when the computer locks or sleeps. It asks the running agent
over its socket, so the agent needs to be running.

  --json  Print the same numbers as JSON.

Exit status:
  0  the session was shown, or there is none yet
  1  the agent isn't running or couldn't answer
  2  usage error
`

func init() {
	register(command{
		name:    "recap",
		summary: "Show your last work session",
		run:     runRecap,
	})
}

// recapJSON is `pairtime recap --json`.
type recapJSON struct {
	V           int  `json:"v"`
	Calibrating bool `json:"calibrating"`
	// Session is null before the first work session.
	Session *sessionJSON `json:"session"`
}

type sessionJSON struct {
	// Date is the local day the session started on, YYYY-MM-DD.
	Date string `json:"date"`
	// Start and End are the session's bounds, in UTC. While calibrating
	// both are left out: with the time a session took, the rest of the
	// recap would give away the credited total it hides (DESIGN 6.7 step
	// 2; SM 7.5).
	Start *time.Time `json:"start,omitempty"`
	End   *time.Time `json:"end,omitempty"`
	// Open is set while the session goes on, and End is then the end of
	// its last counted minute so far.
	Open bool `json:"open"`
	projectJSON
}

func runRecap(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("recap", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { fmt.Fprint(flags.Output(), recapUsage) }
	asJSON := flags.Bool("json", false, "print JSON")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(stderr, "pairtime recap: takes no arguments, got %q\n", flags.Args())
		return 2
	}
	dirs, err := home.Default()
	if err != nil {
		sayAgentFailure(stderr, err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), viewTimeout)
	defer cancel()
	reply, err := askSession(ctx, dirs)
	if err != nil {
		sayAgentFailure(stderr, err)
		return 1
	}
	// The agent leaves your time out while display.shadow is on, and the
	// view reads config.toml as well, in case the agent hasn't seen a
	// change to it yet.
	shadow, err := readShadow(dirs)
	if err != nil {
		sayStoreFailure(stderr, err)
		return 1
	}
	out := buildRecap(reply, reply.Shadow || shadow, viewZone())
	if *asJSON {
		if err := writeJSON(stdout, out); err != nil {
			fmt.Fprintf(stderr, "pairtime recap: %v\n", err)
			return 1
		}
		return 0
	}
	writeRecap(stdout, out, viewZone())
	return 0
}

// buildRecap turns the agent's session into the view, with its day in
// zone. With calibrating set, the session's times are left out as well as
// your time (buildProject).
func buildRecap(r sessionReply, calibrating bool, zone *time.Location) recapJSON {
	out := recapJSON{V: jsonVersion, Calibrating: calibrating}
	if s := r.Session; s != nil {
		js := &sessionJSON{
			Date: s.Start.In(zone).Format(time.DateOnly), Open: s.Open,
			projectJSON: buildProject(s.Project, s.Name, s.tally, calibrating),
		}
		if !calibrating {
			start, end := s.Start.UTC(), s.End.UTC()
			js.Start, js.End = &start, &end
		}
		out.Session = js
	}
	return out
}

// writeRecap prints the session: which project and when, in zone, its
// rows, and how pairtime saw your time. While calibrating, when is its day
// alone.
func writeRecap(w io.Writer, r recapJSON, zone *time.Location) {
	s := r.Session
	if s == nil {
		fmt.Fprintln(w, text.Phrase(text.KeyNoSession))
		return
	}
	head := text.KeySessionLast
	if s.Open {
		head = text.KeySessionOpen
	}
	fmt.Fprintln(w, text.Phrase(head, "project", s.title()))
	switch {
	case s.Start == nil || s.End == nil:
		fmt.Fprintln(w, dayTitle(s.Date))
	case s.Open:
		start := s.Start.In(zone)
		fmt.Fprintln(w, text.Phrase(text.KeySessionSince, "day", text.Day(start), "start", text.Clocktime(start)))
	default:
		start, end := s.Start.In(zone), s.End.In(zone)
		until := text.Clocktime(end)
		if y1, m1, d1 := start.Date(); !sameDay(end, y1, m1, d1) {
			until = text.Day(end) + ", " + until
		}
		fmt.Fprintln(w, text.Phrase(text.KeySessionSpan, "day", text.Day(start), "start", text.Clocktime(start), "end", until))
	}
	fmt.Fprintln(w)
	writeRows(w, s.rows())
	if s.You == nil {
		fmt.Fprintln(w)
		fmt.Fprintln(w, text.Phrase(text.KeyCalibratingNote))
		return
	}
	if len(s.You.Levels) == 0 {
		return
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, text.Phrase(text.KeyLevelsHead))
	for _, l := range s.You.Levels {
		share := fmt.Sprintf("%d%%", l.Share)
		if l.Share == 0 {
			share = "<1%"
		}
		fmt.Fprintf(w, "  %4s  %s\n", share, text.Phrase(text.KeyLevelLine, "level", string(l.Level), "words", l.Words))
	}
}

// sameDay reports whether t falls on the day y-m-d of its own zone.
func sameDay(t time.Time, y int, m time.Month, d int) bool {
	ty, tm, td := t.Date()
	return ty == y && tm == m && td == d
}
