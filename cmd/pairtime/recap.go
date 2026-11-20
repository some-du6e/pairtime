// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/some-du6e/pairtime/internal/coach/text"
)

const recapUsage = `Usage: pairtime recap [--json]

Shows your last work session: your time, and what you spent it on; each
agent's time working on its own; the other time pairtime saw; and how
pairtime saw your time. A work session ends after 20 minutes with nothing
counted, or when the computer locks or sleeps. It asks the running agent,
so the agent needs to be running.

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
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
	// Open is set while the session goes on, and End is then its last
	// counted minute so far.
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
	var reply sessionReply
	if !askView("/v1/sessions/last", &reply, stderr) {
		return 1
	}
	out := buildRecap(reply)
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

func buildRecap(r sessionReply) recapJSON {
	out := recapJSON{V: jsonVersion, Calibrating: r.Shadow}
	if s := r.Session; s != nil {
		out.Session = &sessionJSON{
			Start: s.Start.UTC(), End: s.End.UTC(), Open: s.Open,
			projectJSON: buildProject(s.Project, s.Name, s.tally, r.Shadow),
		}
	}
	return out
}

// writeRecap prints the session: which project and when, in zone, its
// rows, and how pairtime saw your time.
func writeRecap(w io.Writer, r recapJSON, zone *time.Location) {
	s := r.Session
	if s == nil {
		fmt.Fprintln(w, text.Phrase(text.KeyNoSession))
		return
	}
	start, end := s.Start.In(zone), s.End.In(zone)
	if s.Open {
		fmt.Fprintln(w, text.Phrase(text.KeySessionOpen, "project", s.title()))
		fmt.Fprintln(w, text.Phrase(text.KeySessionSince, "day", text.Day(start), "start", text.Clocktime(start)))
	} else {
		until := text.Clocktime(end)
		if y1, m1, d1 := start.Date(); !sameDay(end, y1, m1, d1) {
			until = text.Day(end) + ", " + until
		}
		fmt.Fprintln(w, text.Phrase(text.KeySessionLast, "project", s.title()))
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
