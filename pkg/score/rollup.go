// SPDX-License-Identifier: AGPL-3.0-or-later

package score

import (
	"cmp"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/some-du6e/pairtime/pkg/event"
	"github.com/some-du6e/pairtime/pkg/params"
)

// Rollups sums what the scorer derived into SM 5.3's rollups: work
// sessions, local days and projects. Each one sums the credit slots of the
// minutes it spans, the level below a minute bucket, so its credit by
// reason and by kind is exact where a bucket keeps only each slot's
// dominant reason. Credited seconds in a backfilled minute count at level
// C, as its bucket says (DESIGN 6.4).
//
// The rollups don't apply DAY_CAP_S or the uncommitted cap, which WP-SC6
// adds with the no-progress weight (docs/PLAN.md).
type Rollups struct {
	// Table is the parameter table the records were scored with.
	Table *params.Table
	// Witness gives each minute's witness label, as for Revisions. Nil
	// labels every minute witnessed.
	Witness func(minute int64) event.Witness
	// Location is the profile's time zone, whose days the day rollups
	// follow (SM 5.3, DESIGN 6.2 D10). Nil means UTC.
	Location *time.Location
}

func (r Rollups) backfilled(minute int64) bool {
	return r.Witness != nil && r.Witness(minute) == event.Backfilled
}

// tally is the additive part of a rollup: credit in half-seconds, the rest
// in seconds.
type tally struct {
	halves     int64
	byReason   map[event.Reason]int64
	byKind     map[event.Activity]int64
	byLevel    map[event.Evidence]int64
	byProject  map[string]int64
	uncredited map[event.Reason]int64
	agent      int64
	agentOnly  int64
	anchors    map[event.AnchorKind]int
}

func newTally() *tally {
	return &tally{byReason: map[event.Reason]int64{}, byKind: map[event.Activity]int64{},
		byLevel: map[event.Evidence]int64{}, byProject: map[string]int64{}, uncredited: map[event.Reason]int64{},
		anchors: map[event.AnchorKind]int{}}
}

// addSlot adds one slot. With project "", everything in the slot counts:
// all its credit, all its uncredited seconds, agent_running_s for any
// project and every project's agent-only time. With a project, only what
// is that project's counts: its credit, the uncredited seconds of a slot
// whose focus is on it, the seconds a turn of it ran (its credit inside
// its turns and its agent-only time) and its agent-only time (SM 2.6).
func (t *tally) addSlot(s MinuteSlot, project string, backfilled bool) {
	for _, e := range s.Credited {
		if project != "" && e.ProjectKey != project {
			continue
		}
		t.halves += e.Halves
		t.byReason[e.Reason] += e.Halves
		t.byKind[e.Reason.Activity()] += e.Halves
		level := e.Level
		if backfilled {
			level = event.EvidenceC
		}
		t.byLevel[level] += e.Halves
		t.byProject[e.ProjectKey] += e.Halves
		if project != "" && e.InTurn {
			t.agent += e.Seconds
		}
	}
	if project == "" {
		for r, n := range s.Uncredited {
			t.uncredited[r] += n
		}
		t.agent += s.AgentS
		for _, n := range s.AgentOnly {
			t.agentOnly += n
		}
		return
	}
	if s.FocusProject == project {
		for r, n := range s.Uncredited {
			t.uncredited[r] += n
		}
	}
	n := s.AgentOnly[project]
	t.agent += n
	t.agentOnly += n
}

// addAnchors counts the anchors of project, or of every project for "",
// whose acts fall in [from, to).
func (t *tally) addAnchors(anchors []Anchor, project string, from, to int64) {
	for _, a := range anchors {
		if a.T >= from && a.T < to && (project == "" || a.ProjectKey == project) {
			t.anchors[a.Kind]++
		}
	}
}

// halvesOf converts a map of half-seconds to seconds, leaving out zeros.
func halvesOf[K comparable](m map[K]int64) map[K]float64 {
	var out map[K]float64
	for k, n := range m {
		if n == 0 {
			continue
		}
		if out == nil {
			out = map[K]float64{}
		}
		out[k] = float64(n) / 2
	}
	return out
}

