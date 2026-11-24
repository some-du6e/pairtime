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
// That holds only for stored rows that are a cut of the day under the cap:
// at most the cap kept, and all of it once anything is held back. Rows
// stored before migration 0012, or cut under another DAY_CAP_S, aren't, so
// the move re-cuts such a day whole instead (recut).
func capped(ctx context.Context, tx writer.Tx, account, day string, minuteMS int64, prev, next merged, d delta) (delta, error) {
	limit := dayCapMS()
	kept, withheld, err := dayTotals(ctx, tx, account, day)
	if err != nil {
		return nil, err
	}
	if withheld == 0 && kept+next.credit()-prev.credit() <= limit {
		return d, nil
	}
	if kept > limit || (withheld > 0 && kept != limit) {
		return recut(ctx, tx, account, day, minuteMS, next, d, kept+withheld, limit)
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
	return out, nil
}

// recut re-cuts the whole of day under limit, for a day whose stored rows
// aren't a cut of its minutes under it, with the minute that starts at
// minuteMS as next. total is the credit the rows hold, kept and held back.
// It reads every minute of the day, and re-cuts only when their credit is
// total, so none is missing: the day's new cut takes the place of its rows'
// credit. When the retention jobs have deleted some of the day's minutes,
// d stands, as it did before the cap.
func recut(ctx context.Context, tx writer.Tx, account, day string, minuteMS int64, next merged, d delta, total, limit int64) (delta, error) {
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
	if sum != total {
		return d, nil
	}
	stored, err := dayRows(ctx, tx, account, day)
	if err != nil {
		return nil, err
	}
	out := agentPart(d)
	out.add(cut(0, limit, day, ms))
	out.subtract(stored)
	return out, nil
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
