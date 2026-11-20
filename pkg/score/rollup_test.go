// SPDX-License-Identifier: AGPL-3.0-or-later

package score

import (
	"maps"
	"reflect"
	"slices"
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/some-du6e/pairtime/pkg/event"
)

// slotCredit is a credit slot at t: seconds credited to project p under P
// at level l, and the rest of the slot X.no_anchor.
func slotCredit(t int64, p string, l event.Evidence, seconds int64) Credit {
	c := Credit{Slot: t, Credited: []Credited{credOf(p, rP, l, seconds)}}
	if seconds < 15 {
		c.Uncredited = map[event.Reason]int64{xNoAnch: 15 - seconds}
	}
	return c
}

// creditMinutes is a Derived crediting project p a full minute, at level
// A, from each of the given minute starts.
func creditMinutes(p string, starts ...int64) Derived {
	var d Derived
	for _, m := range starts {
		for k := int64(0); k < 4; k++ {
			d.Credits = append(d.Credits, slotCredit(m+k*event.SlotMS, p, event.EvidenceA, 15))
			d.Slots = append(d.Slots, Slot{Start: m + k*event.SlotMS, FocusProject: p})
		}
	}
	return d
}

func joinDerived(ds ...Derived) Derived {
	var out Derived
	for _, d := range ds {
		out.Append(d)
	}
	return out
}

func TestWorkSessions(t *testing.T) {
	r := Rollups{Table: testTable(t)}
	type want struct {
		project  string
		from, to int64 // seconds after base
		credited float64
	}
	cases := []struct {
		name string
		d    Derived
		want []want
	}{
		{"credited minutes SESSION_GAP_S apart, end to start, are one work session",
			creditMinutes("p_1", at(0), at(1260)), []want{{"p_1", 0, 1320, 120}}},
		{"credited minutes more than SESSION_GAP_S apart are two",
			creditMinutes("p_1", at(0), at(1320)), []want{{"p_1", 0, 60, 60}, {"p_1", 1320, 1380, 60}}},
		{"each project has its own sessions, which another project's minute doesn't split",
			joinDerived(creditMinutes("p_1", at(0), at(300)), creditMinutes("p_2", at(180))),
			[]want{{"p_1", 0, 360, 120}, {"p_2", 180, 240, 60}}},
		{"a minute with nothing credited to the project starts no session", creditMinutes("p_1"), nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := r.WorkSessions(c.d)
			var have []want
			for _, ws := range got {
				if err := ws.Validate(); err != nil {
					t.Errorf("session %+v: %v", ws, err)
				}
				have = append(have, want{ws.ProjectKey, (ws.Start.UnixMilli() - base) / 1000, (ws.End.UnixMilli() - base) / 1000,
					ws.CreditedS})
			}
			if !reflect.DeepEqual(have, c.want) {
				t.Errorf("sessions %+v, want %+v", have, c.want)
			}
		})
	}
	t.Run("a session sums its project's agent time, agent-only time, the uncredited seconds of slots focused on it, and its anchors", func(t *testing.T) {
		in := credOf("p_1", rP, event.EvidenceA, 15)
		in.InTurn = true
		d := Derived{
			Slots: []Slot{{Start: at(0), FocusProject: "p_1"}, {Start: at(15), FocusProject: "p_1"}, {Start: at(30)}},
			Credits: []Credit{
				{Slot: at(0), Credited: []Credited{in}, AgentS: 15},
				{Slot: at(15), Uncredited: map[event.Reason]int64{xAgent: 15}, AgentS: 15, AgentOnly: map[string]int64{"p_1": 15}},
				{Slot: at(30), Uncredited: map[event.Reason]int64{xOther: 15}, AgentS: 15, AgentOnly: map[string]int64{"p_2": 15}},
			},
			Anchors: []Anchor{{ID: idA, Kind: event.AnchorSubstantivePrompt, T: at(10), ProjectKey: "p_1"},
				{ID: idB, Kind: event.AnchorGoAhead, T: at(20), ProjectKey: "p_2"}},
		}
		got := r.WorkSessions(d)
		if len(got) != 1 {
			t.Fatalf("sessions %+v, want one", got)
		}
		ws := got[0]
		if ws.CreditedS != 15 || ws.AgentRunningS != 30 || ws.AgentOnlyS != 15 ||
			!reflect.DeepEqual(ws.Uncredited, map[event.Reason]float64{xAgent: 15}) ||
			!reflect.DeepEqual(ws.AnchorCounts, map[event.AnchorKind]int{event.AnchorSubstantivePrompt: 1}) {
			t.Errorf("session %+v", ws)
		}
	})
}