// secondsOf converts a map of seconds to floats, leaving out zeros.
func secondsOf[K comparable](m map[K]int64) map[K]float64 {
	var out map[K]float64
	for k, n := range m {
		if n == 0 {
			continue
		}
		if out == nil {
			out = map[K]float64{}
		}
		out[k] = float64(n)
	}
	return out
}

// rollup returns the tally as pkg/event's rollup at level, spanning [from,
// to).
func (t *tally) rollup(tab *params.Table, level event.RollupLevel, project string, from, to int64) event.Rollup {
	r := event.Rollup{V: Version(), Params: tab.Hash(), Level: level, ProjectKey: project,
		Start: time.UnixMilli(from).UTC(), End: time.UnixMilli(to).UTC(), CreditedS: float64(t.halves) / 2,
		CreditedByReason: halvesOf(t.byReason), CreditedByKind: halvesOf(t.byKind), Uncredited: secondsOf(t.uncredited),
		CreditedByEvidence: halvesOf(t.byLevel), AgentRunningS: float64(t.agent)}
	if level == event.LevelDay || level == event.LevelSubmission {
		r.CreditedByProject = halvesOf(t.byProject)
	}
	for k, n := range t.anchors {
		if n == 0 {
			continue
		}
		if r.AnchorCounts == nil {
			r.AnchorCounts = map[event.AnchorKind]int{}
		}
		r.AnchorCounts[k] = n
	}
	return r
}

// WorkSession is SM 5.3's work session: a maximal run of credited minutes
// in one project, split where two credited minutes are more than
// SESSION_GAP_S apart. Not a harness session.
type WorkSession struct {
	event.Rollup
	// AgentOnlyS is the project's agent-only time in the session (SM 2.6):
	// the seconds a turn of it ran that weren't credited to it.
	AgentOnlyS float64 `json:"agent_only_s"`
	// Flags are the review flags raised in the session's span by the
	// project's harness sessions (SM 8.7). They change review priority,
	// never credit.
	Flags []Flag `json:"flags,omitempty"`
}

// WorkSessions returns the work sessions in what d derived, by start, then
// project. Two credited minutes of a project are in one session when the
// time between the end of the first and the start of the second is at
// most SESSION_GAP_S. A session spans its first credited minute to the end
// of its last, and sums every minute in that span for its project:
// credit, uncredited seconds of slots focused on it, agent time and
// anchors.
func (r Rollups) WorkSessions(d Derived) []WorkSession {
	ms := Minutes(d)
	gap := r.Table.Int(params.SESSION_GAP_S) * 1000
	sessionOf := map[string]string{}
	for _, tu := range d.Turns {
		if tu.ProjectKey != "" {
			sessionOf[tu.SessionKey] = tu.ProjectKey
		}
	}
	var out []WorkSession
	for _, p := range creditedProjects(ms) {
		var spans []span
		for _, m := range ms {
			if m.creditedHalves()[p] == 0 {
				continue
			}
			if n := len(spans); n > 0 && m.Start-spans[n-1].to <= gap {
				spans[n-1].to = m.End()
				continue
			}
			spans = append(spans, span{m.Start, m.End()})
		}
		for _, sp := range spans {
			t := newTally()
			for _, m := range ms {
				if m.Start < sp.from || m.Start >= sp.to {
					continue
				}
				for _, s := range m.Slots {
					t.addSlot(s, p, r.backfilled(m.Start))
				}
			}
			t.addAnchors(d.Anchors, p, sp.from, sp.to)
			ws := WorkSession{Rollup: t.rollup(r.Table, event.LevelWorkSession, p, sp.from, sp.to),
				AgentOnlyS: float64(t.agentOnly)}
			for _, f := range d.Flags {
				if f.T >= sp.from && f.T < sp.to && sessionOf[f.SessionKey] == p {
					ws.Flags = append(ws.Flags, f)
				}
			}
			out = append(out, ws)
		}
	}
	slices.SortStableFunc(out, func(a, b WorkSession) int {
		if c := a.Start.Compare(b.Start); c != 0 {
			return c
		}
		return strings.Compare(a.ProjectKey, b.ProjectKey)
	})
	return out
}

