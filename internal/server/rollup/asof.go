// SPDX-License-Identifier: AGPL-3.0-or-later

package rollup

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/some-du6e/pairtime/pkg/event"
)

// Credit is a day's credited time at levels A and B, in milliseconds.
type Credit struct {
	AMS, BMS int64
}

// Total is the day's credited time at levels A and B together.
func (c Credit) Total() int64 { return c.AMS + c.BMS }

// add returns c plus o.
func (c Credit) add(o Credit) Credit { return Credit{AMS: c.AMS + o.AMS, BMS: c.BMS + o.BMS} }

// sub returns c minus o.
func (c Credit) sub(o Credit) Credit { return Credit{AMS: c.AMS - o.AMS, BMS: c.BMS - o.BMS} }

// maxZoneOffset is the furthest any time zone's clock is from UTC, so a
// local date never starts more than this before its UTC midnight or ends
// more than this after the next one (UTC+14 is the furthest east, UTC-12 the
// furthest west).
const maxZoneOffset = 14 * time.Hour

// CreditAsOf returns the account's credit on day, a local date, as the
// server held it at asOfMS (Unix ms): the as-of read build days are decided
// from (D-sv12-lag; DESIGN 6.2 D10). For every minute rollup_minutes counts
// toward the day, it takes each device's highest-rev revision in
// minute_bucket_revisions received at or before asOfMS that the server
// itself labeled witnessed or offline, or, for a revision stored with no
// label by a release from before migration 0009, whose bucket the client
// read so. So a revision the server labeled backfilled adds and removes
// nothing, and a late restore keeps the minute's earlier credit. It merges
// each minute's revisions as Apply does (SM 3.4) and sums, over every
// project and kind, the level A and B milliseconds of merged slots whose
// revision isn't pending or whose minute ended at least PendingWindow
// before asOfMS. Level C never counts, and no cap applies. It writes
// nothing: the slots keep the devices' project keys, which the sums don't
// need.
func CreditAsOf(ctx context.Context, q Querier, account, day string, asOfMS int64) (Credit, error) {
	c, _, _, err := CreditReached(ctx, q, account, day, asOfMS, math.MaxInt64)
	return c, err
}

// CreditReached reports whether the account's credit on day, as CreditAsOf
// reads it, reached needMS at any time up to untilMS: at the receipt of one
// of the day's revisions, or at the moment a pending revision's minute
// became PendingWindow old, the only times it can change, which
// minute_bucket_revisions keeps. It returns the credit at the first such
// time and that time, or, when the credit never reached needMS, the credit
// as of untilMS. So a day the build-day job didn't store when it first
// reached BUILD_DAY_S still counts when the job decides it late, though a
// later revision took credit away before the deadline (#278).
//
// It reads rollup_minutes over the widest span a local date has in any
// zone, by its primary key, and keeps the rows of day, so it finds every
// minute whatever zone the account had when each first counted.
func CreditReached(ctx context.Context, q Querier, account, day string, untilMS, needMS int64) (Credit, int64, bool, error) {
	date, err := time.Parse(time.DateOnly, day)
	if err != nil {
		return Credit{}, 0, false, fmt.Errorf("rollup: %q isn't a date: %w", day, err)
	}
	lo := date.Add(-maxZoneOffset).UnixMilli()
	hi := date.AddDate(0, 0, 1).Add(maxZoneOffset).UnixMilli()
	rows, err := q.QueryContext(ctx, `SELECT r.minute_ms, r.device_id, r.received_at_ms, r.bucket, r.server_witness
FROM rollup_minutes m
JOIN minute_bucket_revisions r ON r.device_id = m.device_id AND r.minute_ms = m.minute_ms
WHERE m.user_id = $1 AND m.minute_ms >= $2 AND m.minute_ms < $3 AND m.day = $4
AND r.account_id = $1 AND r.received_at_ms <= $5`, account, lo, hi, day, untilMS)
	if err != nil {
		return Credit{}, 0, false, fmt.Errorf("rollup: reading %s's %s as of %d: %w", account, day, untilMS, err)
	}
	defer rows.Close()
	pendingMS := PendingWindow().Milliseconds()

	// An event is a revision's receipt, or, with rev nil, the moment a
	// minute's pending revisions start to count.
	type ev struct {
		at     int64
		minute int64
		rev    *device
	}
	var evs []ev
	pendingMinutes := make(map[int64]bool)
	for rows.Next() {
		var m, at int64
		var d, bucket string
		var label sql.NullString
		if err := rows.Scan(&m, &d, &at, &bucket, &label); err != nil {
			return Credit{}, 0, false, fmt.Errorf("rollup: reading %s's %s as of %d: %w", account, day, untilMS, err)
		}
		var mb event.MinuteBucket
		if err := json.Unmarshal([]byte(bucket), &mb); err != nil {
			return Credit{}, 0, false, fmt.Errorf("rollup: minute %d of %s on %s has a revision that isn't a bucket: %w", m, account, d, err)
		}
		w := event.Witness(label.String)
		if w == "" {
			w = mb.Witness
		}
		if w != event.Witnessed && w != event.Offline {
			continue
		}
		in, err := minuteOf(FromEvent(account, mb))
		if err != nil {
			return Credit{}, 0, false, err
		}
		evs = append(evs, ev{at: at, minute: m, rev: &in})
		if in.pending {
			pendingMinutes[m] = true
		}
	}
	if err := rows.Err(); err != nil {
		return Credit{}, 0, false, fmt.Errorf("rollup: reading %s's %s as of %d: %w", account, day, untilMS, err)
	}
	for m := range pendingMinutes {
		if at := m + 60_000 + pendingMS; at <= untilMS {
			evs = append(evs, ev{at: at, minute: m})
		}
	}
	slices.SortStableFunc(evs, func(a, b ev) int { return cmp.Compare(a.at, b.at) })

	// Each minute's latest revision on each device so far, whether its
	// pending revisions count yet, and what it adds to the day.
	type state struct {
		devs    map[string]device
		settled bool
		credit  Credit
	}
	minutes := make(map[int64]*state)
	var total Credit
	for i := 0; i < len(evs); {
		at := evs[i].at
		for ; i < len(evs) && evs[i].at == at; i++ {
			e := evs[i]
			st := minutes[e.minute]
			if st == nil {
				st = &state{devs: make(map[string]device)}
				minutes[e.minute] = st
			}
			if e.rev == nil {
				st.settled = true
			} else if held, ok := st.devs[e.rev.id]; !ok || e.rev.rev > held.rev {
				st.devs[e.rev.id] = *e.rev
			}
			devs := make([]device, 0, len(st.devs))
			for _, d := range st.devs {
				devs = append(devs, d)
			}
			c := countOf(merge(devs), st.settled)
			total = total.sub(st.credit).add(c)
			st.credit = c
		}
		if total.Total() >= needMS {
			return total, at, true, nil
		}
	}
	return total, untilMS, false, nil
}