func TestDays(t *testing.T) {
	tab := testTable(t)
	zone := time.FixedZone("UTC-6", -6*3600)
	// base is 18:00 on 2026-11-18 in zone, so local midnight is 6 h later.
	midnight := at(6 * 3600)
	full := func(l event.Evidence, slots int64) Derived {
		var d Derived
		for k := int64(0); k < slots; k++ {
			d.Credits = append(d.Credits, slotCredit(at(0)+k*event.SlotMS, "p_1", l, 15))
		}
		return d
	}
	// Zones where DST starts at local midnight west of UTC: the date that
	// starts it has no 00:00, and time.Date gives 23:00 of the day before
	// for it. time/tzdata keeps them loadable on any runner.
	tz := func(name string) *time.Location {
		loc, err := time.LoadLocation(name)
		if err != nil {
			t.Fatal(err)
		}
		return loc
	}
	santiago, havana := tz("America/Santiago"), tz("America/Havana")
	instant := func(s string) time.Time {
		tm, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		return tm
	}
	// minutesAt credits p_1 a full minute from each UTC minute start.
	minutesAt := func(starts ...string) Derived {
		var ms []int64
		for _, s := range starts {
			ms = append(ms, instant(s).UnixMilli())
		}
		return creditMinutes("p_1", ms...)
	}
	// daysAre checks each day's date, its span from start to end (UTC) and
	// its credit.
	type dayWant struct {
		date, start, end string
		credited         float64
	}
	daysAre := func(want ...dayWant) func([]Day) bool {
		return func(days []Day) bool {
			if len(days) != len(want) {
				return false
			}
			for i, d := range days {
				w := want[i]
				if d.Date != w.date || d.Start.UTC().Format(time.RFC3339) != w.start || d.End.UTC().Format(time.RFC3339) != w.end ||
					d.CreditedS != w.credited {
					return false
				}
			}
			return true
		}
	}
	cases := []struct {
		name    string
		r       Rollups
		d       Derived
		check   func(days []Day) bool
		explain string
	}{
		{"a minute belongs to the local day its start falls in", Rollups{Table: tab, Location: zone},
			joinDerived(creditMinutes("p_1", midnight-MinuteMS), creditMinutes("p_1", midnight)),
			func(days []Day) bool {
				return len(days) == 2 && days[0].Date == "2026-11-18" && days[1].Date == "2026-11-19" &&
					days[0].End.Equal(time.UnixMilli(midnight)) && days[1].Start.Equal(time.UnixMilli(midnight)) &&
					days[0].CreditedS == 60 && days[1].CreditedS == 60
			}, "two days split at local midnight, 60 s each"},
		{"without a profile time zone the day is UTC's", Rollups{Table: tab},
			creditMinutes("p_1", at(0)), func(days []Day) bool { return len(days) == 1 && days[0].Date == "2026-11-19" },
			"one day, 2026-11-19"},
		{"a day with BUILD_DAY_S credited at level A or B is a build day (DESIGN 6.2 D10)", Rollups{Table: tab},
			full(event.EvidenceB, 60), func(days []Day) bool { return len(days) == 1 && days[0].BuildDay },
			"a build day"},
		{"a day with less than BUILD_DAY_S at level A or B isn't", Rollups{Table: tab},
			full(event.EvidenceB, 59), func(days []Day) bool { return len(days) == 1 && !days[0].BuildDay },
			"no build day"},
		{"level-C credit doesn't make a build day", Rollups{Table: tab},
			full(event.EvidenceC, 60), func(days []Day) bool { return len(days) == 1 && !days[0].BuildDay },
			"no build day"},
		{"in a backfilled minute credit counts at level C, which doesn't make a build day", Rollups{Table: tab,
			Witness: func(int64) event.Witness { return event.Backfilled }},
			full(event.EvidenceA, 60), func(days []Day) bool {
				return len(days) == 1 && !days[0].BuildDay &&
					reflect.DeepEqual(days[0].CreditedByEvidence, map[event.Evidence]float64{event.EvidenceC: 900})
			}, "900 s at level C and no build day"},
		{"a day crediting DAY_FLAG_S raises no review flag", Rollups{Table: tab},
			full(event.EvidenceA, 2400), func(days []Day) bool { return len(days) == 1 && !days[0].ReviewFlag },
			"no review flag at 36,000 s"},
		{"a day crediting more than DAY_FLAG_S raises a review flag (SM 5.3)", Rollups{Table: tab},
			full(event.EvidenceA, 2401), func(days []Day) bool { return len(days) == 1 && days[0].ReviewFlag },
			"a review flag at 36,015 s"},
		{"each project's sums are beside the day's, which splits its credit by project", Rollups{Table: tab},
			joinDerived(creditMinutes("p_1", at(0)), creditMinutes("p_2", at(60), at(120))),
			func(days []Day) bool {
				if len(days) != 1 || len(days[0].Projects) != 2 {
					return false
				}
				p := days[0].Projects
				return p[0].ProjectKey == "p_1" && p[0].CreditedS == 60 && p[1].ProjectKey == "p_2" && p[1].CreditedS == 120 &&
					reflect.DeepEqual(days[0].CreditedByProject, map[string]float64{"p_1": 60, "p_2": 120})
			}, "p_1 60 s and p_2 120 s"},
		// 23:30 -04 on 2026-09-05, an hour before DST skips 2026-09-06's
		// midnight, then 01:00 -03 on 2026-09-06, the first minute after it.
		{"America/Santiago: a minute at 23:30 on 2026-09-05, the hour time.Date gives for 2026-09-06's missing midnight, is on 2026-09-05, and 2026-09-06 starts at 01:00 -03",
			Rollups{Table: tab, Location: santiago}, minutesAt("2026-09-06T03:30:00Z", "2026-09-06T04:00:00Z"),
			daysAre(dayWant{"2026-09-05", "2026-09-05T04:00:00Z", "2026-09-06T04:00:00Z", 60},
				dayWant{"2026-09-06", "2026-09-06T04:00:00Z", "2026-09-07T03:00:00Z", 60}),
			"2026-09-05 until the change at 04:00Z and 2026-09-06 from it, 60 s each"},
		// 23:30 -04 on 2027-09-04, then 01:00 -03 and 23:30 -03 on
		// 2027-09-05, the first and last hours of a date with no 00:00.
		{"America/Santiago: 2027-09-05, whose midnight DST skips, is labeled 2027-09-05 and keeps a minute in its last hour, 23:30 -03",
			Rollups{Table: tab, Location: santiago}, minutesAt("2027-09-05T03:30:00Z", "2027-09-05T04:00:00Z", "2027-09-06T02:30:00Z"),
			daysAre(dayWant{"2027-09-04", "2027-09-04T04:00:00Z", "2027-09-05T04:00:00Z", 60},
				dayWant{"2027-09-05", "2027-09-05T04:00:00Z", "2027-09-06T03:00:00Z", 120}),
			"2027-09-04 with 60 s, then 2027-09-05 from the change at 04:00Z to 03:00Z with 120 s"},
		// 23:30 -05 on 2026-03-07, an hour before DST skips 2026-03-08's
		// midnight, then 01:00 -04 on 2026-03-08.
		{"America/Havana: a minute at 23:30 on 2026-03-07, the hour time.Date gives for 2026-03-08's missing midnight, is on 2026-03-07, and 2026-03-08 starts at 01:00 -04",
			Rollups{Table: tab, Location: havana}, minutesAt("2026-03-08T04:30:00Z", "2026-03-08T05:00:00Z"),
			daysAre(dayWant{"2026-03-07", "2026-03-07T05:00:00Z", "2026-03-08T05:00:00Z", 60},
				dayWant{"2026-03-08", "2026-03-08T05:00:00Z", "2026-03-09T04:00:00Z", 60}),
			"2026-03-07 until the change at 05:00Z and 2026-03-08 from it, 60 s each"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			days := c.r.Days(c.d)
			for _, d := range days {
				if err := d.Validate(); err != nil {
					t.Errorf("day %s: %v", d.Date, err)
				}
			}
			if !c.check(days) {
				t.Errorf("days %+v, want %s", days, c.explain)
			}
		})
	}
}

