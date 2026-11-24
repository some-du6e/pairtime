// SPDX-License-Identifier: AGPL-3.0-or-later

// Package buildday decides build days, and the weeks and freezes they make
// (WP-SV12; DESIGN 6.2 D10, 3.8; docs/errata/2026-11-23-build-day-lag.md).
//
// A build day is a local day in the profile's time zone with at least
// BUILD_DAY_S credited seconds at level A or B that the server received
// within BUILD_DAY_LAG_S after the day ended. Its credit comes from the
// as-of read (rollup.CreditAsOf, rollup.CreditReached), which takes only
// minute-bucket revisions the server itself labeled witnessed or offline
// (DESIGN 7.4); a revision stored with no label, by a release from before
// migration 0009, goes by its bucket's own reading. Lookback, minutes
// uploaded late outside a catch-up and level C never count, whatever the
// device sent. A day counts the moment its credit reaches BUILD_DAY_S, and
// a day still short at its deadline, its end plus BUILD_DAY_LAG_S
// (rollup.History.Deadline), is decided then not to count. Either way the
// job stores the day in build_days, and nothing changes or deletes the row
// after that, not even the minute-bucket job, which deletes the minutes it
// was read from at 180 days.
//
// The job runs beside the settle job, every rollup.SettleEvery. It finds the
// days it hasn't stored through rollups_daily's (user_id, day) index, never
// by scanning rollup_minutes.day: on its first run every day ingest still
// holds minutes of, and after that the last 32 days, or back to the
// account's next_week when that is earlier. Either reach starts on a
// Monday, so the week that holds its first date is decided whole, and a day
// of it whose minutes ingest no longer holds is decided not to count. It
// reads on the read pool whether each open day's credit has reached
// BUILD_DAY_S at any time up to now, and takes the writer only to store a
// day that counted or to decide one whose deadline has passed. It decides
// those inside the writer op, as the settle job settles minutes: such a day
// counts if its credit reached BUILD_DAY_S at any time up to the deadline
// (#278). So a revision ingest received before the deadline in a batch that
// committed after it still counts, and a late or first run decides as runs
// on time would have, and stores the same credit: the day's credit when it
// first reached BUILD_DAY_S, or else as of its deadline.
//
// A late or first run applies an account's days and weeks in the order they
// take effect, across ops too (#283). An op decides only the weeks due
// before the account's next day still queued. When it can't decide a week
// due before one of its days, because it has decided opWeeks weeks or a day
// of the week isn't decided yet, it applies nothing from that day on, and
// the account waits for the next run, so no freeze a later day earns
// reaches that week first.
//
// Weeks run Monday to Sunday by the local dates the days already have, and
// a week keeps the goal in force when it was decided (orchestrator,
// 2026-11-23). A week is met the moment its build days reach the goal. It
// is missed only once its last day's deadline has passed, and then it spends
// a held freeze if there is one, which makes it frozen. Every
// FREEZE_EARN_DAYS build days earn a freeze unless FREEZE_MAX are held; a
// build day that would earn one while FREEZE_MAX are held earns nothing, and
// the count starts over. Weeks, freezes, consistency and the summary count a
// build day from the moment it is stored. There is no daily streak counter.
//
// The weekly goal is WEEKLY_GOAL_DEFAULT until the teen sets another, from
// 1 to 7 build days, on the local Build days page (GoalAPI, SetGoal). A
// change applies to every week not decided yet, so such a week whose build
// days already reach a lowered goal is met at once, last week too until its
// deadline, while a week decided keeps the goal it was decided by
// (orchestrator, 2026-11-24).
package buildday

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"slices"
	"sync"
	"time"

	"github.com/some-du6e/pairtime/internal/server/rollup"
	"github.com/some-du6e/pairtime/internal/server/writer"
	"github.com/some-du6e/pairtime/pkg/params"
)

// Params are the build-day parameters of the newest table pairtimed
// embeds, as the settle job takes PENDING_WINDOW_S (DESIGN 6.2 D10, 6.3).
type Params struct {
	// Needed is BUILD_DAY_S, the credit at level A or B a build day needs.
	Needed time.Duration
	// Lag is BUILD_DAY_LAG_S, how long after its end a day's credit can
	// still arrive.
	Lag time.Duration
	// Goal is WEEKLY_GOAL_DEFAULT, in build days a week.
	Goal int64
	// EarnDays is FREEZE_EARN_DAYS, the build days that earn a freeze, and
	// MaxHeld FREEZE_MAX, the most freezes held at once.
	EarnDays, MaxHeld int64
	// Window is CONSISTENCY_WINDOW_D, the days consistency counts build
	// days in.
	Window int
}

