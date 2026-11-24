// SPDX-License-Identifier: AGPL-3.0-or-later

package rollup

import (
	"context"
	"fmt"
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
func capped(ctx context.Context, tx writer.Tx, account, day string, minuteMS int64, prev, next merged, d delta) (delta, error) {
	limit := dayCapMS()
	kept, withheld, err := dayTotals(ctx, tx, account, day)
	if err != nil {
		return nil, err
	}
	if withheld == 0 && kept+next.credit()-prev.credit() <= limit {
		return d, nil
	}
	later, err := laterMinutes(ctx, tx, account, day, minuteMS)
	if err != nil {
		return nil, err
	}
	rest := prev.credit()
	for _, m := range later {
		rest += m.credit()
	}
	start := max(kept+withheld-rest, 0)
	out := make(delta, len(d))
	for k, c := range d {
		out[k] = columns{agentFinal: c[agentFinal], agentPending: c[agentPending]}
	}
	out.add(cut(start, limit, day, append([]merged{next}, later...)))
	out.subtract(cut(start, limit, day, append([]merged{prev}, later...)))
	return out, nil
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

// laterMinutes returns the account's minutes that count toward day and
// start after minuteMS, each merged across its devices (SM 3.4), in time
// order. It reads rollup_minutes by its primary key over the widest span a
// local date has, as CreditReached does, and keeps the rows of day.
func laterMinutes(ctx context.Context, q Querier, account, day string, minuteMS int64) ([]merged, error) {
	date, err := time.Parse(time.DateOnly, day)
	if err != nil {
		return nil, fmt.Errorf("rollup: %q isn't a date: %w", day, err)
	}
	hi := date.AddDate(0, 0, 1).Add(maxZoneOffset).UnixMilli()
	rows, err := q.QueryContext(ctx, `SELECT `+minuteColumns+` FROM rollup_minutes
WHERE user_id = $1 AND minute_ms > $2 AND minute_ms < $3 AND day = $4 ORDER BY minute_ms, device_id`, account, minuteMS, hi, day)
	if err != nil {
		return nil, fmt.Errorf("rollup: reading %s's %s: %w", account, day, err)
	}
	defer rows.Close()
	var out []merged
	var devs []device
	minute := int64(-1)
	flush := func() {
		if len(devs) > 0 {
			out = append(out, merge(devs))
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
