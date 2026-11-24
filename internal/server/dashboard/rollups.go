// SPDX-License-Identifier: AGPL-3.0-or-later

package dashboard

import (
	"context"
	"time"

	"github.com/some-du6e/pairtime/internal/server/projectnames"
	"github.com/some-du6e/pairtime/internal/server/rollup"
	"github.com/some-du6e/pairtime/internal/server/shadow"
	"github.com/some-du6e/pairtime/pkg/projectname"
)

// AgentName is what a real dashboard calls the agent. Tier-1 buckets
// don't say which harness ran in a minute (DESIGN 4.3), so until the
// dashboard reads the harness names the devices' labels list, it names
// the agent this way rather than guess. Its time is the bucket's
// agent_only, per project (SM 2.6, D-bucket-agent-only).
const AgentName = "your agent"

// Rollups is the Source of a signed-in account's dashboard: its
// rollups_daily rows, counted in its time zone (package rollup).
type Rollups struct {
	// DB is where to read, store.DB.Read.
	DB rollup.Querier
	// Scoring is the scoring version the server runs
	// (pages.BuiltinScoringVersion): an account's calibration flag hides
	// its credited time only while it is below 1.0.0 (shadow.Hides).
	Scoring string
}

// View returns the account's last MonthDays local days as of now. Its
// projects are the account-level ones the rollups count
// (D-account-project-id), each called by the short code of its first
// device key (projectname.ShortCodes). Until project registration (WP-SV10) links the
// keys of one repo, each device's key is a project of its own, and the
// view says so. While the account's calibration flag hides its credited
// time, the view is Calibrate's (D-server-shadow).
func (r Rollups) View(ctx context.Context, account string, now time.Time) (View, error) {
	zone, name, err := rollup.Zone(ctx, r.DB, account)
	if err != nil {
		return View{}, err
	}
	hidden, err := shadow.Hides(ctx, r.DB, account, r.Scoring)
	if err != nil {
		return View{}, err
	}
	// Today is the local date now, from the instant itself. The window
	// steps back by whole dates at noon UTC, which no time zone's daylight
	// saving time can move.
	y, m, d := now.In(zone).Date()
	today := time.Date(y, m, d, 12, 0, 0, 0, time.UTC)
	from := today.AddDate(0, 0, -(MonthDays - 1)).Format(DateLayout)
	days, err := rollup.Read(ctx, r.DB, account, from, today.Format(DateLayout))
	if err != nil {
		return View{}, err
	}
	keys, err := rollup.Projects(ctx, r.DB, account)
	if err != nil {
		return View{}, err
	}
	// The names the owner turned on, by project id (D-owner-names): a
	// project is called by the name of its first key that has one, and a
	// name counts only for the project its own device's key maps to.
	labels, err := projectnames.OfAccount(ctx, r.DB, account)
	if err != nil {
		return View{}, err
	}
	// The codes are unique among all the account's projects, not only the
	// month's, so a project's code doesn't change when others go quiet,
	// and gets longer only when a new key clashes with it. The names
	// endpoint hands each device its keys' codes from the same
	// ProjectCodes, so pairtime projects shows the code this page does.
	// rollup_projects keeps a project's keys as long as the account; a
	// project nothing maps is called by its id's own code.
	codes := projectname.ProjectCodes(keys)
	for _, day := range days {
		if id := day.ProjectID; id != "" && codes[id] == "" {
			codes[id] = projectname.ShortCodes([]string{id})[id]
		}
	}
	names := make(map[string]string, len(codes))
	for id, code := range codes {
		names[id] = projectname.CodeName(code)
	}
	var rows []Row
	for _, day := range days {
		id := day.ProjectID
		if day.Kind == "" {
			rows = append(rows,
				Row{Day: day.Day, Project: id, Agent: true, Seconds: seconds(day.AgentOnlyFinalMS)},
				Row{Day: day.Day, Project: id, Agent: true, Pending: true, Seconds: seconds(day.AgentOnlyPendingMS)})
			continue
		}
		for i, level := range rollup.Levels {
			rows = append(rows,
				Row{Day: day.Day, Project: id, Kind: day.Kind, Level: level, Seconds: seconds(day.FinalMS[i])},
				Row{Day: day.Day, Project: id, Kind: day.Kind, Level: level, Pending: true, Seconds: seconds(day.PendingMS[i])})
		}
	}
	v, err := Build(Meta{
		AsOf: now, TZ: name, Today: today.Format(DateLayout), Agent: AgentName,
		Names: names, Codes: codes, Labels: labels, Keys: keys,
		// TODO(WP-SV10): false once registration links the keys of one
		// repo across devices.
		SplitByDevice: true,
	}, rows)
	if err != nil || !hidden {
		return v, err
	}
	return Calibrate(v), nil
}

// seconds turns milliseconds into seconds. Credit moves in steps of half
// a second, which float64 holds exactly.
func seconds(ms int64) float64 { return float64(ms) / 1000 }
