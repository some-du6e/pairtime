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
	"time"

	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/pkg/event"
)

// Day is one local day's rollup (SM 5.3), the file days/YYYY-MM-DD.json
// (DESIGN 3.5) that `pairtime statusline` reads (WP-CO0). It stands in
// for WP-SC4's day rollup, and Rollup for the function that builds one.
//
// Every number is seconds. Credit sums the buckets' exact split by
// project, reason and level (Bucket.Projects), so the projects add up to
// the day and each project's levels to the project.
type Day struct {
	// Date is the day, YYYY-MM-DD, in the time zone Zone names: the
	// device's own, since SM 5.3's day is the person's local day. Start
	// and End are its bounds, in UTC.
	Date  string    `json:"date"`
	Zone  string    `json:"zone"`
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
	// CreditedS is the day's credited seconds, and PendingS the part of
	// them in minutes that are still pending, which late records can
	// change (DESIGN 3.7).
	CreditedS float64 `json:"credited_s"`
	PendingS  float64 `json:"pending_s"`
	// ByLevel splits CreditedS by evidence level, with every level present
	// (DESIGN 6.4).
	ByLevel map[event.Evidence]float64 `json:"by_level"`
	// Uncredited holds the seconds that were looked at and not credited,
	// by reason, X.day_cap included.
	Uncredited map[event.Reason]float64 `json:"uncredited,omitempty"`
	// AgentRunningS is the seconds an agent turn ran, in any project.
	AgentRunningS float64 `json:"agent_running_s"`
	// Projects has a row for each project with credit or agent time, in
	// project key order.
	Projects []DayProject `json:"projects"`
	// Minutes counts the minutes with a bucket, and PendingMinutes the
	// pending ones among them.
	Minutes        int `json:"minutes"`
	PendingMinutes int `json:"pending_minutes"`
}

// DayProject is one project's part of a day.
type DayProject struct {
	ProjectKey string  `json:"project_key"`
	CreditedS  float64 `json:"credited_s"`
	PendingS   float64 `json:"pending_s"`
	// ByLevel splits CreditedS by evidence level, with every level present.
	ByLevel  map[event.Evidence]float64 `json:"by_level"`
	ByReason map[event.Reason]float64   `json:"by_reason,omitempty"`
	ByKind   map[event.Activity]float64 `json:"by_kind,omitempty"`
	// AgentS is the seconds a turn of the project ran, and AgentOnlyS the
	// part of them the project wasn't credited (SM 2.6).
	AgentS     int64 `json:"agent_s"`
	AgentOnlyS int64 `json:"agent_only_s"`
}

// Levels are DESIGN 6.4's evidence levels, strongest first.
var Levels = []event.Evidence{event.EvidenceA, event.EvidenceB, event.EvidenceC}

func byLevel() map[event.Evidence]float64 {
	m := make(map[event.Evidence]float64, len(Levels))
	for _, l := range Levels {
		m[l] = 0
	}
	return m
}

// DayBounds returns the start and end of the day date, YYYY-MM-DD, in loc.
// A day is 23 or 25 hours long where the clocks change.
func DayBounds(date string, loc *time.Location) (start, end time.Time, err error) {
	t, err := time.ParseInLocation(time.DateOnly, date, loc)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("store: %q isn't a date: %w", date, err)
	}
	return t, t.AddDate(0, 0, 1), nil
}

// projectSum is a project's running totals in half-seconds.
type projectSum struct {
	halves, pending  int64
	level            map[event.Evidence]int64
	reason           map[event.Reason]int64
	agent, agentOnly int64
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
	day := Day{Date: date, Zone: loc.String(), Start: start.UTC(), End: end.UTC(), ByLevel: byLevel(), Projects: []DayProject{}}
	sums := map[string]*projectSum{}
	uncredited := map[event.Reason]float64{}
	var total, capped int64
	capHalves := 2 * max(dayCapS, 0)
	from, to := start.UnixMilli(), end.UnixMilli()
	ordered := slices.Clone(buckets)
	slices.SortFunc(ordered, func(a, b Bucket) int { return cmp.Compare(a.At(), b.At()) })
	for _, b := range ordered {
		if b.At() < from || b.At() >= to {
			continue
		}
		day.Minutes++
		pending := b.State == event.StatePending
		if pending {
			day.PendingMinutes++
		}
		day.AgentRunningS += b.AgentRunningS
		for r, s := range b.Uncredited {
			uncredited[r] += s
		}
		for _, key := range slices.Sorted(maps.Keys(b.Projects)) {
			pm := b.Projects[key]
			ps := sums[key]
			if ps == nil {
				ps = &projectSum{level: map[event.Evidence]int64{}, reason: map[event.Reason]int64{}}
				sums[key] = ps
			}
			ps.agent += pm.AgentS
			ps.agentOnly += pm.AgentOnlyS
			for _, c := range pm.Credited {
				h := max(c.Halves, 0)
				if over := total + h - capHalves; over > 0 {
					cut := min(over, h)
					h -= cut
					capped += cut
				}
				total += h
				if h == 0 {
					continue
				}
				ps.halves += h
				if pending {
					ps.pending += h
				}
				ps.level[c.Ev] += h
				ps.reason[c.Why] += h
			}
		}
	}
	if capped > 0 {
		uncredited[event.ReasonDayCap] += float64(capped) / 2
	}
	if len(uncredited) > 0 {
		day.Uncredited = uncredited
	}
	var dayHalves, dayPending int64
	dayLevel := map[event.Evidence]int64{}
	for _, key := range slices.Sorted(maps.Keys(sums)) {
		ps := sums[key]
		if ps.halves == 0 && ps.agent == 0 {
			continue
		}
		row := DayProject{ProjectKey: key, CreditedS: halfSeconds(ps.halves), PendingS: halfSeconds(ps.pending),
			ByLevel: byLevel(), AgentS: ps.agent, AgentOnlyS: ps.agentOnly}
		for l, h := range ps.level {
			row.ByLevel[l] = halfSeconds(h)
			dayLevel[l] += h
		}
		if len(ps.reason) > 0 {
			row.ByReason = map[event.Reason]float64{}
			row.ByKind = map[event.Activity]float64{}
			kinds := map[event.Activity]int64{}
			for r, h := range ps.reason {
				row.ByReason[r] = halfSeconds(h)
				kinds[r.Activity()] += h
			}
			for k, h := range kinds {
				row.ByKind[k] = halfSeconds(h)
			}
		}
		dayHalves += ps.halves
		dayPending += ps.pending
		day.Projects = append(day.Projects, row)
	}
	day.CreditedS, day.PendingS = halfSeconds(dayHalves), halfSeconds(dayPending)
	for l, h := range dayLevel {
		day.ByLevel[l] = halfSeconds(h)
	}
	return day, nil
}

func halfSeconds(h int64) float64 { return float64(h) / 2 }

// DayFile returns the path of date's rollup in the days directory dir.
func DayFile(dir, date string) string { return filepath.Join(dir, date+".json") }

// WriteDay writes d to the days directory dir, replacing the day's file by
// atomic rename, so a reader finds the old rollup or the new one.
func WriteDay(dir string, d Day) error {
	if _, err := time.Parse(time.DateOnly, d.Date); err != nil {
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
