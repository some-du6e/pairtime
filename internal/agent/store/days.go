// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/pkg/event"
)

// Tally is what the store sums for one project over a span, a local day
// or a work session (SM 5.3), from the latest revision of each minute's
// bucket. It is the shape the local API, `pairtime today`, `recap` and
// `statusline` and the dashboard all read (WP-CO0, WP-UI1), and it stands
// in for WP-SC4's rollups. Every number is seconds, in steps of 0.5.
type Tally struct {
	// CreditedS is the credited seconds: your time. PendingS is the part
	// of it in minutes that are still pending, which later records can
	// change (DESIGN 3.7).
	CreditedS float64 `json:"credited_s"`
	PendingS  float64 `json:"pending_s"`
	// CreditedByKind and CreditedByEvidence each split CreditedS, by
	// activity kind (SM 5.1) and by evidence level (DESIGN 6.4). Only kinds
	// and levels with credit are in them.
	CreditedByKind     map[event.Activity]float64 `json:"credited_by_kind"`
	CreditedByEvidence map[event.Evidence]float64 `json:"credited_by_evidence"`
	// AgentOnlyS is SM 5.3's agent_only_s: the seconds a turn in the
	// project ran that the project wasn't credited, whatever their reason
	// code. They aren't the X.agent_only seconds: in SM 6.4 all 23,180 of
	// them are X.absent.
	AgentOnlyS float64 `json:"agent_only_s"`
	// AgentOnlyByHarness splits AgentOnlyS by the harness whose turn ran,
	// and names every harness that had a session in the project in the
	// span, with 0 for one that had no time on its own.
	AgentOnlyByHarness map[event.Harness]float64 `json:"agent_only_by_harness"`
	// Uncredited is the seconds looked at and not credited, by reason
	// (SM 5.2): agent-only seconds under their own codes, and X.day_cap for
	// credit past DAY_CAP_S.
	Uncredited map[event.Reason]float64 `json:"uncredited"`
}

// DayProject is one project's part of a day.
type DayProject struct {
	// Project is the project's key, and Name its name from the local
	// project map, which never leaves the device; "" until there is one.
	Project string `json:"project"`
	Name    string `json:"name"`
	Tally
}

// Day is one local day's rollup (SM 5.3): the file days/YYYY-MM-DD.json
// (DESIGN 3.5), which the local API serves for GET /v1/days/{date} and
// `pairtime statusline` reads through it.
type Day struct {
	// Date is the day, YYYY-MM-DD, in the time zone Zone names: the
	// device's own, since SM 5.3's day is the person's local day. Start
	// and End are its bounds, in UTC.
	Date  string    `json:"date"`
	Zone  string    `json:"zone"`
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
	// Projects has a row for each project with credit, agent time or
	// uncredited time in the day, in project key order.
	Projects []DayProject `json:"projects"`
}

// Session is a work session (SM 5.3): a run of one project's credited
// minutes, split where one ends more than SESSION_GAP_S before the next
// starts. Start is the start of its first credited minute and End the end
// of its last, in UTC, and the tally sums the project's part of every
// minute from Start to End.
type Session struct {
	Project string    `json:"project"`
	Name    string    `json:"name"`
	Start   time.Time `json:"start"`
	End     time.Time `json:"end"`
	Tally
}

// DayBounds returns the start and end of the day date, YYYY-MM-DD, in loc.
// A day is 23 or 25 hours long where the clocks change.
func DayBounds(date string, loc *time.Location) (start, end time.Time, err error) {
	t, err := time.ParseInLocation(time.DateOnly, date, loc)
	if err != nil || t.Format(time.DateOnly) != date {
		return time.Time{}, time.Time{}, fmt.Errorf("store: %q isn't a date", date)
	}
	return t, t.AddDate(0, 0, 1), nil
}

