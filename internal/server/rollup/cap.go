// SPDX-License-Identifier: AGPL-3.0-or-later

package rollup

import (
	"context"
	"fmt"
	"math"
	"slices"
	"sync"
	"time"

	"github.com/some-du6e/pairtime/internal/server/writer"
	"github.com/some-du6e/pairtime/pkg/params"
)

// dayCapMS is DAY_CAP_S of the newest table pairtimed embeds, in
// milliseconds: the most credit one local day of an account keeps, across
// its devices (SM 5.3). Tests swap it for a smaller cap.
var dayCapMS = sync.OnceValue(func() int64 {
	tables := params.Tables()
	return tables[len(tables)-1].Int(params.DAY_CAP_S) * 1000
})

// capped applies the day cap (SM 5.3) to d, the change to day's rollups
// that the account's minute makes as it moves from prev to next, with all
// of its credit counted. While the day's credit stays within DAY_CAP_S
// before and after the move, d stands as it is.
//
// Otherwise the move re-cuts the day from the minute on. The day's credit
// before the minute is the stored rows' credit, the part they keep and the
// part they hold back, less the minute's and the later minutes'. Credit
// before the minute fits under the cap or doesn't as it did, since the cap
// counts in time order. The minute and the day's later minutes, which
// rollup_minutes still holds because the retention jobs delete a day's
// minutes oldest first, are cut again from there: prev's cut comes off and
// next's goes on, slot by slot. The agent's time on its own is never
// capped, so d's agent columns stand.
//
// That holds only for stored rows that are the day's cut under the cap, in
// time order. Rows stored before migration 0012, or cut under another
// DAY_CAP_S, aren't: they keep more than the cap, or hold some back while
// keeping less. Rows keep changed can't be either, though their totals
// may look like a cut, so the move checks that the re-cut takes no column
// below zero. Any of those days is re-cut whole instead (recut).
func capped(ctx context.Context, tx writer.Tx, account, day string, minuteMS int64, prev, next merged, d delta) (delta, error) {
	limit := dayCapMS()
	stored, err := dayRows(ctx, tx, account, day)
	if err != nil {
		return nil, err
	}
	kept, withheld := stored.totals()
	if withheld == 0 && kept+next.credit()-prev.credit() <= limit {
		// Rows keep changed can be under the cap with columns that aren't
		// the day's cut, so d must fit them too.
		if fits(stored, d) {
			return d, nil
		}
		return recut(ctx, tx, account, day, minuteMS, next, d, kept, limit, stored)
	}
	if !isCut(kept, withheld, limit) {
		return recut(ctx, tx, account, day, minuteMS, next, d, kept+withheld, limit, stored)
	}
	later, err := dayMinutes(ctx, tx, account, day, minuteMS)
	if err != nil {
		return nil, err
	}
	rest := prev.credit()
	ms := []merged{next}
	for _, m := range later {
		rest += m.merged.credit()
		ms = append(ms, m.merged)
	}
	start := max(kept+withheld-rest, 0)
	out := agentPart(d)
	out.add(cut(start, limit, day, ms))
	ms[0] = prev
	out.subtract(cut(start, limit, day, ms))
	if !fits(stored, out) {
		return recut(ctx, tx, account, day, minuteMS, next, d, kept+withheld, limit, stored)
	}
	return out, nil
}

// isCut reports whether a day's rows, which keep kept and hold back
// withheld, can be its cut under limit: at most limit kept, and all of it
// once anything is held back. Rows stored before migration 0012, or cut
// under another DAY_CAP_S, can fail this.
func isCut(kept, withheld, limit int64) bool {
	return kept <= limit && (withheld == 0 || kept == limit)
}

// The days RecutDays looks at, which the 180 days of minutes rollup_minutes
// keeps cover, with two days to spare for the widest local date, and how
// many days one of its ops cuts at most. recutRowsPerDay is the rows a cut
// day writes, as a typical day has them.
const (
	recutDays       = 182
	recutBatch      = 20
	recutRowsPerDay = 8
)

