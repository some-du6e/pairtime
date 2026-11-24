// SPDX-License-Identifier: AGPL-3.0-or-later

package buildday

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"
)

// Exported is every row this package keeps for an account: its stored
// days, its decided weeks, its weekly goal and freezes, and its time zone
// changes. The privacy hub's export only counts these tables, since the
// hub's contract answers counts and never a person's data (WP-SV13), so
// nothing reads Exported yet. It is kept for a later download the teen
// asks for themselves.
type Exported struct {
	Days        []StoredDay  `json:"build_days"`
	Weeks       []StoredWeek `json:"build_weeks"`
	Goal        *Goal        `json:"build_goal,omitempty"`
	ZoneChanges []ZoneChange `json:"zone_changes"`
}

// StoredDay is a build_days row.
type StoredDay struct {
	Date      string    `json:"date"`
	AMS       int64     `json:"a_ms"`
	BMS       int64     `json:"b_ms"`
	Counted   bool      `json:"counted"`
	DecidedAt time.Time `json:"decided_at"`
}

// StoredWeek is a build_weeks row.
type StoredWeek struct {
	Start     string    `json:"start"`
	Goal      int64     `json:"goal"`
	BuildDays int64     `json:"build_days"`
	Result    string    `json:"result"`
	DecidedAt time.Time `json:"decided_at"`
}

// Goal is a build_goals row.
type Goal struct {
	WeeklyGoal    int64  `json:"weekly_goal"`
	FreezeDays    int64  `json:"build_days_toward_next_freeze"`
	FreezesHeld   int64  `json:"freezes_held"`
	FreezesEarned int64  `json:"freezes_earned"`
	NextWeek      string `json:"next_week,omitempty"`
}

// ZoneChange is a zone_changes row: when the profile's zone changed, and
// the zone it replaced.
type ZoneChange struct {
	At     time.Time `json:"at"`
	Before string    `json:"before"`
}

// Export reads every row this package keeps for account, and writes
// nothing: the rows a later download by the teen would hold, not the
// privacy hub's export, which counts them. DeleteAccount deletes the same
// rows.
func Export(ctx context.Context, q Querier, account string) (Exported, error) {
	var out Exported
	err := each(ctx, q, `SELECT day, a_ms, b_ms, counted, decided_at_ms FROM build_days WHERE user_id = $1`, account, func(rows *sql.Rows) error {
		var d StoredDay
		var counted, at int64
		if err := rows.Scan(&d.Date, &d.AMS, &d.BMS, &counted, &at); err != nil {
			return err
		}
		d.Counted, d.DecidedAt = counted == 1, time.UnixMilli(at).UTC()
		out.Days = append(out.Days, d)
		return nil
	})
	if err == nil {
		err = each(ctx, q, `SELECT week, goal, build_days, result, decided_at_ms FROM build_weeks WHERE user_id = $1`, account, func(rows *sql.Rows) error {
			var w StoredWeek
			var at int64
			if err := rows.Scan(&w.Start, &w.Goal, &w.BuildDays, &w.Result, &at); err != nil {
				return err
			}
			w.DecidedAt = time.UnixMilli(at).UTC()
			out.Weeks = append(out.Weeks, w)
			return nil
		})
	}
	if err == nil {
		err = each(ctx, q, `SELECT changed_at_ms, tz_before FROM zone_changes WHERE user_id = $1`, account, func(rows *sql.Rows) error {
			var c ZoneChange
			var at int64
			if err := rows.Scan(&at, &c.Before); err != nil {
				return err
			}
			c.At = time.UnixMilli(at).UTC()
			out.ZoneChanges = append(out.ZoneChanges, c)
			return nil
		})
	}
	if err != nil {
		return Exported{}, fmt.Errorf("buildday: exporting %s's rows: %w", account, err)
	}
	var g Goal
	var next sql.NullString
	err = q.QueryRowContext(ctx, `SELECT goal, freeze_days, freezes_held, freezes_earned, next_week FROM build_goals WHERE user_id = $1`, account).
		Scan(&g.WeeklyGoal, &g.FreezeDays, &g.FreezesHeld, &g.FreezesEarned, &next)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return Exported{}, fmt.Errorf("buildday: exporting %s's weekly goal: %w", account, err)
	default:
		g.NextWeek = next.String
		out.Goal = &g
	}
	// Sorted here, so the order doesn't depend on the engine.
	slices.SortFunc(out.Days, func(a, b StoredDay) int { return cmp.Compare(a.Date, b.Date) })
	slices.SortFunc(out.Weeks, func(a, b StoredWeek) int { return cmp.Compare(a.Start, b.Start) })
	slices.SortFunc(out.ZoneChanges, func(a, b ZoneChange) int { return a.At.Compare(b.At) })
	return out, nil
}

// each runs query with account and calls scan for each row.
func each(ctx context.Context, q Querier, query, account string, scan func(*sql.Rows) error) error {
	rows, err := q.QueryContext(ctx, query, account)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := scan(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}
