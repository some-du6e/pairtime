// SPDX-License-Identifier: AGPL-3.0-or-later

package dashboard

import (
	"context"
	"reflect"
	"testing"
	"time"
)

// demoNow is a moment the sample is viewed at.
var demoNow = time.Date(2026, 11, 20, 18, 30, 0, 0, time.UTC)

func demoView(t *testing.T, now time.Time) View {
	t.Helper()
	v, err := Demo{}.View(context.Background(), "", now)
	if err != nil {
		t.Fatalf("Demo.View: %v", err)
	}
	return v
}

// TestDemoAddsUp checks that every total the sample's page shows is the
// sum of the sample's rows, and that the totals agree with each other.
func TestDemoAddsUp(t *testing.T) {
	v := demoView(t, demoNow)
	rows := demoRows(demoNow)
	// sum adds up the rows of days [from, to] that keep says to.
	sum := func(from, to string, keep func(Row) bool) Time {
		var s Time
		for _, r := range rows {
			if r.Day >= from && r.Day <= to && keep(r) {
				if r.Pending {
					s.PendingS += r.Seconds
				} else {
					s.FinalS += r.Seconds
				}
			}
		}
		return s
	}
	yours := func(r Row) bool { return !r.Agent }
	agents := func(r Row) bool { return r.Agent }
	windows := []struct {
		name   string
		totals Totals
		days   []Day
	}{
		{name: "the week", totals: v.Week, days: v.Days[MonthDays-WeekDays:]},
		{name: "the month", totals: v.Month, days: v.Days},
	}
	for _, w := range windows {
		from, to := w.totals.From, w.totals.To
		t.Run(w.name+"'s time, yours and the agent's, is the sum of the sample's rows", func(t *testing.T) {
			if got, want := w.totals.You, sum(from, to, yours); got != want {
				t.Errorf("you %+v, want %+v", got, want)
			}
			if got, want := w.totals.Agent, sum(from, to, agents); got != want {
				t.Errorf("agent %+v, want %+v", got, want)
			}
		})
		t.Run(w.name+"'s projects each sum their rows, and together make the total", func(t *testing.T) {
			var you, agent Time
			for _, p := range w.totals.Projects {
				key := p.Project
				if got, want := p.You, sum(from, to, func(r Row) bool { return !r.Agent && r.Project == key }); got != want {
					t.Errorf("%s: you %+v, want %+v", key, got, want)
				}
				if got, want := p.Agent, sum(from, to, func(r Row) bool { return r.Agent && r.Project == key }); got != want {
					t.Errorf("%s: agent %+v, want %+v", key, got, want)
				}
				you, agent = you.Add(p.You), agent.Add(p.Agent)
			}
			if you != w.totals.You || agent != w.totals.Agent {
				t.Errorf("projects add up to you %+v and agent %+v, want %+v and %+v", you, agent, w.totals.You, w.totals.Agent)
			}
		})
		t.Run(w.name+"'s levels each sum their rows, and together make your time", func(t *testing.T) {
			var all Time
			for _, l := range w.totals.Levels {
				level := l.Level
				if got, want := l.Time, sum(from, to, func(r Row) bool { return !r.Agent && r.Level == level }); got != want {
					t.Errorf("level %s: %+v, want %+v", level, got, want)
				}
				all = all.Add(l.Time)
			}
			if all != w.totals.You {
				t.Errorf("levels add up to %+v, want %+v", all, w.totals.You)
			}
		})
		t.Run(w.name+"'s kinds each sum their rows, and together make your time", func(t *testing.T) {
			var all Time
			for _, k := range w.totals.Kinds {
				kind := k.Kind
				if got, want := k.Time, sum(from, to, func(r Row) bool { return !r.Agent && r.Kind == kind }); got != want {
					t.Errorf("kind %s: %+v, want %+v", kind, got, want)
				}
				all = all.Add(k.Time)
			}
			if all != w.totals.You {
				t.Errorf("kinds add up to %+v, want %+v", all, w.totals.You)
			}
		})
		t.Run(w.name+"'s days each sum their projects, and together make the total", func(t *testing.T) {
			var you, agent Time
			for _, d := range w.days {
				var dy, da Time
				for _, p := range d.Projects {
					dy, da = dy.Add(p.You), da.Add(p.Agent)
				}
				if dy != d.You || da != d.Agent {
					t.Errorf("%s: projects add up to %+v and %+v, the day says %+v and %+v", d.Date, dy, da, d.You, d.Agent)
				}
				if want := sum(d.Date, d.Date, yours); d.You != want {
					t.Errorf("%s: you %+v, want %+v", d.Date, d.You, want)
				}
				you, agent = you.Add(d.You), agent.Add(d.Agent)
			}
			if you != w.totals.You || agent != w.totals.Agent {
				t.Errorf("days add up to %+v and %+v, the window says %+v and %+v", you, agent, w.totals.You, w.totals.Agent)
			}
		})
	}
}