// countOf is what the merged minute m adds to its day's credit: the level
// A and B milliseconds of its slots, a pending revision's only once settled.
func countOf(m merged, settled bool) Credit {
	var c Credit
	for _, s := range m.slots {
		if s.MS == 0 || (s.pending && !settled) {
			continue
		}
		switch s.Level {
		case "A":
			c.AMS += s.MS
		case "B":
			c.BMS += s.MS
		}
	}
	return c
}

// Deadline returns when the account's day, a local date, is decided: its
// end plus lag, in Unix ms (D-sv12-lag; DESIGN 6.2 D10). The end is the
// first instant of the next date in the zone the day is counted in, so a
// 23- or 25-hour day ends where it should. A day that had begun in the zone
// a change replaced, before the change, and whose deadline in that zone
// hadn't passed when the change came, ends at the later of its ends in the
// two zones. A day whose deadline had passed by then keeps its end in the
// zone before, and a day that began in the zone before at or after the
// change takes the new zone's. Changes come at least ZoneChangeEvery apart,
// so at most one change falls inside a day and its deadline.
func (h History) Deadline(day string, lag time.Duration) (int64, error) {
	date, err := time.Parse(time.DateOnly, day)
	if err != nil {
		return 0, fmt.Errorf("rollup: %q isn't a date: %w", day, err)
	}
	y, mo, d := date.Date()
	ny, nmo, nd := date.AddDate(0, 0, 1).Date()
	lagMS := lag.Milliseconds()
	endIn := func(loc *time.Location) int64 { return dayStart(ny, nmo, nd, loc).UnixMilli() }
	// From the latest change back: the first the day began after is the one
	// that set its zone, unless the day straddles it.
	for i := len(h.Changes) - 1; i >= 0; i-- {
		c := h.Changes[i]
		if dayStart(y, mo, d, c.Before).UnixMilli() >= c.AtMS {
			return endIn(h.after(i)) + lagMS, nil
		}
		if before := endIn(c.Before); before+lagMS > c.AtMS {
			return max(before, endIn(h.after(i))) + lagMS, nil
		}
	}
	return endIn(h.At(math.MinInt64)) + lagMS, nil
}

// Today returns the local date in the account's zone at the instant ms.
func (h History) Today(ms int64) string {
	return time.UnixMilli(ms).In(h.Zone).Format(time.DateOnly)
}

// onDate reports whether t falls on the date y-mo-dd of its location.
func onDate(t time.Time, y int, mo time.Month, dd int) bool {
	ty, tmo, tdd := t.Date()
	return ty == y && tmo == mo && tdd == dd
}

// dayStart returns the first instant of the local date y-mo-dd in loc, as
// pkg/score's dayStart does. time.Date alone can miss it where a clock
// change skips or repeats local midnight. Where DST starts at midnight west
// of UTC, as in America/Santiago, America/Havana and Atlantic/Azores, the
// date has no 00:00, and time.Date gives 23:00 of the day before: the date
// starts where that hour's zone ends. Where a clock change repeats
// midnight, time.Date may give the second one, and the date starts at the
// first, in the zone before the change.
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