// tallySum is a Tally's running sums, in half-seconds and seconds.
type tallySum struct {
	halves, pending int64
	kind            map[event.Activity]int64
	level           map[event.Evidence]int64
	agentOnly       int64
	harness         map[event.Harness]int64
	uncredited      map[event.Reason]int64 // seconds
	capped          int64                  // half-seconds past the day cap
}

func newSum() *tallySum {
	return &tallySum{kind: map[event.Activity]int64{}, level: map[event.Evidence]int64{},
		harness: map[event.Harness]int64{}, uncredited: map[event.Reason]int64{}}
}

// dayCap counts a day's credit against DAY_CAP_S, in half-seconds.
type dayCap struct{ used, max int64 }

// take returns how many of h half-seconds of credit fit under the cap, and
// counts them. A nil cap takes everything.
func (c *dayCap) take(h int64) int64 {
	if c == nil {
		return h
	}
	fit := min(h, max(c.max-c.used, 0))
	c.used += fit
	return fit
}

// add sums pm, a project's part of a minute, into s.
func (s *tallySum) add(pm ProjectMinute, pending bool, c *dayCap) {
	for _, cr := range pm.Credited {
		h := max(cr.Halves, 0)
		kept := c.take(h)
		s.capped += h - kept
		if kept == 0 {
			continue
		}
		s.halves += kept
		if pending {
			s.pending += kept
		}
		s.kind[cr.Why.Activity()] += kept
		s.level[cr.Ev] += kept
	}
	s.agentOnly += pm.AgentOnlyS
	for h, n := range pm.Harnesses {
		s.harness[h] += n
	}
	for r, n := range pm.Uncredited {
		s.uncredited[r] += n
	}
}

func halfSeconds(h int64) float64 { return float64(h) / 2 }

// tally turns s into a Tally.
func (s *tallySum) tally() Tally {
	t := Tally{
		CreditedS: halfSeconds(s.halves), PendingS: halfSeconds(s.pending),
		CreditedByKind: map[event.Activity]float64{}, CreditedByEvidence: map[event.Evidence]float64{},
		AgentOnlyS:         float64(s.agentOnly),
		AgentOnlyByHarness: map[event.Harness]float64{}, Uncredited: map[event.Reason]float64{},
	}
	for k, h := range s.kind {
		if h > 0 {
			t.CreditedByKind[k] = halfSeconds(h)
		}
	}
	for l, h := range s.level {
		if h > 0 {
			t.CreditedByEvidence[l] = halfSeconds(h)
		}
	}
	for h, n := range s.harness {
		t.AgentOnlyByHarness[h] = float64(n)
	}
	for r, n := range s.uncredited {
		t.Uncredited[r] = float64(n)
	}
	if s.capped > 0 {
		t.Uncredited[event.ReasonDayCap] += halfSeconds(s.capped)
	}
	return t
}

// Rollup sums the buckets of the day date in loc into its rollup. buckets
// are the latest revisions of the day's minutes, as Buckets.Range returns
// them; any outside the day are left out. Once the day has dayCapS
// credited seconds, the credit of every later minute is X.day_cap instead
// (SM 5.3), and within the minute that reaches the cap, projects take
// their credit in key order. This is SM 5.3's day level as the device sees
// it, for this device alone.
func Rollup(date string, loc *time.Location, buckets []Bucket, dayCapS int64) (Day, error) {
	start, end, err := DayBounds(date, loc)
	if err != nil {
		return Day{}, err
	}
	day := Day{Date: date, Zone: loc.String(), Start: start.UTC(), End: end.UTC(), Projects: []DayProject{}}
	sums := map[string]*tallySum{}
	c := &dayCap{max: 2 * max(dayCapS, 0)}
	for _, b := range sorted(buckets) {
		if b.At() < start.UnixMilli() || b.At() >= end.UnixMilli() {
			continue
		}
		for _, key := range slices.Sorted(maps.Keys(b.Projects)) {
			if sums[key] == nil {
				sums[key] = newSum()
			}
			sums[key].add(b.Projects[key], b.State == event.StatePending, c)
		}
	}
	for _, key := range slices.Sorted(maps.Keys(sums)) {
		day.Projects = append(day.Projects, DayProject{Project: key, Tally: sums[key].tally()})
	}
	return day, nil
}

