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

	"github.com/some-du6e/pairtime/internal/server/rollup"
	"github.com/some-du6e/pairtime/internal/server/writer"
)

// The results a week can have (build_weeks.result). A missed week stores
// no row: every week before an account's next_week without a row was
// missed.
const (
	Met    = "met"
	Frozen = "frozen"
	Missed = "missed"
	// Open is a week not decided yet, which the summary shows as such.
	Open = "open"
)

// goals is an account's build_goals row, as an op changes it.
type goals struct {
	exists, changed bool
	goal            int64
	// freezeDays is the build days counted toward the next freeze.
	freezeDays, held, earned int64
	// nextWeek is the Monday of the earliest week not decided yet, "" before
	// the account's first build day.
	nextWeek string
}

// loadGoals reads the account's build_goals row, or the defaults of p for
// an account without one.
func loadGoals(ctx context.Context, q Querier, account string, p Params) (*goals, error) {
	g := &goals{goal: p.Goal}
	var next sql.NullString
	err := q.QueryRowContext(ctx, `SELECT goal, freeze_days, freezes_held, freezes_earned, next_week FROM build_goals WHERE user_id = $1`, account).
		Scan(&g.goal, &g.freezeDays, &g.held, &g.earned, &next)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return g, nil
	case err != nil:
		return nil, fmt.Errorf("buildday: reading %s's weekly goal: %w", account, err)
	}
	g.exists, g.nextWeek = true, next.String
	return g, nil
}

// save stores g as the account's build_goals row, if it changed.
func (g *goals) save(ctx context.Context, tx writer.Tx, account string) error {
	if !g.changed {
		return nil
	}
	var next any
	if g.nextWeek != "" {
		next = g.nextWeek
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO build_goals (user_id, goal, freeze_days, freezes_held, freezes_earned, next_week)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (user_id) DO UPDATE SET goal = excluded.goal, freeze_days = excluded.freeze_days,
freezes_held = excluded.freezes_held, freezes_earned = excluded.freezes_earned, next_week = excluded.next_week`,
		account, g.goal, g.freezeDays, g.held, g.earned, next); err != nil {
		return fmt.Errorf("buildday: storing %s's weekly goal: %w", account, err)
	}
	g.exists, g.changed = true, false
	return nil
}

// count notes a build day of the account stored at at, inside tx. It counts
// toward the next freeze: every p.EarnDays build days earn one, unless
// p.MaxHeld are held, and then the count starts over with nothing earned.
// Its week is followed from now on if it wasn't, and it is met at once if
// its build days reach the goal.
func (g *goals) count(ctx context.Context, tx writer.Tx, account, day string, at int64, p Params) error {
	g.changed = true
	g.freezeDays++
	if g.freezeDays >= p.EarnDays {
		g.freezeDays = 0
		if g.held < p.MaxHeld {
			g.held++
			g.earned++
		}
	}
	w, err := weekOf(day)
	if err != nil {
		return err
	}
	// A week before next_week whose day only counts now wasn't decided:
	// its days are all decided before it is.
	if g.nextWeek == "" || w < g.nextWeek {
		g.nextWeek = w
	}
	if done, err := weekStored(ctx, tx, account, w); err != nil || done {
		return err
	}
	n, err := buildDaysIn(ctx, tx, account, w)
	if err != nil {
		return err
	}
	if n >= g.goal {
		return putWeek(ctx, tx, account, w, g.goal, n, Met, at)
	}
	return nil
}

// weekOf returns the Monday of day's week, a local date (ISO 8601 weeks,
// Monday to Sunday).
func weekOf(day string) (string, error) {
	d, err := time.Parse(time.DateOnly, day)
	if err != nil {
		return "", fmt.Errorf("buildday: %q isn't a date: %w", day, err)
	}
	return d.AddDate(0, 0, -((int(d.Weekday()) + 6) % 7)).Format(time.DateOnly), nil
}

// addDays returns the date n days after day, a date this package made.
func addDays(day string, n int) string {
	d, _ := time.Parse(time.DateOnly, day)
	return d.AddDate(0, 0, n).Format(time.DateOnly)
}

// weekDeadline returns when the week that starts on Monday week is
// decided: the latest deadline of its seven days.
func weekDeadline(h rollup.History, week string, lag time.Duration) (int64, error) {
	var latest int64
	for i := range 7 {
		d, err := h.Deadline(addDays(week, i), lag)
		if err != nil {
			return 0, err
		}
		latest = max(latest, d)
	}
	return latest, nil
}

// weekStored reports whether build_weeks holds the account's week.
func weekStored(ctx context.Context, q Querier, account, week string) (bool, error) {
	var one int
	err := q.QueryRowContext(ctx, `SELECT 1 FROM build_weeks WHERE user_id = $1 AND week = $2`, account, week).Scan(&one)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("buildday: reading %s's week of %s: %w", account, week, err)
	}
	return true, nil
}

// buildDaysIn counts the account's build days in the week that starts on
// Monday week.
func buildDaysIn(ctx context.Context, q Querier, account, week string) (int64, error) {
	var n int64
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM build_days WHERE user_id = $1 AND day >= $2 AND day <= $3 AND counted = 1`,
		account, week, addDays(week, 6)).Scan(&n); err != nil {
		return 0, fmt.Errorf("buildday: counting %s's build days in the week of %s: %w", account, week, err)
	}
	return n, nil
}

