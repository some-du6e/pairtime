// SPDX-License-Identifier: AGPL-3.0-or-later

// Package dashboard is what the owner's dashboard shows (WP-SV7's API and
// a server-rendered first cut of WP-SV8's page): the View, and the Source
// views come from. /demo renders a View from a fixed sample, /dashboard
// renders the signed-in account's from its rollups, and
// GET /api/v1/users/me/dashboard returns the same View as JSON.
//
// Every number is seconds the scorer credited, or seconds an agent ran,
// as rolled up from minute buckets (SM 5.1, 5.3). Minutes still inside
// their pending window are kept apart from final ones throughout, because
// a late event can still change them (DESIGN 6.2 D1), and the page draws
// them faded. The server settles a minute its device leaves pending once
// the window has passed (D-server-settles-pending).
//
// The server can't see display.shadow, SM 7.5's calibration week: no
// tier-1 field carries it, so a real dashboard shows credited time
// whatever the device's setting says. D-shadow-scope hides it on the
// device's own views. On the server an account flag the operator sets
// will carry it, before 2026-12-01 (D-server-shadow); until then the
// dashboard ignores shadow mode.
package dashboard

import (
	"context"
	"time"
)

// Window lengths, in local days, today included.
const (
	WeekDays  = 7
	MonthDays = 30
)

// Source is where views come from: the demo's fixed sample, or an
// account's rollups.
type Source interface {
	// View returns the dashboard of account, a users row's id, as of now.
	// The demo's sample ignores account.
	View(ctx context.Context, account string, now time.Time) (View, error)
}

// View is the owner's dashboard.
type View struct {
	// Sample is true for /demo's made-up data. A page that shows a sample
	// says so on the page itself.
	Sample bool `json:"sample"`
	// AsOf is when the view was read.
	AsOf time.Time `json:"as_of"`
	// TZ is the IANA time zone the days are counted in, the account's.
	TZ string `json:"tz"`
	// Today is the account's local date, as YYYY-MM-DD.
	Today string `json:"today"`
	// Agent names the agent whose time on its own shows beside the owner's,
	// such as "Claude Code" (DESIGN 3.8: agent-only time is named after
	// the agent that ran).
	Agent string `json:"agent"`
	// SplitByDevice says that each device's project keys count as
	// projects of their own, so a repo used on two computers shows as two
	// projects. Every real dashboard says so until project registration
	// (WP-SV10) links the keys of one repo (D-account-project-id).
	SplitByDevice bool `json:"projects_split_by_device"`
	// Projects lists every project with time in the last MonthDays days,
	// the most of the owner's time first.
	Projects []Project `json:"projects"`
	// Days are the last MonthDays local days, oldest first, so the last
	// one is Today.
	Days []Day `json:"days"`
	// Week and Month sum the last WeekDays and MonthDays days.
	Week  Totals `json:"week"`
	Month Totals `json:"month"`
}

// Project is one of the owner's projects.
type Project struct {
	// ID is the account-level project id that ProjectTime.Project names
	// (D-account-project-id). The sample's ids are made up.
	ID string `json:"id"`
	// Keys are the opaque project keys the owner's devices upload for the
	// project (DESIGN 4.3), in device order, so a client can match the
	// project with its own name for it. Each device keys its projects
	// itself, so until project registration (WP-SV10) links the keys of
	// one repo, a project has one key.
	Keys []string `json:"keys"`
	// Name is what the page calls the project: CodeName of its Code on a
	// real dashboard, since the server never learns a project's name.
	Name string `json:"name"`
	// Code is the project's short code (ShortCodes), such as "7c41e9":
	// unique among the account's projects, so the owner can tell two
	// projects without names apart. The samples' made-up projects have
	// names and no code.
	Code string `json:"code,omitempty"`
}

// Time is seconds, with pending minutes kept apart from final ones.
type Time struct {
	FinalS   float64 `json:"final_s"`
	PendingS float64 `json:"pending_s"`
}

// Total is final and pending seconds together.
func (t Time) Total() float64 { return t.FinalS + t.PendingS }

// Add returns t plus u.
func (t Time) Add(u Time) Time {
	return Time{FinalS: t.FinalS + u.FinalS, PendingS: t.PendingS + u.PendingS}
}

// ProjectTime is the owner's credited time and the agent's time on its own
// in one project.
type ProjectTime struct {
	// Project is the Project's ID.
	Project string `json:"project"`
	You     Time   `json:"you"`
	Agent   Time   `json:"agent"`
}

// Day is one local day.
type Day struct {
	// Date is YYYY-MM-DD.
	Date string `json:"date"`
	// You and Agent sum the day's projects.
	You   Time `json:"you"`
	Agent Time `json:"agent"`
	// Projects has an entry for each project with time that day, in
	// View.Projects order.
	Projects []ProjectTime `json:"projects"`
}

// Totals sums a window of days.
type Totals struct {
	// From and To are the window's first and last dates, YYYY-MM-DD.
	From string `json:"from"`
	To   string `json:"to"`
	You  Time   `json:"you"`
	// Agent is the agent's time on its own: the seconds it ran in a
	// project that none of the owner's credited time there covers (SM 2.6,
	// D-bucket-agent-only). In a project it never overlaps the owner's
	// time, as on a chess clock.
	Agent Time `json:"agent"`
	// Projects has an entry for each project with time in the window, in
	// View.Projects order.
	Projects []ProjectTime `json:"projects"`
	// Levels splits the owner's time by evidence level, A, B and C in
	// that order (DESIGN 6.4).
	Levels []LevelTime `json:"levels"`
	// Kinds splits the owner's time by activity kind (SM 5.1), the most
	// time first; kinds with no time are left out.
	Kinds []KindTime `json:"kinds"`
}

// LevelTime is the owner's time at one evidence level.
type LevelTime struct {
	// Level is "A", "B" or "C".
	Level string `json:"level"`
	Time
}

// KindTime is the owner's time in one activity kind.
type KindTime struct {
	// Kind is an activity kind, such as "prompting".
	Kind string `json:"kind"`
	Time
}