// creditedProjects returns every project with credit in ms, sorted.
func creditedProjects(ms []Minute) []string {
	seen := map[string]bool{}
	for _, m := range ms {
		for p, n := range m.creditedHalves() {
			if n > 0 {
				seen[p] = true
			}
		}
	}
	return slices.Sorted(maps.Keys(seen))
}

// Day is SM 5.3's day rollup: one local day of the profile's time zone,
// across projects, with each project's own sums beside it (DESIGN 3.5's
// days/YYYY-MM-DD.json).
type Day struct {
	event.Rollup
	// Date is the local date, YYYY-MM-DD.
	Date string `json:"date"`
	// AgentOnlyS is the agent-only time of every project (SM 2.6).
	AgentOnlyS float64 `json:"agent_only_s"`
	// Projects holds each project's sums for the day, by project key.
	Projects []DayProject `json:"projects,omitempty"`
	// BuildDay says the day has BUILD_DAY_S credited seconds at level A or
	// B, as a build day needs (DESIGN 6.2 D10). Whether the server received
	// them within BUILD_DAY_LAG_S is the server's to say.
	BuildDay bool `json:"build_day"`
	// ReviewFlag says the day credits more than DAY_FLAG_S, which raises a
	// review flag (SM 5.3).
	ReviewFlag bool `json:"review_flag,omitempty"`
}

// DayProject is one project's part of a day.
type DayProject struct {
	ProjectKey         string                     `json:"project_key"`
	CreditedS          float64                    `json:"credited_s"`
	CreditedByReason   map[event.Reason]float64   `json:"credited_by_reason,omitempty"`
	CreditedByKind     map[event.Activity]float64 `json:"credited_by_kind,omitempty"`
	CreditedByEvidence map[event.Evidence]float64 `json:"credited_by_evidence,omitempty"`
	Uncredited         map[event.Reason]float64   `json:"uncredited,omitempty"`
	AgentRunningS      float64                    `json:"agent_running_s"`
	AgentOnlyS         float64                    `json:"agent_only_s"`
	AnchorCounts       map[event.AnchorKind]int   `json:"anchor_counts,omitempty"`
}

// Days returns a day rollup for each local day with a derived slot, in
// order. A minute belongs to the local date its start falls on, and a day
// runs from the first instant of its date to the first instant of the next
// (dayStart), so it can be 23 or 25 hours long.
func (r Rollups) Days(d Derived) []Day {
	loc := r.Location
	if loc == nil {
		loc = time.UTC
	}
	ms := Minutes(d)
	buildDay := r.Table.Int(params.BUILD_DAY_S) * 2
	flagDay := r.Table.Int(params.DAY_FLAG_S) * 2
	var out []Day
	for i := 0; i < len(ms); {
		y, mo, dd := time.UnixMilli(ms[i].Start).In(loc).Date()
		// The day takes ms[i] and every minute after it on the same local
		// date, so each pass moves on by at least one minute.
		j := i + 1
		for j < len(ms) && onDate(time.UnixMilli(ms[j].Start).In(loc), y, mo, dd) {
			j++
		}
		day := ms[i:j]
		i = j
		date := time.Date(y, mo, dd, 0, 0, 0, 0, time.UTC)
		ny, nmo, ndd := date.AddDate(0, 0, 1).Date()
		from, to := dayStart(y, mo, dd, loc).UnixMilli(), dayStart(ny, nmo, ndd, loc).UnixMilli()
		all := newTally()
		for _, m := range day {
			for _, s := range m.Slots {
				all.addSlot(s, "", r.backfilled(m.Start))
			}
		}
		all.addAnchors(d.Anchors, "", from, to)
		out = append(out, Day{
			Rollup:     all.rollup(r.Table, event.LevelDay, "", from, to),
			Date:       date.Format(time.DateOnly),
			AgentOnlyS: float64(all.agentOnly),
			Projects:   r.dayProjects(d, day, from, to),
			BuildDay:   all.byLevel[event.EvidenceA]+all.byLevel[event.EvidenceB] >= buildDay,
			ReviewFlag: all.halves > flagDay,
		})
	}
	return out
}

