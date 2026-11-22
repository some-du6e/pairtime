// SPDX-License-Identifier: AGPL-3.0-or-later

package main

// What `pairtime today`, `recap` and `statusline` read, and how. None of
// them uses the agent's local HTTP API, so none sends its bearer token
// anywhere (#120):
//
//   - today and statusline read the day rollups the agent's scorer writes,
//     days/YYYY-MM-DD.json (DESIGN 3.5), in the shape of WP-AG4's
//     internal/agent/store.Day (matched at 6c15be9), and display.shadow
//     from config.toml, the way the agent reads it (config.Load). This is
//     PLAN WP-CO0's "reading days/ only". Today is the day whose rollup
//     holds now (dayStore.today).
//   - recap asks the agent for its last work session over the per-user
//     socket (ipc.Ask), which checks that the agent runs as this user
//     before it writes anything. The agent works the session out from its
//     buckets and the last lock or suspend it saw, which no file holds.
//     The answer is what GET /v1/sessions/last gives.
//   - today asks the running agent for its status over the same socket
//     (ipc.AskStatus), as doctor does, only to learn whether focus is
//     unknown on this desktop, and prints the day without DESIGN 5.9's
//     sentence about it when the agent doesn't answer (focusLines).
//
// The local API keeps GET /v1/days/{date} and GET /v1/sessions/last, which
// the dashboard reads with its session cookie (WP-UI1).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/config"
	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/ipc"
	"github.com/some-du6e/pairtime/internal/agent/localhttp"
	"github.com/some-du6e/pairtime/pkg/event"
)

// tally is what the agent counts for one project over a span, a local day
// or a work session, from its minute buckets (SM 5.1, 5.3): a project's row
// in a day's rollup, and the session in GET /v1/sessions/last. Seconds may
// be halves.
type tally struct {
	// CreditedS is the credited seconds: your time.
	CreditedS float64 `json:"credited_s"`
	// PendingS is the part of CreditedS in minutes that are still pending
	// (DESIGN 3.7), which late records may change.
	PendingS float64 `json:"pending_s"`
	// CreditedByKind and CreditedByEvidence each split CreditedS.
	CreditedByKind     map[event.Activity]float64 `json:"credited_by_kind"`
	CreditedByEvidence map[event.Evidence]float64 `json:"credited_by_evidence"`
	// AgentOnlyS is SM 5.3's agent_only_s for the project: the seconds this
	// project's agent turns ran that this project didn't credit, including
	// seconds credited to another project (SM 6.3; testdata/timelines/6.3
	// agent_only_s.<project>). They aren't the X.agent_only seconds: in
	// SM 6.4 all 23,180 of them are X.absent.
	AgentOnlyS float64 `json:"agent_only_s"`
	// AgentOnlyByHarness splits AgentOnlyS by the harness whose turn ran.
	// It names every harness that had a session in the project in the
	// span, with 0 for one that had no agent-only time, so the views can
	// name the agent whose answers were read.
	AgentOnlyByHarness map[event.Harness]float64 `json:"agent_only_by_harness"`
	// Uncredited is the seconds the project was looked at and not
	// credited, by reason (SM 5.2). Agent-only seconds that no project
	// credited are in it under their own codes; those another project
	// credited aren't (SM 6.3).
	Uncredited map[event.Reason]float64 `json:"uncredited"`
	// OnHoldS is the part of Uncredited's X.noprog in minutes still
	// pending or provisional (SM 4.3, 5.1): half-weighted seconds that
	// progress within PROGRESS_RESTORE_S can still bring back. The views
	// call only these "on hold" (approach-teen.md D5). Absent means 0.
	OnHoldS float64 `json:"on_hold_s,omitempty"`
}

// dayFile is one local day's rollup, days/YYYY-MM-DD.json, which the
// agent's scorer rewrites whenever one of the day's minute buckets changes
// (WP-AG4's store.Day).
type dayFile struct {
	// Date is the day, YYYY-MM-DD, in the time zone Zone names, the
	// device's own. Start and End are its bounds, in UTC.
	Date  string    `json:"date"`
	Zone  string    `json:"zone"`
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
	// Projects has a row for each project with credit, agent time or
	// uncredited time in the day.
	Projects []dayProject `json:"projects"`
}

// dayProject is one project's row in a day.
type dayProject struct {
	// Project is the project's key. Name is its name from the local project
	// map, which never leaves the device: "" until there is one, and then
	// the views show the key.
	Project string `json:"project"`
	Name    string `json:"name"`
	tally
}

// sessionReply is the last work session (SM 5.3), or the one still going,
// as GET /v1/sessions/last gives it and the agent answers
// ipc.AskLastSession. Session is null before the first.
//
// While display.shadow is on, the agent leaves out every credited number,
// every uncredited code but its own and SM 4.2's loops, and the session's
// start and end (D-shadow-scope). A number it leaves out reads as 0 and a
// map as empty, and the views say "calibrating".
type sessionReply struct {
	// Shadow is display.shadow (DESIGN 6.7 step 2).
	Shadow  bool         `json:"shadow"`
	Session *workSession `json:"session"`
}

type workSession struct {
	Project string `json:"project"`
	Name    string `json:"name"`
	// Date is the local day the session started on, YYYY-MM-DD: all a
	// session says of when it was while display.shadow is on
	// (D-shadow-scope). Without it, the views take the day from Start.
	Date  string    `json:"date,omitempty"`
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
	// Open is set while the session goes on: no SESSION_GAP_S without
	// credit since End, and no lock or suspend (DESIGN 3.8).
	Open bool `json:"open"`
	tally
}

