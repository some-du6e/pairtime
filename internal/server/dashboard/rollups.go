// SPDX-License-Identifier: AGPL-3.0-or-later

package dashboard

import (
	"context"
	"time"

	"github.com/some-du6e/pairtime/internal/server/rollup"
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
}

// View returns the account's last MonthDays local days as of now.
func (r Rollups) View(ctx context.Context, account string, now time.Time) (View, error) {
	zone, name, err := rollup.Zone(ctx, r.DB, account)
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
	var rows []Row
	names := make(map[string]string)
	for _, day := range days {
		if day.Project != "" {
			names[day.Project] = shortKey(day.Project)
		}
		if day.Kind == "" {
			rows = append(rows,
				Row{Day: day.Day, Project: day.Project, Agent: true, Seconds: seconds(day.AgentOnlyFinalMS)},
				Row{Day: day.Day, Project: day.Project, Agent: true, Pending: true, Seconds: seconds(day.AgentOnlyPendingMS)})
			continue
		}
		for i, level := range rollup.Levels {
			rows = append(rows,
				Row{Day: day.Day, Project: day.Project, Kind: day.Kind, Level: level, Seconds: seconds(day.FinalMS[i])},
				Row{Day: day.Day, Project: day.Project, Kind: day.Kind, Level: level, Pending: true, Seconds: seconds(day.PendingMS[i])})
		}
	}
	return Build(Meta{AsOf: now, TZ: name, Today: today.Format(DateLayout), Agent: AgentName, Names: names}, rows)
}

// seconds turns milliseconds into seconds. Credit moves in steps of half
// a second, which float64 holds exactly.
func seconds(ms int64) float64 { return float64(ms) / 1000 }

// shortKey is what the page calls a project with no name: its key, cut
// to 12 characters. Keys are opaque HMACs (DESIGN 4.3), and the server
// never learns a project's name unless the owner publishes it.
func shortKey(key string) string {
	if len(key) <= 12 {
		return key
	}
	return key[:12] + "…"
}