// onDate reports whether t falls on the date y-mo-dd of its location.
func onDate(t time.Time, y int, mo time.Month, dd int) bool {
	ty, tmo, tdd := t.Date()
	return ty == y && tmo == mo && tdd == dd
}

// dayStart returns the first instant of the local date y-mo-dd in loc.
// time.Date alone can miss it where a clock change skips or repeats local
// midnight. Where DST starts at midnight west of UTC, as in
// America/Santiago, America/Havana and Atlantic/Azores, the date has no
// 00:00, and time.Date gives 23:00 of the day before: the date starts where
// that hour's zone ends. Where a clock change repeats midnight, time.Date
// may give the second one, and the date starts at the first, in the zone
// before the change.
func dayStart(y int, mo time.Month, dd int, loc *time.Location) time.Time {
	t := time.Date(y, mo, dd, 0, 0, 0, 0, loc)
	if !onDate(t, y, mo, dd) {
		if _, end := t.ZoneBounds(); !end.IsZero() {
			t = end
		}
	}
	if start, _ := t.ZoneBounds(); !start.IsZero() {
		if before := start.Add(-time.Millisecond); onDate(before, y, mo, dd) {
			_, offset := before.Zone()
			t = time.Date(y, mo, dd, 0, 0, 0, 0, time.UTC).Add(-time.Duration(offset) * time.Second).In(loc)
		}
	}
	return t
}

// dayProjects returns the sums of every project that has credit, agent
// time or focus in the day's minutes.
func (r Rollups) dayProjects(d Derived, day []Minute, from, to int64) []DayProject {
	seen := map[string]bool{}
	for _, m := range day {
		for _, s := range m.Slots {
			for _, e := range s.Credited {
				seen[e.ProjectKey] = true
			}
			for p := range s.AgentOnly {
				seen[p] = true
			}
			if s.FocusProject != "" {
				seen[s.FocusProject] = true
			}
		}
	}
	delete(seen, "")
	var out []DayProject
	for _, p := range slices.Sorted(maps.Keys(seen)) {
		t := newTally()
		for _, m := range day {
			for _, s := range m.Slots {
				t.addSlot(s, p, r.backfilled(m.Start))
			}
		}
		t.addAnchors(d.Anchors, p, from, to)
		ro := t.rollup(r.Table, event.LevelProject, p, from, to)
		out = append(out, DayProject{ProjectKey: p, CreditedS: ro.CreditedS, CreditedByReason: ro.CreditedByReason,
			CreditedByKind: ro.CreditedByKind, CreditedByEvidence: ro.CreditedByEvidence, Uncredited: ro.Uncredited,
			AgentRunningS: ro.AgentRunningS, AgentOnlyS: float64(t.agentOnly), AnchorCounts: ro.AnchorCounts})
	}
	return out
}

// ProjectRollup is SM 5.3's project rollup: all of a project's credited
// time, with the time attached to each commit (SM 3.5).
type ProjectRollup struct {
	event.Rollup
	// AgentOnlyS is the project's agent-only time (SM 2.6).
	AgentOnlyS float64 `json:"agent_only_s"`
	// Commits are the project's commits, in order, each with the credit
	// since the commit before it.
	Commits []CommitTime `json:"commits,omitempty"`
	// UncommittedS is the credit after the project's last commit, the
	// uncommitted time (SM 3.5, SP 4.3), and UncommittedByKind splits it.
	UncommittedS      float64                    `json:"uncommitted_s"`
	UncommittedByKind map[event.Activity]float64 `json:"uncommitted_by_kind,omitempty"`
}

// CommitTime is the credit attached to one commit (SM 3.5).
type CommitTime struct {
	SHA string `json:"sha"`
	// T is when the commit's git.change record says it happened.
	T int64 `json:"t"`
	// CreditedS is the commit's credited_seconds_since_parent: the
	// project's credit since the commit before it, and CreditedByKind
	// splits it.
	CreditedS      float64                    `json:"credited_seconds_since_parent"`
	CreditedByKind map[event.Activity]float64 `json:"credited_by_kind,omitempty"`
}

