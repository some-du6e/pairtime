// SPDX-License-Identifier: AGPL-3.0-or-later

package buildday

import (
	"encoding/json"
	"time"
)

// Calibrate returns s as GET /api/v1/build-days answers a calibration
// volunteer while their account's flag hides their credited time
// (D-server-shadow; package shadow). Every part of a summary that comes of
// credited time goes, as the dashboard hides it: each day's a_ms and b_ms,
// whether it counted and when it was decided, the build days in the
// window, the weeks in a row, the freezes held, earned and owed, and each
// week's result and build days. What stays comes of the calendar and the
// account's own settings alone: the time zone, today, the weekly goal,
// the window, each day's date and deadline, each week's start and goal,
// and how freezes are earned and held. The hidden numbers are zero in the
// result, and its JSON leaves them out and says calibrating.
func Calibrate(s Summary) Summary {
	s.Calibrating = true
	s.BuildDays, s.WeeksInARow = 0, 0
	s.Freezes = Freezes{Max: s.Freezes.Max, Every: s.Freezes.Every}
	days := make([]Day, len(s.Days))
	for i, d := range s.Days {
		days[i] = Day{Date: d.Date, Deadline: d.Deadline}
	}
	s.Days = days
	weeks := make([]Week, len(s.Weeks))
	for i, w := range s.Weeks {
		weeks[i] = Week{Start: w.Start, Goal: w.Goal}
	}
	s.Weeks = weeks
	return s
}

// MarshalJSON writes s as GET /api/v1/build-days sends it. A calibrating
// summary leaves out every field Calibrate hides, rather than send zeros
// or a state, and says calibrating. Any other summary is written field
// for field as the Summary type has it.
func (s Summary) MarshalJSON() ([]byte, error) {
	type plain Summary
	if !s.Calibrating {
		return json.Marshal(plain(s))
	}
	type day struct {
		Date     string    `json:"date"`
		Deadline time.Time `json:"deadline"`
	}
	type week struct {
		Start string `json:"start"`
		Goal  int64  `json:"goal"`
	}
	type freezes struct {
		Max   int64 `json:"max"`
		Every int64 `json:"every"`
	}
	out := struct {
		Calibrating bool    `json:"calibrating"`
		TimeZone    string  `json:"time_zone"`
		Today       string  `json:"today"`
		WeeklyGoal  int64   `json:"weekly_goal"`
		WindowDays  int     `json:"window_days"`
		Freezes     freezes `json:"freezes"`
		Days        []day   `json:"days"`
		Weeks       []week  `json:"weeks"`
	}{
		Calibrating: true, TimeZone: s.TimeZone, Today: s.Today, WeeklyGoal: s.WeeklyGoal, WindowDays: s.WindowDays,
		Freezes: freezes{Max: s.Freezes.Max, Every: s.Freezes.Every},
		Days:    make([]day, len(s.Days)), Weeks: make([]week, len(s.Weeks)),
	}
	for i, d := range s.Days {
		out.Days[i] = day{Date: d.Date, Deadline: d.Deadline}
	}
	for i, w := range s.Weeks {
		out.Weeks[i] = week{Start: w.Start, Goal: w.Goal}
	}
	return json.Marshal(out)
}
