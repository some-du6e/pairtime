// SPDX-License-Identifier: AGPL-3.0-or-later

package dashboard

import (
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
)

// meta is a Meta for 2026-11-20.
func meta() Meta {
	return Meta{AsOf: time.Date(2026, 11, 20, 18, 0, 0, 0, time.UTC), TZ: "UTC", Today: "2026-11-20", Agent: "Claude Code"}
}

func TestBuild(t *testing.T) {
	you := func(day, project, kind, level string, s float64) Row {
		return Row{Day: day, Project: project, Kind: kind, Level: level, Seconds: s}
	}
	agent := func(day, project string, s float64) Row {
		return Row{Day: day, Project: project, Agent: true, Seconds: s}
	}
	pending := func(r Row) Row {
		r.Pending = true
		return r
	}
	tests := []struct {
		name    string
		meta    func(*Meta)
		rows    []Row
		check   func(*testing.T, View)
		wantErr string
	}{
		{
			name: "the days are the last 30 local days, oldest first, ending today",
			check: func(t *testing.T, v View) {
				if len(v.Days) != MonthDays {
					t.Fatalf("%d days, want %d", len(v.Days), MonthDays)
				}
				if v.Days[0].Date != "2026-10-22" || v.Days[MonthDays-1].Date != "2026-11-20" {
					t.Errorf("days run %s to %s, want 2026-10-22 to 2026-11-20", v.Days[0].Date, v.Days[MonthDays-1].Date)
				}
				if v.Week.From != "2026-11-14" || v.Week.To != "2026-11-20" || v.Month.From != "2026-10-22" || v.Month.To != "2026-11-20" {
					t.Errorf("week %s to %s and month %s to %s", v.Week.From, v.Week.To, v.Month.From, v.Month.To)
				}
			},
		},
		{
			name: "the days count across a change of month and of daylight saving time, since they are dates",
			meta: func(m *Meta) { m.Today = "2026-03-30" },
			check: func(t *testing.T, v View) {
				if v.Days[0].Date != "2026-03-01" || v.Days[MonthDays-2].Date != "2026-03-29" {
					t.Errorf("days start %s and end the day before today on %s", v.Days[0].Date, v.Days[MonthDays-2].Date)
				}
			},
		},
		{
			name: "rows outside the last 30 days are left out, and rows outside the last 7 count only toward the month",
			rows: []Row{
				you("2026-10-21", "p1", "prompting", "A", 600),
				you("2026-10-22", "p1", "prompting", "A", 60),
				you("2026-11-13", "p1", "prompting", "A", 120),
				you("2026-11-14", "p1", "prompting", "A", 240),
				you("2026-11-21", "p1", "prompting", "A", 900),
			},
			check: func(t *testing.T, v View) {
				if got := v.Month.You.FinalS; got != 420 {
					t.Errorf("month %v s, want 420", got)
				}
				if got := v.Week.You.FinalS; got != 240 {
					t.Errorf("week %v s, want 240", got)
				}
			},
		},
		{
			name: "pending seconds stay apart from final ones in the day, the project, the level and the kind",
			rows: []Row{
				you("2026-11-20", "p1", "prompting", "A", 300),
				pending(you("2026-11-20", "p1", "prompting", "A", 45)),
				agent("2026-11-20", "p1", 600),
				pending(agent("2026-11-20", "p1", 30)),
			},
			check: func(t *testing.T, v View) {
				want := Time{FinalS: 300, PendingS: 45}
				today := v.Days[MonthDays-1]
				for what, got := range map[string]Time{
					"the day":           today.You,
					"the day's project": today.Projects[0].You,
					"the week":          v.Week.You,
					"level A":           v.Week.Levels[0].Time,
					"prompting":         v.Week.Kinds[0].Time,
				} {
					if got != want {
						t.Errorf("%s has %+v, want %+v", what, got, want)
					}
				}
				if got := v.Week.Agent; got != (Time{FinalS: 600, PendingS: 30}) {
					t.Errorf("the agent's week %+v, want 600 final and 30 pending", got)
				}
			},
		},
		{
			name: "projects go by the owner's time over the month, then the agent's, then their keys",
			rows: []Row{
				you("2026-11-01", "p-small", "prompting", "A", 60),
				you("2026-11-20", "p-big", "prompting", "A", 600),
				agent("2026-11-20", "p-agent-more", 900),
				agent("2026-11-20", "p-agent-less", 300),
				agent("2026-11-20", "p-tie-b", 100),
				agent("2026-11-20", "p-tie-a", 100),
			},
			check: func(t *testing.T, v View) {
				var got []string
				for _, p := range v.Projects {
					got = append(got, p.Key)
				}
				want := []string{"p-big", "p-small", "p-agent-more", "p-agent-less", "p-tie-a", "p-tie-b"}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("projects %q, want %q", got, want)
				}
				if v.Week.Projects[0].Project != "p-big" || len(v.Week.Projects) != 5 {
					t.Errorf("the week's projects %+v, want the five with time this week, p-big first", v.Week.Projects)
				}
			},
		},
		{
			name: "a project is called by its name, or by its key without one, and agent time without a project is No project",
			meta: func(m *Meta) { m.Names = map[string]string{"p1": "chip8-emulator", "p2": ""} },
			rows: []Row{
				you("2026-11-20", "p1", "prompting", "A", 600),
				you("2026-11-20", "p2", "prompting", "A", 500),
				you("2026-11-20", "p3", "prompting", "A", 400),
				agent("2026-11-20", "", 300),
			},
			check: func(t *testing.T, v View) {
				want := []Project{{Key: "p1", Name: "chip8-emulator"}, {Key: "p2", Name: "p2"}, {Key: "p3", Name: "p3"}, {Key: "", Name: "No project"}}
				if !reflect.DeepEqual(v.Projects, want) {
					t.Errorf("projects %+v, want %+v", v.Projects, want)
				}
			},
		},
		{
			name: "the levels are always A, B and C in that order, even with no time at a level",
			rows: []Row{you("2026-11-20", "p1", "testing", "C", 90)},
			check: func(t *testing.T, v View) {
				want := []LevelTime{{Level: "A"}, {Level: "B"}, {Level: "C", Time: Time{FinalS: 90}}}
				if !reflect.DeepEqual(v.Week.Levels, want) {
					t.Errorf("levels %+v, want %+v", v.Week.Levels, want)
				}
			},
		},
		{
			name: "kinds go by time, ties in SM's order of kinds, then kinds SM doesn't list by name, and kinds with no time are left out",
			rows: []Row{
				you("2026-11-20", "p1", "testing", "A", 100),
				you("2026-11-20", "p1", "prompting", "A", 100),
				you("2026-11-20", "p1", "zeta_new", "A", 100),
				you("2026-11-20", "p1", "alpha_new", "A", 100),
				you("2026-11-20", "p1", "hand_edit", "B", 400),
				you("2026-11-20", "p1", "lookup", "A", 0),
			},
			check: func(t *testing.T, v View) {
				var got []string
				for _, k := range v.Week.Kinds {
					got = append(got, k.Kind)
				}
				want := []string{"hand_edit", "prompting", "testing", "alpha_new", "zeta_new"}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("kinds %q, want %q", got, want)
				}
			},
		},
		{
			name: "a row with no seconds puts no project on the page",
			rows: []Row{you("2026-11-20", "p-empty", "prompting", "A", 0)},
			check: func(t *testing.T, v View) {
				if len(v.Projects) != 0 || len(v.Days[MonthDays-1].Projects) != 0 {
					t.Errorf("projects %+v and today's %+v, want none", v.Projects, v.Days[MonthDays-1].Projects)
				}
			},
		},
		{
			name: "the agent's time counts beside the owner's and never in the levels or kinds",
			rows: []Row{
				you("2026-11-20", "p1", "supervising", "A", 120),
				agent("2026-11-20", "p1", 3000),
			},
			check: func(t *testing.T, v View) {
				var levels, kinds float64
				for _, l := range v.Week.Levels {
					levels += l.Total()
				}
				for _, k := range v.Week.Kinds {
					kinds += k.Total()
				}
				if levels != 120 || kinds != 120 || v.Week.You.Total() != 120 || v.Week.Agent.Total() != 3000 {
					t.Errorf("levels %v, kinds %v, you %v, agent %v; want 120, 120, 120 and 3000", levels, kinds, v.Week.You.Total(), v.Week.Agent.Total())
				}
			},
		},
		{
			name:    "a row of the owner's time with a level that isn't A, B or C is refused, so the levels add up",
			rows:    []Row{you("2026-11-20", "p1", "prompting", "D", 60)},
			wantErr: `evidence level "D"`,
		},
		{
			name:    "a row of the owner's time with no level is refused",
			rows:    []Row{you("2026-11-20", "p1", "prompting", "", 60)},
			wantErr: `evidence level ""`,
		},
		{
			name:    "negative seconds are refused",
			rows:    []Row{agent("2026-11-20", "p1", -1)},
			wantErr: "-1 seconds",
		},
		{
			name:    "seconds that aren't a number are refused",
			rows:    []Row{agent("2026-11-20", "p1", math.NaN())},
			wantErr: "NaN seconds",
		},
		{
			name:    "a today that isn't a date is refused",
			meta:    func(m *Meta) { m.Today = "20 Nov 2026" },
			wantErr: "isn't a date",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := meta()
			if tt.meta != nil {
				tt.meta(&m)
			}
			v, err := Build(m, tt.rows)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Build = %v, want an error containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			if tt.check != nil {
				tt.check(t, v)
			}
		})
	}
}