// Projects returns a rollup for each project with credit in what d
// derived, by project key. records are the records d was derived from, or
// any that include the projects' git.change records.
//
// A project's commits are its git.change records of op commit, merge or
// revert, each a new commit after the one before it. An amend replaces the
// commit before it, so the amended commit takes that commit's time and
// what came after it. Each slot's credit goes to the first commit made
// after the slot's start, and credit after the last commit is
// uncommitted. Moving time across a rebase is the server's (SP 4.4): here
// a rebase moves nothing.
func (r Rollups) Projects(d Derived, records []event.Event) []ProjectRollup {
	ms := Minutes(d)
	commits := projectCommits(records)
	var out []ProjectRollup
	for _, p := range creditedProjects(ms) {
		t := newTally()
		from, to := int64(0), int64(0)
		cs := commits[p]
		byCommit := make([]map[event.Activity]int64, len(cs))
		var after map[event.Activity]int64
		for _, m := range ms {
			if m.creditedHalves()[p] == 0 && !m.touches(p) {
				continue
			}
			if from == 0 {
				from = m.Start
			}
			to = m.End()
			for _, s := range m.Slots {
				t.addSlot(s, p, r.backfilled(m.Start))
				k, _ := slices.BinarySearchFunc(cs, s.Slot, func(c CommitTime, slot int64) int {
					if c.T > slot {
						return 1
					}
					return -1
				})
				for _, e := range s.Credited {
					if e.ProjectKey != p || e.Halves == 0 {
						continue
					}
					if k < len(cs) {
						if byCommit[k] == nil {
							byCommit[k] = map[event.Activity]int64{}
						}
						byCommit[k][e.Reason.Activity()] += e.Halves
					} else {
						if after == nil {
							after = map[event.Activity]int64{}
						}
						after[e.Reason.Activity()] += e.Halves
					}
				}
			}
		}
		t.addAnchors(d.Anchors, p, from, to)
		pr := ProjectRollup{Rollup: t.rollup(r.Table, event.LevelProject, p, from, to), AgentOnlyS: float64(t.agentOnly),
			UncommittedByKind: halvesOf(after)}
		for _, n := range after {
			pr.UncommittedS += float64(n) / 2
		}
		for k, c := range cs {
			c.CreditedByKind = halvesOf(byCommit[k])
			for _, n := range byCommit[k] {
				c.CreditedS += float64(n) / 2
			}
			pr.Commits = append(pr.Commits, c)
		}
		out = append(out, pr)
	}
	return out
}

// touches reports whether a turn of project p ran in the minute or a slot
// of it was focused on p.
func (m Minute) touches(p string) bool {
	for _, s := range m.Slots {
		if s.AgentOnly[p] > 0 || s.FocusProject == p {
			return true
		}
	}
	return false
}

// projectCommits returns each project's commits from its git.change
// records, in record order.
func projectCommits(records []event.Event) map[string][]CommitTime {
	recs := slices.Clone(records)
	slices.SortStableFunc(recs, recordCmp)
	out := map[string][]CommitTime{}
	for _, e := range recs {
		g, ok := e.Data.(*event.GitChange)
		if !ok || e.ProjectKey == "" {
			continue
		}
		cs := out[e.ProjectKey]
		switch g.Op {
		case event.GitCommit, event.GitMerge, event.GitRevert:
			out[e.ProjectKey] = append(cs, CommitTime{SHA: g.SHA, T: e.T})
		case event.GitAmend:
			if n := len(cs); n > 0 {
				cs[n-1].SHA, cs[n-1].T = g.SHA, e.T
			} else {
				out[e.ProjectKey] = append(cs, CommitTime{SHA: g.SHA, T: e.T})
			}
		}
	}
	for p, cs := range out {
		slices.SortStableFunc(cs, func(a, b CommitTime) int { return cmp.Compare(a.T, b.T) })
		out[p] = cs
	}
	return out
}