// Current returns the newest table's Params.
var Current = sync.OnceValue(func() Params {
	tables := params.Tables()
	t := tables[len(tables)-1]
	return Params{
		Needed:   time.Duration(t.Int(params.BUILD_DAY_S)) * time.Second,
		Lag:      time.Duration(t.Int(params.BUILD_DAY_LAG_S)) * time.Second,
		Goal:     t.Int(params.WEEKLY_GOAL_DEFAULT),
		EarnDays: t.Int(params.FREEZE_EARN_DAYS),
		MaxHeld:  t.Int(params.FREEZE_MAX),
		Window:   int(t.Int(params.CONSISTENCY_WINDOW_D)),
	}
})

// Querier reads: the read pool, or a writer transaction.
type Querier = rollup.Querier

// Doer runs a write through the writer goroutine. *writer.Writer is one.
type Doer interface {
	Do(ctx context.Context, rows int, op writer.Op) error
}

// The horizons the job looks for days it hasn't stored in. recent covers
// the summary's CONSISTENCY_WINDOW_D days and two more, so no day the
// summary lists stays open past its deadline, and every day whose deadline
// is still ahead; a run also reaches back to the account's next_week, so a
// day first uploaded later than that never holds its week up. full covers
// every day ingest still holds minutes of, 180 days (DESIGN 4.4), which the
// first run decides. A run reaches back from the Monday of the horizon's
// week, so the week that holds the horizon date never waits on days no
// run looks at.
const (
	recentHorizon = 32 * 24 * time.Hour
	fullHorizon   = 182 * 24 * time.Hour
)

// The bounds of one run, so a backlog can't keep the writer busy for long:
// at most opDays days and opWeeks weeks in one op, and at most maxOps ops a
// run. The next run goes on where this one stopped. rowsPerDay is the rows
// storing a day writes, its build_days row, the account's build_goals row
// and a build_weeks row when its week is met, and rowsPerWeek the rows a
// decided week writes.
const (
	opDays      = 50
	opWeeks     = 20
	maxOps      = 100
	rowsPerDay  = 3
	rowsPerWeek = 2
)

// Result says what one run did.
type Result struct {
	// Counted is the days stored as build days before their deadline, and
	// Decided the days decided at their deadline, whether or not they
	// counted.
	Counted, Decided int
	// Weeks is the weeks decided at their last day's deadline.
	Weeks int
	// More says the run stopped at its bounds with work left.
	More bool
}

// Job decides build days and weeks.
type Job struct {
	d   Doer
	q   Querier
	now func() time.Time
	// full says the next run looks for days back to fullHorizon, as the
	// first run does, until a run finishes its work.
	full bool
}

// NewJob returns a job that writes through d, reads q, the read pool, and
// tells time by now.
func NewJob(d Doer, q Querier, now func() time.Time) *Job {
	return &Job{d: d, q: q, now: now, full: true}
}

