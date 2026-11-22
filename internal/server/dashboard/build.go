// SPDX-License-Identifier: AGPL-3.0-or-later

package dashboard

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"time"
)

// Levels are the evidence levels in the order Totals.Levels lists them
// (DESIGN 6.4).
var Levels = []string{"A", "B", "C"}

// Kinds are the activity kinds of SM 5.1, in the order that breaks ties
// in Totals.Kinds.
var Kinds = []string{"prompting", "reading_output", "supervising", "reviewing", "testing", "debugging", "hand_edit", "lookup"}

// DateLayout is how a View writes a local date: YYYY-MM-DD.
const DateLayout = "2006-01-02"

// Row is one rollup row a View sums: seconds in one local day and one
// project. It is either the owner's credited time at one activity kind and
// evidence level, or the agent's time on its own, which has neither.
type Row struct {
	// Day is the local date, YYYY-MM-DD.
	Day string
	// Project is the project's id.
	Project string
	// Agent marks the agent's time on its own. Kind and Level are then "".
	Agent bool
	Kind  string
	Level string
	// Pending marks seconds of minutes still in their pending window.
	Pending bool
	Seconds float64
}

// Meta is what a View says besides its numbers.
type Meta struct {
	Sample bool
	AsOf   time.Time
	TZ     string
	// Today is the local date, YYYY-MM-DD.
	Today string
	Agent string
	// Names are the projects' display names, by id. A project without
	// one is called by its id.
	Names map[string]string
	// Codes are the projects' short codes, by id (Project.Code).
	Codes map[string]string
	// Keys are the device project keys of each project, by id
	// (Project.Keys).
	Keys map[string][]string
	// SplitByDevice is View.SplitByDevice.
	SplitByDevice bool
}

// noProject names the agent's time that no project claims.
const noProject = "No project"

// Build sums rows into a View. Rows outside the last MonthDays days are
// left out. It refuses a row whose seconds aren't a finite number >= 0,
// and a row of the owner's time whose level isn't A, B or C, so the levels
// always add up to the total.
func Build(m Meta, rows []Row) (View, error) {
	today, err := time.Parse(DateLayout, m.Today)
	if err != nil {
		return View{}, fmt.Errorf("dashboard: today %q isn't a date: %w", m.Today, err)
	}
	v := View{Sample: m.Sample, AsOf: m.AsOf, TZ: m.TZ, Today: m.Today, Agent: m.Agent, SplitByDevice: m.SplitByDevice}
	index := make(map[string]int, MonthDays)
	days := make([]*sums, MonthDays)
	for i := range days {
		d := today.AddDate(0, 0, i-(MonthDays-1)).Format(DateLayout)
		index[d] = i
		days[i] = newSums(d, d)
	}
	week := newSums(days[MonthDays-WeekDays].from, m.Today)
	month := newSums(days[0].from, m.Today)
	for _, r := range rows {
		if math.IsNaN(r.Seconds) || math.IsInf(r.Seconds, 0) || r.Seconds < 0 {
			return View{}, fmt.Errorf("dashboard: a row of %s has %v seconds", r.Day, r.Seconds)
		}
		if !r.Agent && !slices.Contains(Levels, r.Level) {
			return View{}, fmt.Errorf("dashboard: a row of %s has evidence level %q, not one of %q", r.Day, r.Level, Levels)
		}
		i, ok := index[r.Day]
		if !ok || r.Seconds == 0 {
			// A row with no seconds puts no project or kind on the page.
			continue
		}
		days[i].add(r)
		month.add(r)
		if i >= MonthDays-WeekDays {
			week.add(r)
		}
	}

	// Projects are ordered by the owner's time over the month, then the
	// agent's, then id, so the order never depends on the rows' order.
	// Every list is empty rather than nil, so the JSON has [] and no null.
	v.Projects = make([]Project, 0, len(month.projects))
	for id := range month.projects {
		name, ok := m.Names[id]
		switch {
		case ok && name != "":
		case id == "":
			name = noProject
		default:
			name = id
		}
		keys := slices.Clone(m.Keys[id])
		if keys == nil {
			keys = []string{}
		}
		v.Projects = append(v.Projects, Project{ID: id, Keys: keys, Name: name, Code: m.Codes[id]})
	}
	slices.SortFunc(v.Projects, func(a, b Project) int {
		pa, pb := month.projects[a.ID], month.projects[b.ID]
		if c := cmp.Compare(pb.You.Total(), pa.You.Total()); c != 0 {
			return c
		}
		if c := cmp.Compare(pb.Agent.Total(), pa.Agent.Total()); c != 0 {
			return c
		}
		return cmp.Compare(a.ID, b.ID)
	})
	order := make([]string, len(v.Projects))
	for i, p := range v.Projects {
		order[i] = p.ID
	}

	v.Days = make([]Day, MonthDays)
	for i, d := range days {
		v.Days[i] = Day{Date: d.from, You: d.you, Agent: d.agent, Projects: d.projectList(order)}
	}
	v.Week = week.totals(order)
	v.Month = month.totals(order)
	return v, nil
}

// sums adds up rows over a span of days.
type sums struct {
	from, to   string
	you, agent Time
	projects   map[string]*ProjectTime
	levels     map[string]Time
	kinds      map[string]Time
}

func newSums(from, to string) *sums {
	return &sums{
		from:     from,
		to:       to,
		projects: make(map[string]*ProjectTime),
		levels:   make(map[string]Time),
		kinds:    make(map[string]Time),
	}
}

func (s *sums) add(r Row) {
	t := Time{FinalS: r.Seconds}
	if r.Pending {
		t = Time{PendingS: r.Seconds}
	}
	p := s.projects[r.Project]
	if p == nil {
		p = &ProjectTime{Project: r.Project}
		s.projects[r.Project] = p
	}
	if r.Agent {
		s.agent = s.agent.Add(t)
		p.Agent = p.Agent.Add(t)
		return
	}
	s.you = s.you.Add(t)
	p.You = p.You.Add(t)
	s.levels[r.Level] = s.levels[r.Level].Add(t)
	s.kinds[r.Kind] = s.kinds[r.Kind].Add(t)
}

// projectList returns the projects with time, in order.
func (s *sums) projectList(order []string) []ProjectTime {
	out := []ProjectTime{}
	for _, key := range order {
		if p := s.projects[key]; p != nil {
			out = append(out, *p)
		}
	}
	return out
}

func (s *sums) totals(order []string) Totals {
	t := Totals{From: s.from, To: s.to, You: s.you, Agent: s.agent, Projects: s.projectList(order), Kinds: []KindTime{}}
	for _, l := range Levels {
		t.Levels = append(t.Levels, LevelTime{Level: l, Time: s.levels[l]})
	}
	for k, kt := range s.kinds {
		t.Kinds = append(t.Kinds, KindTime{Kind: k, Time: kt})
	}
	// The most time first. Ties go in SM's order of kinds, and a kind SM
	// doesn't list (from a newer scoring version) after those, by name.
	rank := func(k string) int {
		if i := slices.Index(Kinds, k); i >= 0 {
			return i
		}
		return len(Kinds)
	}
	slices.SortFunc(t.Kinds, func(a, b KindTime) int {
		if c := cmp.Compare(b.Total(), a.Total()); c != 0 {
			return c
		}
		if c := cmp.Compare(rank(a.Kind), rank(b.Kind)); c != 0 {
			return c
		}
		return cmp.Compare(a.Kind, b.Kind)
	})
	return t
}