// TestDemoStory checks that the sample shows what /demo is for: a sample,
// labelled so, with each thing the page draws.
func TestDemoStory(t *testing.T) {
	v := demoView(t, demoNow)
	today := v.Days[MonthDays-1]
	// project returns key's entry in ps, or a zero one.
	project := func(key string, ps []ProjectTime) ProjectTime {
		for _, p := range ps {
			if p.Project == key {
				return p
			}
		}
		return ProjectTime{}
	}
	tests := []struct {
		name string
		ok   func() bool
	}{
		{name: "the view says it is a sample", ok: func() bool { return v.Sample }},
		{name: "the sample's made-up projects aren't split by device", ok: func() bool { return !v.SplitByDevice }},
		{name: "the agent is Claude Code", ok: func() bool { return v.Agent == "Claude Code" }},
		{name: "the sample has three projects, with names", ok: func() bool {
			return reflect.DeepEqual(v.Projects, []Project{
				{ID: demoChip8, Keys: []string{demoChip8}, Name: "chip8-emulator"},
				{ID: demoRobotics, Keys: []string{demoRobotics}, Name: "robotics-scouting"},
				{ID: demoPortfolio, Keys: []string{demoPortfolio}, Name: "portfolio-site"},
			})
		}},
		{name: "today's chess clock in chip8-emulator reads You 1h 12m, Claude Code 2h 40m, DESIGN 3.8's example", ok: func() bool {
			p := project(demoChip8, today.Projects)
			return p.You.Total() == 72*60 && p.Agent.Total() == 160*60
		}},
		{name: "some of today's minutes are still pending, and no earlier day's are", ok: func() bool {
			if today.You.PendingS != 9*60 || today.Agent.PendingS != 12*60 {
				return false
			}
			for _, d := range v.Days[:MonthDays-1] {
				if d.You.PendingS != 0 || d.Agent.PendingS != 0 {
					return false
				}
			}
			return true
		}},
		{name: "the week has time at each of levels A, B and C", ok: func() bool {
			for _, l := range v.Week.Levels {
				if l.Total() == 0 {
					return false
				}
			}
			return true
		}},
		{name: "most of the week is level A", ok: func() bool { return v.Week.Levels[0].Total() > v.Week.You.Total()/2 }},
		{name: "the week has time in every activity kind", ok: func() bool { return len(v.Week.Kinds) == len(Kinds) }},
		{name: "the month has more time than the week", ok: func() bool { return v.Month.You.Total() > v.Week.You.Total() }},
		{name: "the week has days off", ok: func() bool {
			for _, d := range v.Days[MonthDays-WeekDays:] {
				if d.You.Total() == 0 {
					return true
				}
			}
			return false
		}},
		{name: "no day passes the 12-hour cap on credited time (SM 8.6)", ok: func() bool {
			for _, d := range v.Days {
				if d.You.Total() > 43200 {
					return false
				}
			}
			return true
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.ok() {
				t.Error("it doesn't")
			}
		})
	}
}

func TestDemoDates(t *testing.T) {
	tests := []struct {
		name string
		now  time.Time
		want string
	}{
		{name: "the sample's last day is today", now: demoNow, want: "2026-11-20"},
		{name: "the sample's days are UTC days, whatever the zone of now", now: time.Date(2026, 11, 20, 20, 0, 0, 0, time.FixedZone("UTC-5", -5*3600)), want: "2026-11-21"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := demoView(t, tt.now)
			if v.Today != tt.want || v.Days[MonthDays-1].Date != tt.want {
				t.Errorf("today %s and last day %s, want %s", v.Today, v.Days[MonthDays-1].Date, tt.want)
			}
		})
	}
}

func TestSplit(t *testing.T) {
	tests := []struct {
		name    string
		total   int
		weights []int
		want    []int
	}{
		{name: "parts in proportion to the weights add up to the total", total: 100, weights: []int{50, 30, 20}, want: []int{50, 30, 20}},
		{name: "leftover units go to the largest remainders", total: 10, weights: []int{1, 1, 1}, want: []int{4, 3, 3}},
		{name: "a part with no weight gets nothing", total: 7, weights: []int{0, 1, 0, 1}, want: []int{0, 4, 0, 3}},
		{name: "nothing splits into nothing", total: 0, weights: []int{3, 4}, want: []int{0, 0}},
		{name: "no weight at all gives nothing", total: 9, weights: []int{0, 0}, want: []int{0, 0}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := split(tt.total, tt.weights); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("split(%d, %v) = %v, want %v", tt.total, tt.weights, got, tt.want)
			}
		})
	}
}