// The ways reading the agent's time fails that the views tell apart.
var (
	// errAgentDown is no agent to ask: nothing answers on its socket, or
	// port.json is missing or says the agent stopped.
	errAgentDown = errors.New("the agent isn't running")
	// errAgentOld is an agent that doesn't answer the question, because it
	// is older than it.
	errAgentOld = errors.New("the running agent can't answer this")
	// errAgentSlow is an agent that didn't answer in time.
	errAgentSlow = errors.New("the agent didn't answer in time")
)

// maxDayFile bounds what the views read of a day's rollup.
const maxDayFile = 1 << 20

// dayStore reads the day rollups in dir: the agent's days/
// (home.Dirs.Days), or a test's.
type dayStore struct{ dir string }

// day returns date's rollup, and whether the agent has written one. A day
// without one is empty: nothing was tracked that day, or the scorer hasn't
// got to it yet.
func (s dayStore) day(date string) (dayFile, bool, error) {
	if t, err := time.Parse(time.DateOnly, date); err != nil || t.Format(time.DateOnly) != date {
		return dayFile{}, false, fmt.Errorf("%q isn't a date", date)
	}
	name := filepath.Join(s.dir, date+".json")
	f, err := os.Open(name)
	if errors.Is(err, fs.ErrNotExist) {
		return dayFile{Date: date, Projects: []dayProject{}}, false, nil
	}
	if err != nil {
		return dayFile{}, false, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxDayFile+1))
	if err != nil {
		return dayFile{}, false, err
	}
	if len(b) > maxDayFile {
		return dayFile{}, false, fmt.Errorf("%s is over %d bytes", name, maxDayFile)
	}
	var d dayFile
	if err := json.Unmarshal(b, &d); err != nil {
		return dayFile{}, false, fmt.Errorf("%s: %w", name, err)
	}
	if d.Date != date {
		return dayFile{}, false, fmt.Errorf("%s holds the day %q", name, d.Date)
	}
	if d.Projects == nil {
		d.Projects = []dayProject{}
	}
	return d, true, nil
}

// today returns the date of the local day the agent counts now in: of the
// days around now's date in viewZone, the one whose rollup holds now
// between its start and end, which are the agent's bounds for the day. The
// agent reads its time zone once, when it starts, and this process reads
// its own, so after the computer's zone changes the two can name different
// days for the same instant, and the agent goes on writing its own. With no
// rollup that holds now, as before the agent has written today's, it is
// localToday.
func (s dayStore) today() string {
	now := viewNow()
	y, m, d := now.In(viewZone()).Date()
	// Two zones' dates are at most two days apart.
	for _, delta := range []int{0, -1, 1, -2, 2} {
		date := time.Date(y, m, d+delta, 12, 0, 0, 0, time.UTC).Format(time.DateOnly)
		if day, found, err := s.day(date); err == nil && found && !now.Before(day.Start) && now.Before(day.End) {
			return date
		}
	}
	return localToday()
}

// readShadow reads display.shadow from config.toml as the agent does: off
// when there is no config.toml, or when it doesn't parse, as the agent has
// it then (config.Load). A config.toml that can't be read is an error,
// because then nobody knows whether your time is to be hidden.
func readShadow(dirs home.Dirs) (bool, error) {
	cfg, _, err := config.Load(dirs.ConfigFile())
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	return cfg.Display.Shadow, nil
}

// agentStopped reports whether the agent is known not to run: it never
// wrote port.json, or wrote 0 as its pid when it stopped cleanly. An agent
// that was killed leaves its pid behind, so this can't tell that one has
// gone. Nothing from port.json goes anywhere: the views read it for this
// alone.
func agentStopped(dirs home.Dirs) bool {
	pf, err := localhttp.ReadPortFile(dirs.PortFile())
	return errors.Is(err, fs.ErrNotExist) || (err == nil && pf.PID == 0)
}

// askSession asks the agent for its last work session over the socket,
// within ctx.
func askSession(ctx context.Context, dirs home.Dirs) (sessionReply, error) {
	ep, err := ipc.EndpointFor(dirs)
	if err != nil {
		return sessionReply{}, err
	}
	ans, err := ipc.Ask(ctx, ep, ipc.AskLastSession)
	if err != nil {
		return sessionReply{}, askFailure(ctx, err)
	}
	var r sessionReply
	if err := json.Unmarshal(ans, &r); err != nil {
		return sessionReply{}, fmt.Errorf("the agent's answer didn't parse: %w", err)
	}
	return r, nil
}

// askFailure sorts an error from asking the agent: out of time, an agent
// older than the question, no agent listening, or something else.
func askFailure(ctx context.Context, err error) error {
	var op *net.OpError
	switch {
	case timedOut(ctx, err):
		return errAgentSlow
	case errors.Is(err, ipc.ErrUnknownQuestion):
		return errAgentOld
	case errors.Is(err, ipc.ErrNotAgent):
		return err
	case errors.Is(err, fs.ErrNotExist), errors.As(err, &op) && op.Op == "dial":
		return errAgentDown
	}
	return err
}

// timedOut reports whether err came from ctx's deadline. A dial bounded by
// a context can fail with its own i/o timeout a moment before ctx.Err() is
// set, so a network timeout counts as well.
func timedOut(ctx context.Context, err error) bool {
	if ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}