// sorted returns buckets in minute order, leaving buckets as it was.
func sorted(buckets []Bucket) []Bucket {
	out := slices.Clone(buckets)
	slices.SortFunc(out, func(a, b Bucket) int { return cmp.Compare(a.At(), b.At()) })
	return out
}

// creditedProject returns the project with the most credit in b, the
// first in key order on a tie, or "" when nothing in b was credited.
func creditedProject(b Bucket) string {
	best, most := "", int64(0)
	for _, key := range slices.Sorted(maps.Keys(b.Projects)) {
		var h int64
		for _, c := range b.Projects[key].Credited {
			h += c.Halves
		}
		if h > most {
			best, most = key, h
		}
	}
	return best
}

// credits reports whether b credits project key anything.
func credits(b Bucket, key string) bool {
	for _, c := range b.Projects[key].Credited {
		if c.Halves > 0 {
			return true
		}
	}
	return false
}

// sessionLookback bounds how far back LastSession looks for a credited
// minute, so a store with none doesn't have every file read at each ask.
const sessionLookback = 30 * dayMS

// LastSession returns the most recent work session in s whose last
// credited minute starts before to, in Unix ms, or nil when there is none
// in the 30 days before to: the one whose last credited minute is latest,
// in the project with most of that minute's credit. gapMS is
// SESSION_GAP_S in milliseconds.
func LastSession(s *Buckets, to, gapMS int64) (*Session, error) {
	var (
		project    string
		start, end int64
		in         []Bucket
	)
	err := s.Before(to, func(b Bucket) bool {
		m := b.At()
		if project == "" {
			if m < to-sessionLookback {
				return false
			}
			if project = creditedProject(b); project == "" {
				return true
			}
			start, end = m, m+minuteMS
			in = append(in, b)
			return true
		}
		if m+minuteMS < start-gapMS {
			return false // no earlier minute is close enough to join
		}
		in = append(in, b)
		if credits(b, project) {
			start = m
		}
		return true
	})
	if err != nil || project == "" {
		return nil, err
	}
	sum := newSum()
	for _, b := range sorted(in) {
		if pm, ok := b.Projects[project]; ok && b.At() >= start && b.At() < end {
			sum.add(pm, b.State == event.StatePending, nil)
		}
	}
	return &Session{Project: project, Start: time.UnixMilli(start).UTC(), End: time.UnixMilli(end).UTC(), Tally: sum.tally()}, nil
}

// DayFile returns the path of date's rollup in the days directory dir.
func DayFile(dir, date string) string { return filepath.Join(dir, date+".json") }

// WriteDay writes d to the days directory dir, replacing the day's file by
// atomic rename, so a reader finds the old rollup or the new one.
func WriteDay(dir string, d Day) error {
	if _, err := time.Parse(time.DateOnly, d.Date); err != nil || strings.ContainsAny(d.Date, `/\`) {
		return fmt.Errorf("store: %q isn't a date", d.Date)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(d)
	if err != nil {
		return err
	}
	return home.WriteFile(DayFile(dir, d.Date), append(data, '\n'))
}

// ReadDay reads date's rollup from the days directory dir.
func ReadDay(dir, date string) (Day, error) {
	if _, err := time.Parse(time.DateOnly, date); err != nil {
		return Day{}, fmt.Errorf("store: %q isn't a date", date)
	}
	data, err := os.ReadFile(DayFile(dir, date))
	if err != nil {
		return Day{}, err
	}
	var d Day
	if err := json.Unmarshal(data, &d); err != nil {
		return Day{}, fmt.Errorf("store: %s: %w", DayFile(dir, date), err)
	}
	return d, nil
}