func TestProjectCommits(t *testing.T) {
	b := newTL(false)
	commit := func(tm int64, project string, op event.GitOp, sha string) {
		b.emit(tm, event.Envelope{Source: event.SourceGit, Via: event.ViaGit, Harness: event.HarnessNone,
			SessionKey: event.NoSession, ProjectKey: project}, &event.GitChange{Op: op, SHA: sha, Actor: event.ActorHuman})
	}
	sha := func(c byte) string { return string(slices.Repeat([]byte{c}, 40)) }
	commit(at(100), "p_1", event.GitCommit, sha('1'))
	commit(at(250), "p_1", event.GitCommit, sha('2'))
	// The amend replaces the commit at 250 s.
	commit(at(300), "p_1", event.GitAmend, sha('3'))
	// Another project's commit and a rebase move nothing.
	commit(at(200), "p_2", event.GitCommit, sha('4'))
	commit(at(280), "p_1", event.GitRebase, sha('5'))
	d := Derived{Credits: []Credit{
		{Slot: at(0), Credited: []Credited{credOf("p_1", rP, event.EvidenceA, 15)}},
		{Slot: at(90), Credited: []Credited{credOf("p_1", event.ReasonEdit, event.EvidenceA, 15)}},
		{Slot: at(105), Credited: []Credited{credOf("p_1", rREAD, event.EvidenceA, 15)}},
		{Slot: at(240), Credited: []Credited{credOf("p_1", rG, event.EvidenceA, 15)}},
		{Slot: at(255), Credited: []Credited{credOf("p_1", rP, event.EvidenceA, 15)}},
		{Slot: at(300), Credited: []Credited{credOf("p_1", rP, event.EvidenceA, 15)}},
	}}
	got := Rollups{Table: testTable(t)}.Projects(d, b.records())
	if len(got) != 1 {
		t.Fatalf("project rollups %+v, want p_1's", got)
	}
	p := got[0]
	type commitWant struct {
		sha      string
		credited float64
		byKind   map[event.Activity]float64
	}
	var have []commitWant
	for _, c := range p.Commits {
		have = append(have, commitWant{c.SHA, c.CreditedS, c.CreditedByKind})
	}
	cases := []struct {
		name string
		ok   bool
	}{
		{"the project rollup passes pkg/event validation and credits all 90 s", p.Validate() == nil && p.CreditedS == 90},
		{"each slot's credit goes to the first commit after the slot's start, and an amend takes the commit it replaces (SM 3.5)",
			reflect.DeepEqual(have, []commitWant{
				{sha('1'), 30, map[event.Activity]float64{event.ActivityPrompting: 15, event.ActivityHandEdit: 15}},
				{sha('3'), 45, map[event.Activity]float64{event.ActivityReadingOutput: 15, event.ActivityReviewing: 15,
					event.ActivityPrompting: 15}},
			})},
		{"credit after the last commit is uncommitted", p.UncommittedS == 15 &&
			reflect.DeepEqual(p.UncommittedByKind, map[event.Activity]float64{event.ActivityPrompting: 15})},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !c.ok {
				t.Errorf("project rollup %+v, commits %+v", p, have)
			}
		})
	}
}