// putWeek stores the account's week's result.
func putWeek(ctx context.Context, tx writer.Tx, account, week string, goal, n int64, result string, at int64) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO build_weeks (user_id, week, goal, build_days, result, decided_at_ms)
VALUES ($1, $2, $3, $4, $5, $6)`, account, week, goal, n, result, at); err != nil {
		return fmt.Errorf("buildday: storing %s's week of %s: %w", account, week, err)
	}
	return nil
}

// weekDue reports whether the account's week that starts on Monday week can
// be decided at nowMS: its last day's deadline has passed, and its days are
// all decided.
func weekDue(ctx context.Context, q Querier, h rollup.History, account, week string, nowMS int64, p Params) (bool, error) {
	deadline, err := weekDeadline(h, week, p.Lag)
	if err != nil || nowMS < deadline {
		return false, err
	}
	return daysDecided(ctx, q, account, week)
}

// daysDecided reports whether every day of the account's week that starts
// on Monday week that has rollups is in build_days.
func daysDecided(ctx context.Context, q Querier, account, week string) (bool, error) {
	var one int
	err := q.QueryRowContext(ctx, `SELECT 1 FROM rollups_daily d WHERE d.user_id = $1 AND d.day >= $2 AND d.day <= $3
AND NOT EXISTS (SELECT 1 FROM build_days b WHERE b.user_id = d.user_id AND b.day = d.day) LIMIT 1`,
		account, week, addDays(week, 6)).Scan(&one)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return true, nil
	case err != nil:
		return false, fmt.Errorf("buildday: reading %s's undecided days in the week of %s: %w", account, week, err)
	}
	return false, nil
}

// decideWeek decides the account's week that starts on Monday w, g's next
// week, at at, and moves g's next week on. A week met before isn't decided
// again. Any other week is met if its build days reach the goal, frozen if
// a freeze is held, which it spends, and missed, with no row, if not.
func (g *goals) decideWeek(ctx context.Context, tx writer.Tx, account, w string, at int64) error {
	done, err := weekStored(ctx, tx, account, w)
	if err != nil {
		return err
	}
	if !done {
		n, err := buildDaysIn(ctx, tx, account, w)
		if err != nil {
			return err
		}
		switch {
		case n >= g.goal:
			err = putWeek(ctx, tx, account, w, g.goal, n, Met, at)
		case g.held > 0:
			g.held--
			err = putWeek(ctx, tx, account, w, g.goal, n, Frozen, at)
		}
		if err != nil {
			return err
		}
	}
	g.nextWeek = addDays(w, 7)
	g.changed = true
	return nil
}

// weeks decides, in at most budget ops, each account's weeks whose last
// day's deadline has passed, oldest first, and reports how many it decided
// and whether it stopped at its bounds with weeks left. It counts the due
// weeks on the read pool first and takes the writer only for an account
// that has one. It skips the accounts in skip, whose days this run left in
// part.
func (j *Job) weeks(ctx context.Context, now time.Time, p Params, budget int, skip map[string]bool) (int, bool, error) {
	// A week's last day ends at least 6 days after its Monday in any zone,
	// so its deadline is at least that plus BUILD_DAY_LAG_S after it.
	latest := now.Add(-6*24*time.Hour - p.Lag).UTC().Format(time.DateOnly)
	rows, err := j.q.QueryContext(ctx, `SELECT user_id, next_week FROM build_goals WHERE next_week IS NOT NULL AND next_week <= $1`, latest)
	if err != nil {
		return 0, false, fmt.Errorf("buildday: finding the weeks to decide: %w", err)
	}
	type next struct{ account, week string }
	var due []next
	for rows.Next() {
		var n next
		if err := rows.Scan(&n.account, &n.week); err != nil {
			rows.Close()
			return 0, false, fmt.Errorf("buildday: finding the weeks to decide: %w", err)
		}
		due = append(due, n)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, false, fmt.Errorf("buildday: finding the weeks to decide: %w", err)
	}
	slices.SortFunc(due, func(a, b next) int { return cmp.Compare(a.account, b.account) })
	total := 0
	for _, n := range due {
		if skip[n.account] {
			continue
		}
		h, err := rollup.ZoneHistory(ctx, j.q, n.account)
		if errors.Is(err, rollup.ErrNoAccount) {
			continue
		}
		if err != nil {
			return total, false, err
		}
		k := 0
		for w := n.week; k < opWeeks; w = addDays(w, 7) {
			ok, err := weekDue(ctx, j.q, h, n.account, w, now.UnixMilli(), p)
			if err != nil {
				return total, false, err
			}
			if !ok {
				break
			}
			k++
		}
		if k == 0 {
			continue
		}
		if budget == 0 {
			return total, true, nil
		}
		budget--
		decided, err := j.decideWeeks(ctx, n.account, k, p)
		total += decided
		if err != nil {
			return total, false, err
		}
		if decided == opWeeks {
			return total, true, nil
		}
	}
	return total, false, nil
}

// decideWeeks decides up to most of the account's weeks from its next_week
// on, inside one writer op, while each is due by the op's own clock. A week
// met before isn't decided again. Any other week is met if its build days
// reach the goal, frozen if a freeze is held, which it spends, and missed,
// with no row, if not. It returns how many weeks it decided.
func (j *Job) decideWeeks(ctx context.Context, account string, most int, p Params) (int, error) {
	var decided int
	err := j.d.Do(ctx, rowsPerWeek*most, func(ctx context.Context, tx writer.Tx) error {
		decided = 0
		at := j.now().UnixMilli()
		g, err := loadGoals(ctx, tx, account, p)
		if err != nil {
			return err
		}
		h, err := rollup.ZoneHistory(ctx, tx, account)
		if err != nil {
			return err
		}
		for decided < most && g.nextWeek != "" {
			w := g.nextWeek
			ok, err := weekDue(ctx, tx, h, account, w, at, p)
			if err != nil {
				return err
			}
			if !ok {
				break
			}
			if err := g.decideWeek(ctx, tx, account, w, at); err != nil {
				return err
			}
			decided++
		}
		return g.save(ctx, tx, account)
	})
	if err != nil {
		return 0, fmt.Errorf("buildday: deciding %s's weeks: %w", account, err)
	}
	return decided, nil
}
