// SPDX-License-Identifier: AGPL-3.0-or-later

package dashboard

import (
	"cmp"
	"encoding/json"
	"slices"
	"time"
)

// Calibrate returns v as a calibration volunteer's dashboard shows it while
// their account's flag is on (D-server-shadow): what D-shadow-scope keeps
// on the volunteer's own computer, so nobody learns to steer the numbers
// the shadow week calibrates (SM 7.5). It hides the owner's credited and
// pending seconds everywhere, each day's, project's, week's and month's,
// and their split by evidence level and by activity kind, and keeps the
// agent's time on its own, the dates and the projects. The projects go in
// the order of the agent's time over the month, then name, then id, as the
// volunteer's own views order them while calibrating, since the order of
// the owner's time would rank what it hides. The view has no moments,
// loop codes, other uncredited codes or sessions, which D-shadow-scope
// also names. The hidden numbers are zero in the result, and its JSON
// leaves them out.
func Calibrate(v View) View {
	v.Calibrating = true
	month := make(map[string]Time, len(v.Month.Projects))
	for _, pt := range v.Month.Projects {
		month[pt.Project] = pt.Agent
	}
	v.Projects = slices.Clone(v.Projects)
	slices.SortFunc(v.Projects, func(a, b Project) int {
		if c := cmp.Compare(month[b.ID].Total(), month[a.ID].Total()); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Name, b.Name); c != 0 {
			return c
		}
		return cmp.Compare(a.ID, b.ID)
	})
	rank := make(map[string]int, len(v.Projects))
	for i, p := range v.Projects {
		rank[p.ID] = i
	}
	hide := func(pts []ProjectTime) []ProjectTime {
		out := make([]ProjectTime, len(pts))
		for i, pt := range pts {
			out[i] = ProjectTime{Project: pt.Project, Agent: pt.Agent}
		}
		slices.SortFunc(out, func(a, b ProjectTime) int { return cmp.Compare(rank[a.Project], rank[b.Project]) })
		return out
	}
	days := make([]Day, len(v.Days))
	for i, d := range v.Days {
		days[i] = Day{Date: d.Date, Agent: d.Agent, Projects: hide(d.Projects)}
	}
	v.Days = days
	for _, t := range []*Totals{&v.Week, &v.Month} {
		*t = Totals{From: t.From, To: t.To, Agent: t.Agent, Projects: hide(t.Projects), Levels: []LevelTime{}, Kinds: []KindTime{}}
	}
	return v
}

// MarshalJSON writes v as GET /api/v1/users/me/dashboard sends it. A
// calibrating view leaves out every field Calibrate hides, rather than
// send zeros, as the agent's local API does while shadow is on
// (D-shadow-scope), and says calibrating. Any other view is written field
// for field as the View type has it.
func (v View) MarshalJSON() ([]byte, error) {
	type plain View
	if !v.Calibrating {
		return json.Marshal(plain(v))
	}
	type agentTime struct {
		Project string `json:"project"`
		Agent   Time   `json:"agent"`
	}
	type day struct {
		Date     string      `json:"date"`
		Agent    Time        `json:"agent"`
		Projects []agentTime `json:"projects"`
	}
	type totals struct {
		From     string      `json:"from"`
		To       string      `json:"to"`
		Agent    Time        `json:"agent"`
		Projects []agentTime `json:"projects"`
	}
	agents := func(pts []ProjectTime) []agentTime {
		out := make([]agentTime, len(pts))
		for i, pt := range pts {
			out[i] = agentTime{Project: pt.Project, Agent: pt.Agent}
		}
		return out
	}
	window := func(t Totals) totals {
		return totals{From: t.From, To: t.To, Agent: t.Agent, Projects: agents(t.Projects)}
	}
	out := struct {
		Sample        bool      `json:"sample"`
		Calibrating   bool      `json:"calibrating"`
		AsOf          time.Time `json:"as_of"`
		TZ            string    `json:"tz"`
		Today         string    `json:"today"`
		Agent         string    `json:"agent"`
		SplitByDevice bool      `json:"projects_split_by_device"`
		Projects      []Project `json:"projects"`
		Days          []day     `json:"days"`
		Week          totals    `json:"week"`
		Month         totals    `json:"month"`
	}{
		Sample: v.Sample, Calibrating: true, AsOf: v.AsOf, TZ: v.TZ, Today: v.Today, Agent: v.Agent,
		SplitByDevice: v.SplitByDevice, Projects: v.Projects, Days: make([]day, len(v.Days)),
		Week: window(v.Week), Month: window(v.Month),
	}
	if out.Projects == nil {
		out.Projects = []Project{}
	}
	for i, d := range v.Days {
		out.Days[i] = day{Date: d.Date, Agent: d.Agent, Projects: agents(d.Projects)}
	}
	return json.Marshal(out)
}