// RecutDays cuts again, once, every day of the last recutDays whose
// rollups_daily rows aren't a cut under the day cap (isCut), such as a day
// stored over 12 hours before migration 0012, while rollup_minutes still
// holds all of it (SM 5.3). The settle job runs it on its first run
// (RunSettle). It finds the days and checks their minutes on q, the read
// pool, and takes the writer only for days it can cut, so a run with
// nothing to cut makes no transaction. A day it can't cut, because the
// retention jobs have deleted some of its minutes, keeps its rows. It
// returns how many days it cut.
func RecutDays(ctx context.Context, d Doer, q Querier, now time.Time) (int, error) {
	return recutAll(ctx, d, q, now, nil)
}

// recutAll is RecutDays, and calls changed, when set, with each account
// whose day an op cut again, once that op's transaction has committed, so
// the live stream reads the account's rollups only after they moved
// (WP-SV8).
func recutAll(ctx context.Context, d Doer, q Querier, now time.Time, changed func(account string)) (int, error) {
	limit := dayCapMS()
	from := now.AddDate(0, 0, -recutDays).UTC().Format(time.DateOnly)
	rows, err := q.QueryContext(ctx, `SELECT user_id, day,
CAST(SUM(final_a_ms + final_b_ms + final_c_ms + pending_a_ms + pending_b_ms + pending_c_ms) AS BIGINT),
CAST(SUM(day_cap_ms) AS BIGINT)
FROM rollups_daily WHERE day >= $1 GROUP BY user_id, day`, from)
	if err != nil {
		return 0, fmt.Errorf("rollup: finding the days to cut again: %w", err)
	}
	type accountDay struct{ account, day string }
	var due []accountDay
	for rows.Next() {
		var ad accountDay
		var kept, withheld int64
		if err := rows.Scan(&ad.account, &ad.day, &kept, &withheld); err != nil {
			rows.Close()
			return 0, fmt.Errorf("rollup: finding the days to cut again: %w", err)
		}
		if !isCut(kept, withheld, limit) {
			due = append(due, ad)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("rollup: finding the days to cut again: %w", err)
	}
	var cuttable []accountDay
	for _, ad := range due {
		ok, err := recuttable(ctx, q, ad.account, ad.day)
		if err != nil {
			return 0, err
		}
		if ok {
			cuttable = append(cuttable, ad)
		}
	}
	n := 0
	for len(cuttable) > 0 {
		batch := cuttable[:min(len(cuttable), recutBatch)]
		cuttable = cuttable[len(batch):]
		var cutNow int
		var accounts []string
		err := d.Do(ctx, recutRowsPerDay*len(batch), func(ctx context.Context, tx writer.Tx) error {
			cutNow, accounts = 0, accounts[:0]
			for _, ad := range batch {
				did, err := recutDay(ctx, tx, ad.account, ad.day, limit)
				if err != nil {
					return err
				}
				if did {
					cutNow++
					accounts = append(accounts, ad.account)
				}
			}
			return nil
		})
		if err != nil {
			return n, fmt.Errorf("rollup: cutting days again: %w", err)
		}
		n += cutNow
		if changed != nil {
			for _, a := range accounts {
				changed(a)
			}
		}
	}
	return n, nil
}

// recuttable reports whether rollup_minutes still holds all of the
// account's day: whether its minutes' credit is the credit its rows hold.
func recuttable(ctx context.Context, q Querier, account, day string) (bool, error) {
	kept, withheld, err := dayTotals(ctx, q, account, day)
	if err != nil {
		return false, err
	}
	all, err := dayMinutes(ctx, q, account, day, math.MinInt64)
	if err != nil {
		return false, err
	}
	var sum int64
	for _, m := range all {
		sum += m.merged.credit()
	}
	return sum == kept+withheld, nil
}

// recutDay cuts the account's day again whole under limit, inside tx, if
// its rows still aren't a cut and rollup_minutes still holds all of it,
// and reports whether it did.
func recutDay(ctx context.Context, tx writer.Tx, account, day string, limit int64) (bool, error) {
	kept, withheld, err := dayTotals(ctx, tx, account, day)
	if err != nil || isCut(kept, withheld, limit) {
		return false, err
	}
	all, err := dayMinutes(ctx, tx, account, day, math.MinInt64)
	if err != nil {
		return false, err
	}
	var sum int64
	ms := make([]merged, 0, len(all))
	for _, m := range all {
		sum += m.merged.credit()
		ms = append(ms, m.merged)
	}
	if sum != kept+withheld {
		return false, nil
	}
	stored, err := dayRows(ctx, tx, account, day)
	if err != nil {
		return false, err
	}
	out := cut(0, limit, day, ms)
	out.subtract(stored)
	return true, addDaily(ctx, tx, account, out)
}

// totals returns the credit d's rows keep, at every level and state, and
// the credit they hold back past the day cap.
func (d delta) totals() (kept, withheld int64) {
	for _, c := range d {
		for col := 0; col < agentFinal; col++ {
			kept += c[col]
		}
		withheld += c[dayCap]
	}
	return kept, withheld
}

// fits reports whether adding d to stored leaves every credited column
// and every held-back total at zero or more, as it does when stored is the
// day's cut in time order.
func fits(stored, d delta) bool {
	for k, c := range d {
		s := stored[k]
		for col := 0; col < agentFinal; col++ {
			if s[col]+c[col] < 0 {
				return false
			}
		}
		if s[dayCap]+c[dayCap] < 0 {
			return false
		}
	}
	return true
}

// recut re-cuts the whole of day under limit, for a day whose stored rows
// aren't a cut of its minutes under it, with the minute that starts at
// minuteMS as next. total is the credit the rows hold, kept and held back.
// It reads every minute of the day, and re-cuts only when their credit is
// total, so none is missing: the day's new cut takes the place of its rows'
// credit. When the retention jobs have deleted some of the day's minutes,
// the day can't be cut again, and keep applies d to the rows as they are.
// stored is the day's rows, or nil for recut to read them.
func recut(ctx context.Context, tx writer.Tx, account, day string, minuteMS int64, next merged, d delta, total, limit int64, stored delta) (delta, error) {
	all, err := dayMinutes(ctx, tx, account, day, math.MinInt64)
	if err != nil {
		return nil, err
	}
	var sum int64
	ms := make([]merged, 0, len(all)+1)
	placed := false
	for _, m := range all {
		sum += m.merged.credit()
		switch {
		case m.ms == minuteMS:
			ms = append(ms, next)
			placed = true
			continue
		case !placed && m.ms > minuteMS:
			ms = append(ms, next)
			placed = true
		}
		ms = append(ms, m.merged)
	}
	if !placed {
		ms = append(ms, next)
	}
	if stored == nil {
		if stored, err = dayRows(ctx, tx, account, day); err != nil {
			return nil, err
		}
	}
	if sum != total {
		return keep(stored, d), nil
	}
	out := agentPart(d)
	out.add(cut(0, limit, day, ms))
	out.subtract(stored)
	return out, nil
}

// keep applies d to stored, a day's rows that aren't a cut under the cap
// and can't be cut again. It never lifts the cap and takes no row below
// zero: credit d adds is held back, and credit d takes away comes out of
// the row's held-back credit first, then out of the column d names, at most
// down to zero.
func keep(stored, d delta) delta {
	out := agentPart(d)
	for k, c := range d {
		s, o := stored[k], out[k]
		held := s[dayCap]
		// The credited columns come before the agent's.
		for col := 0; col < agentFinal; col++ {
			switch n := c[col]; {
			case n > 0:
				o[dayCap] += n
			case n < 0:
				w := min(held, -n)
				held -= w
				o[dayCap] -= w
				o[col] -= min(-n-w, s[col])
			}
		}
		out[k] = o
	}
	return out
}

// agentPart is d's agent columns alone: the change to the agent's time on
// its own, which the cap never touches.
func agentPart(d delta) delta {
	out := make(delta, len(d))
	for k, c := range d {
		out[k] = columns{agentFinal: c[agentFinal], agentPending: c[agentPending]}
	}
	return out
}

// cut splits the credit of ms, a day's minutes in time order, each slot in
// order, between the part that fits under limit and the part past it,
// X.day_cap, in rollups_daily's columns for day. start is the day's credit
// before ms. A slot the limit falls inside is split there.
func cut(start, limit int64, day string, ms []merged) delta {
	out := make(delta)
	run := start
	for _, m := range ms {
		for _, s := range m.slots {
			if s.MS == 0 {
				continue
			}
			fits := min(max(limit-run, 0), s.MS)
			run += s.MS
			col := slices.Index(Levels[:], s.Level)
			if s.pending {
				col += 3
			}
			k := dayKey{project: s.Project, day: day, kind: s.Kind}
			c := out[k]
			c[col] += fits
			c[dayCap] += s.MS - fits
			out[k] = c
		}
	}
	return out
}

// dayTotals returns the credit the account's rollups_daily rows of day
// keep, and the credit they hold back past the day cap.
func dayTotals(ctx context.Context, q Querier, account, day string) (kept, withheld int64, err error) {
	err = q.QueryRowContext(ctx, `SELECT
CAST(COALESCE(SUM(final_a_ms + final_b_ms + final_c_ms + pending_a_ms + pending_b_ms + pending_c_ms), 0) AS BIGINT),
CAST(COALESCE(SUM(day_cap_ms), 0) AS BIGINT)
FROM rollups_daily WHERE user_id = $1 AND day = $2`, account, day).Scan(&kept, &withheld)
	if err != nil {
		return 0, 0, fmt.Errorf("rollup: reading %s's %s: %w", account, day, err)
	}
	return kept, withheld, nil
}

// dayRows returns the credit the account's rollups_daily rows of day hold,
// kept and held back, as a delta, with no agent time.
func dayRows(ctx context.Context, q Querier, account, day string) (delta, error) {
	rows, err := q.QueryContext(ctx, `SELECT project_id, activity_kind,
final_a_ms, final_b_ms, final_c_ms, pending_a_ms, pending_b_ms, pending_c_ms, day_cap_ms
FROM rollups_daily WHERE user_id = $1 AND day = $2 AND activity_kind <> ''`, account, day)
	if err != nil {
		return nil, fmt.Errorf("rollup: reading %s's %s: %w", account, day, err)
	}
	defer rows.Close()
	out := make(delta)
	for rows.Next() {
		k := dayKey{day: day}
		var c columns
		if err := rows.Scan(&k.project, &k.kind, &c[0], &c[1], &c[2], &c[3], &c[4], &c[5], &c[dayCap]); err != nil {
			return nil, fmt.Errorf("rollup: reading %s's %s: %w", account, day, err)
		}
		out[k] = c
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rollup: reading %s's %s: %w", account, day, err)
	}
	return out, nil
}

// dayMinute is one minute of a day, its devices merged.
type dayMinute struct {
	ms     int64
	merged merged
}

// dayMinutes returns the account's minutes that count toward day and
// start after afterMS, each merged across its devices (SM 3.4), in time
// order. It reads rollup_minutes by its primary key over the widest span a
// local date has, as CreditReached does, and keeps the rows of day.
func dayMinutes(ctx context.Context, q Querier, account, day string, afterMS int64) ([]dayMinute, error) {
	date, err := time.Parse(time.DateOnly, day)
	if err != nil {
		return nil, fmt.Errorf("rollup: %q isn't a date: %w", day, err)
	}
	lo := max(afterMS, date.Add(-maxZoneOffset).UnixMilli()-1)
	hi := date.AddDate(0, 0, 1).Add(maxZoneOffset).UnixMilli()
	rows, err := q.QueryContext(ctx, `SELECT `+minuteColumns+` FROM rollup_minutes
WHERE user_id = $1 AND minute_ms > $2 AND minute_ms < $3 AND day = $4 ORDER BY minute_ms, device_id`, account, lo, hi, day)
	if err != nil {
		return nil, fmt.Errorf("rollup: reading %s's %s: %w", account, day, err)
	}
	defer rows.Close()
	var out []dayMinute
	var devs []device
	minute := int64(-1)
	flush := func() {
		if len(devs) > 0 {
			out = append(out, dayMinute{ms: minute, merged: merge(devs)})
		}
		devs = nil
	}
	for rows.Next() {
		d, m, err := scanDevice(rows, account)
		if err != nil {
			return nil, err
		}
		if m != minute {
			flush()
			minute = m
		}
		devs = append(devs, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rollup: reading %s's %s: %w", account, day, err)
	}
	flush()
	return out, nil
}