// TestFixtureRollups checks each fixture's rollups against each other and
// its minutes: every one is a valid pkg/event rollup, the days credit what
// the minutes do, each project's work sessions what its project rollup
// does, and each day's projects what the day does.
func TestFixtureRollups(t *testing.T) {
	for _, f := range creditCases(t) {
		d := scoreAll(t, fixtureDevice(f), f.Events)
		r := Rollups{Table: testTable(t), Witness: func(int64) event.Witness { return f.Expected.Witness }}
		var total float64
		byProject := map[string]float64{}
		for _, m := range Minutes(d) {
			for p, n := range m.creditedHalves() {
				total += float64(n) / 2
				byProject[p] += float64(n) / 2
			}
		}
		days, sessions, projects := r.Days(d), r.WorkSessions(d), r.Projects(d, f.Events)
		t.Run(f.Name+": every rollup passes pkg/event validation", func(t *testing.T) {
			for _, x := range days {
				if err := x.Validate(); err != nil {
					t.Errorf("day %s: %v", x.Date, err)
				}
			}
			for _, x := range sessions {
				if err := x.Validate(); err != nil {
					t.Errorf("work session %s from %s: %v", x.ProjectKey, x.Start, err)
				}
			}
			for _, x := range projects {
				if err := x.Validate(); err != nil {
					t.Errorf("project %s: %v", x.ProjectKey, err)
				}
			}
		})
		t.Run(f.Name+": the days credit what the minutes do, and each day's projects what the day does", func(t *testing.T) {
			var sum float64
			for _, x := range days {
				sum += x.CreditedS
				var parts float64
				for _, p := range x.Projects {
					parts += p.CreditedS
				}
				if parts != x.CreditedS {
					t.Errorf("day %s credits %v s, its projects %v s", x.Date, x.CreditedS, parts)
				}
			}
			if sum != total {
				t.Errorf("the days credit %v s, the minutes %v s", sum, total)
			}
		})
		t.Run(f.Name+": each project's work sessions and its project rollup credit what its minutes do", func(t *testing.T) {
			fromSessions := map[string]float64{}
			for _, x := range sessions {
				fromSessions[x.ProjectKey] += x.CreditedS
			}
			fromProjects := map[string]float64{}
			for _, x := range projects {
				fromProjects[x.ProjectKey] = x.CreditedS
				var commits float64
				for _, c := range x.Commits {
					commits += c.CreditedS
				}
				if commits+x.UncommittedS != x.CreditedS {
					t.Errorf("project %s credits %v s, its commits %v s and uncommitted %v s", x.ProjectKey, x.CreditedS,
						commits, x.UncommittedS)
				}
			}
			for _, p := range slices.Sorted(maps.Keys(byProject)) {
				if fromSessions[p] != byProject[p] || fromProjects[p] != byProject[p] {
					t.Errorf("project %s: sessions %v s, project rollup %v s, minutes %v s", p, fromSessions[p], fromProjects[p],
						byProject[p])
				}
			}
		})
	}
}