// Run runs Once at once, then every interval, until ctx ends. report, when
// set, hears each run's result and error.
func Run(ctx context.Context, j *Job, every time.Duration, report func(Result, error)) {
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		r, err := j.Once(ctx)
		if report != nil {
			report(r, err)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// candidate is a day of an account with rollups that build_days doesn't
// hold yet.
type candidate struct {
	account, day string
}

// item is a day to store: one whose credit has reached BUILD_DAY_S before
// its deadline, or, when due is set, one whose deadline has passed. at is
// when it takes effect by the read pool's look, the moment its credit first
// reached BUILD_DAY_S or else its deadline, which orders the account's
// items across ops. The op reads its credit again inside the writer.
type item struct {
	day string
	due bool
	at  int64
}

// Once runs the job once: it stores the days that have counted and
// decides those whose deadline has passed, then decides the weeks whose
// last day's deadline has passed.
func (j *Job) Once(ctx context.Context) (Result, error) {
	var res Result
	p := Current()
	now := j.now()
	horizon := recentHorizon
	if j.full {
		horizon = fullHorizon
	}
	from, err := weekOf(now.Add(-horizon).UTC().Format(time.DateOnly))
	if err != nil {
		return res, err
	}
	cands, err := candidates(ctx, j.q, from)
	if err != nil {
		return res, err
	}
	ops := 0
	more := false
	// stopped holds the accounts whose items an op left behind a week it
	// couldn't decide yet. They wait for the next run, and the weeks pass
	// skips them, so none of their weeks is decided before a day that takes
	// effect earlier.
	stopped := map[string]bool{}
accounts:
	for len(cands) > 0 {
		account := cands[0].account
		n := 1
		for n < len(cands) && cands[n].account == account {
			n++
		}
		days := cands[:n]
		cands = cands[n:]
		h, err := rollup.ZoneHistory(ctx, j.q, account)
		if errors.Is(err, rollup.ErrNoAccount) {
			continue
		}
		if err != nil {
			return res, err
		}
		var items []item
		for _, c := range days {
			deadline, err := h.Deadline(c.day, p.Lag)
			if err != nil {
				return res, err
			}
			due := now.UnixMilli() >= deadline
			until := min(now.UnixMilli(), deadline)
			_, when, reached, err := rollup.CreditReached(ctx, j.q, account, c.day, until, p.Needed.Milliseconds())
			if err != nil {
				return res, err
			}
			if reached || due {
				items = append(items, item{day: c.day, due: due, at: when})
			}
		}
		// In the order the items take effect, so ops of a backlog apply them
		// as runs on time would have.
		slices.SortFunc(items, func(a, b item) int { return cmp.Or(cmp.Compare(a.at, b.at), cmp.Compare(a.day, b.day)) })
		for len(items) > 0 {
			if ops == maxOps {
				more = true
				break accounts
			}
			n := min(len(items), opDays)
			next := int64(math.MaxInt64)
			if n < len(items) {
				next = items[n].at
			}
			r, used, err := j.settle(ctx, account, items[:n], next, p)
			if err != nil {
				return res, err
			}
			ops++
			res.Counted += r.Counted
			res.Decided += r.Decided
			res.Weeks += r.Weeks
			if used < n {
				stopped[account] = true
				continue accounts
			}
			items = items[n:]
		}
	}
	weeks, weeksMore, err := j.weeks(ctx, now, p, maxOps-ops, stopped)
	res.Weeks += weeks
	if err != nil {
		return res, err
	}
	res.More = more || len(stopped) > 0 || weeksMore
	if !more {
		j.full = false
	}
	return res, nil
}

// candidates returns the days with rollups that build_days doesn't hold, on
// or after from, a date, or the account's next_week when that is earlier,
// by account and then date. It reads rollups_daily through its (user_id,
// day) index, account by account.
func candidates(ctx context.Context, q Querier, from string) ([]candidate, error) {
	rows, err := q.QueryContext(ctx, `SELECT DISTINCT d.user_id, d.day FROM users u
LEFT JOIN build_goals g ON g.user_id = u.id
JOIN rollups_daily d ON d.user_id = u.id
AND d.day >= CASE WHEN g.next_week IS NOT NULL AND g.next_week < $1 THEN g.next_week ELSE $1 END
WHERE NOT EXISTS (SELECT 1 FROM build_days b WHERE b.user_id = d.user_id AND b.day = d.day)`, from)
	if err != nil {
		return nil, fmt.Errorf("buildday: finding the days to decide: %w", err)
	}
	defer rows.Close()
	var out []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.account, &c.day); err != nil {
			return nil, fmt.Errorf("buildday: finding the days to decide: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("buildday: finding the days to decide: %w", err)
	}
	// Sorted here, so the order doesn't depend on the engine's collation.
	slices.SortFunc(out, func(a, b candidate) int {
		return cmp.Or(cmp.Compare(a.account, b.account), cmp.Compare(a.day, b.day))
	})
	return out, nil
}

// settle stores the account's items and decides its due weeks in one
// writer op, in time order, so freezes are earned and spent as runs on time
// would have earned and spent them: a day that counts takes effect at the
// moment its credit first reached BUILD_DAY_S, a day that doesn't at its
// deadline, and a week at its deadline, once all its days are decided. A
// day whose deadline has passed counts if its credit reached BUILD_DAY_S at
// any time up to the deadline (rollup.CreditReached; #278). A day that
// counts stores its credit at that first moment, as a run on time does, and
// one that doesn't stores its credit as of the deadline. A day stored
// meanwhile is left as it is.
//
// next is when the account's next item still queued takes effect, or
// math.MaxInt64 when items holds its last: the op decides only the weeks
// due before next, and the op that applies that item decides the rest.
// When a week due before one of the items can't be decided in this op,
// because the op has decided opWeeks weeks or a day of the week isn't
// decided yet, the op applies nothing from that item on, so the freezes
// they earn never reach the week first. settle returns how many of the
// items it used, fewer than len(items) then.
func (j *Job) settle(ctx context.Context, account string, items []item, next int64, p Params) (Result, int, error) {
	var res Result
	var used int
	err := j.d.Do(ctx, rowsPerDay*len(items)+rowsPerWeek*(len(items)/7+1), func(ctx context.Context, tx writer.Tx) error {
		res, used = Result{}, len(items)
		at := j.now().UnixMilli()
		need := p.Needed.Milliseconds()
		g, err := loadGoals(ctx, tx, account, p)
		if err != nil {
			return err
		}
		// The zone as the op sees it, which a change since the read pool's
		// look may have moved a deadline by.
		h, err := rollup.ZoneHistory(ctx, tx, account)
		if err != nil {
			return err
		}
		type event struct {
			at          int64
			day         string
			due, counts bool
			credit      rollup.Credit
		}
		var evs []event
		for _, it := range items {
			held, err := stored(ctx, tx, account, it.day)
			if err != nil {
				return err
			}
			if held {
				continue
			}
			until := at
			if it.due {
				deadline, err := h.Deadline(it.day, p.Lag)
				if err != nil {
					return err
				}
				if at < deadline {
					continue
				}
				until = deadline
			}
			credit, when, reached, err := rollup.CreditReached(ctx, tx, account, it.day, until, need)
			if err != nil {
				return err
			}
			if reached || it.due {
				evs = append(evs, event{at: when, day: it.day, due: it.due, counts: reached, credit: credit})
			}
		}
		slices.SortFunc(evs, func(a, b event) int { return cmp.Or(cmp.Compare(a.at, b.at), cmp.Compare(a.day, b.day)) })
		// weeksBefore decides the account's weeks whose deadline is before t,
		// oldest first, and reports false when it leaves one undecided: the
		// op has decided opWeeks weeks, or a day of the week isn't decided.
		weeksBefore := func(t int64) (bool, error) {
			for g.nextWeek != "" {
				w := g.nextWeek
				deadline, err := weekDeadline(h, w, p.Lag)
				if err != nil || deadline >= t {
					return err == nil, err
				}
				if res.Weeks == opWeeks {
					return false, nil
				}
				ok, err := daysDecided(ctx, tx, account, w)
				if err != nil || !ok {
					return false, err
				}
				if err := g.decideWeek(ctx, tx, account, w, at); err != nil {
					return false, err
				}
				res.Weeks++
			}
			return true, nil
		}
		for i, e := range evs {
			ok, err := weeksBefore(e.at)
			if err != nil {
				return err
			}
			if !ok {
				used = len(items) - (len(evs) - i)
				break
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO build_days (user_id, day, a_ms, b_ms, counted, decided_at_ms)
VALUES ($1, $2, $3, $4, $5, $6)`, account, e.day, e.credit.AMS, e.credit.BMS, boolInt(e.counts), at); err != nil {
				return fmt.Errorf("buildday: storing %s's %s: %w", account, e.day, err)
			}
			if e.due {
				res.Decided++
			} else {
				res.Counted++
			}
			if e.counts {
				if err := g.count(ctx, tx, account, e.day, at, p); err != nil {
					return err
				}
			}
		}
		if used == len(items) {
			if _, err := weeksBefore(min(at+1, next)); err != nil {
				return err
			}
		}
		return g.save(ctx, tx, account)
	})
	if err != nil {
		return Result{}, 0, fmt.Errorf("buildday: storing %s's days: %w", account, err)
	}
	return res, used, nil
}

// stored reports whether build_days holds the account's day.
func stored(ctx context.Context, q Querier, account, day string) (bool, error) {
	var one int
	err := q.QueryRowContext(ctx, `SELECT 1 FROM build_days WHERE user_id = $1 AND day = $2`, account, day).Scan(&one)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("buildday: reading %s's %s: %w", account, day, err)
	}
	return true, nil
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// DeleteAccount deletes every build day, week result and freeze of
// account, and the record of its time zone changes, inside tx: what the
// eraser (WP-SV13) runs when the account is deleted, before its users row.
func DeleteAccount(ctx context.Context, tx writer.Tx, account string) error {
	for _, table := range []string{"build_days", "build_weeks", "build_goals", "zone_changes"} {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE user_id = $1`, account); err != nil {
			return fmt.Errorf("buildday: deleting an account's %s: %w", table, err)
		}
	}
	return nil
}
