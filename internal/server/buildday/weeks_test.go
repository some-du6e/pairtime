// SPDX-License-Identifier: AGPL-3.0-or-later

package buildday

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/store/storetest"
	"github.com/some-du6e/pairtime/internal/server/writer"
	"github.com/some-du6e/pairtime/pkg/event"
)

// buildOn makes the date y-mo-d a build day of the world's UTC account: 900
// seconds at level A that morning, uploaded as each minute ends, and a run
// of the job right after.
func buildOn(w *world, y int, mo time.Month, d int) {
	w.t.Helper()
	w.online(full("d1", utc(y, mo, d, 10, 0), 15, event.EvidenceA))
	w.run(utc(y, mo, d, 10, 16))
}

// uploadOn uploads n minutes of 60 seconds at level A from 10:00 on the
// date y-mo-d, each as it ends, with no run of the job: 15 make a build
// day, which reaches 900 seconds at 10:15.
func uploadOn(w *world, y int, mo time.Month, d, n int) {
	w.t.Helper()
	w.online(full("d1", utc(y, mo, d, 10, 0), n, event.EvidenceA))
}

// nextWeek returns the account's build_goals next_week, "" without one.
func (w *world) nextWeek() string {
	w.t.Helper()
	var next sql.NullString
	err := w.db.Read.QueryRowContext(context.Background(), `SELECT next_week FROM build_goals WHERE user_id = 'u1'`).Scan(&next)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		w.t.Fatal(err)
	}
	return next.String
}

// frozenWeeks returns the Mondays of the account's frozen weeks, in order.
func (w *world) frozenWeeks() []string {
	w.t.Helper()
	got, err := weekResults(context.Background(), w.db.Read, "u1")
	if err != nil {
		w.t.Fatal(err)
	}
	var out []string
	for week, r := range got {
		if r.Result == Frozen {
			out = append(out, week)
		}
	}
	slices.Sort(out)
	return out
}

// weekResult returns the build_weeks result of the account's week that
// starts on Monday week, "" when there is no row.
func (w *world) weekResult(week string) string {
	w.t.Helper()
	got, err := weekResults(context.Background(), w.db.Read, "u1")
	if err != nil {
		w.t.Fatal(err)
	}
	return got[week].Result
}

// TestWeeks checks D10's weekly goal, weeks in a row and freezes: weeks run
// Monday to Sunday, every 7 build days earn a freeze unless 2 are held, a
// week is met the moment it reaches the goal and missed only once its last
// day's deadline has passed, spending a held freeze. 2026-11-02 is a
// Monday.
func TestWeeks(t *testing.T) {
	tests := []struct {
		name  string
		do    func(w *world)
		check func(t *testing.T, w *world)
	}{
		{
			name: "a week is met the moment its build days reach the goal, before it ends",
			do: func(w *world) {
				for d := 16; d <= 18; d++ {
					buildOn(w, 2026, 11, d)
				}
			},
			check: func(t *testing.T, w *world) {
				if got := w.weekResult("2026-11-16"); got != Met {
					t.Errorf("the week of 2026-11-16 is %q after 3 build days, want met", got)
				}
				if s := w.summary(utc(2026, 11, 18, 12, 0)); s.WeeksInARow != 1 || s.WeeklyGoal != 3 {
					t.Errorf("weeks in a row %d, goal %d; want 1 and 3", s.WeeksInARow, s.WeeklyGoal)
				}
			},
		},
		{
			name: "every 7 build days earn a freeze",
			do: func(w *world) {
				for d := 2; d <= 8; d++ {
					buildOn(w, 2026, 11, d)
				}
			},
			check: func(t *testing.T, w *world) {
				if f := w.summary(utc(2026, 11, 8, 12, 0)).Freezes; f.Held != 1 || f.Earned != 1 || f.TowardNext != 0 || f.Every != 7 || f.Max != 2 {
					t.Errorf("freezes %+v, want 1 held and earned, 0 of 7 toward the next, at most 2", f)
				}
			},
		},
		{
			name: "a missed week spends a held freeze only once its last day's deadline has passed, and counts as frozen",
			do: func(w *world) {
				for d := 2; d <= 9; d++ {
					buildOn(w, 2026, 11, d)
				}
				// The week of 2026-11-09 has one build day. Its Sunday ends
				// at 2026-11-16T00:00Z and is decided 72 hours later.
				w.run(utc(2026, 11, 19, 0, 0).Add(-time.Millisecond))
				if f := w.summary(utc(2026, 11, 19, 0, 0).Add(-time.Millisecond)).Freezes; f.Held != 1 {
					w.t.Errorf("%d freezes held a millisecond before the week's deadline, want 1", f.Held)
				}
				if got := w.weekResult("2026-11-09"); got != "" {
					w.t.Errorf("the week of 2026-11-09 is %q before its deadline, want no result", got)
				}
				w.run(utc(2026, 11, 19, 0, 0))
			},
			check: func(t *testing.T, w *world) {
				if got := w.weekResult("2026-11-09"); got != Frozen {
					t.Errorf("the week of 2026-11-09 is %q, want frozen", got)
				}
				s := w.summary(utc(2026, 11, 19, 0, 0))
				if s.Freezes.Held != 0 || s.Freezes.Earned != 1 {
					t.Errorf("freezes %+v, want the one earned spent", s.Freezes)
				}
				// The weeks of 2026-11-02 (met) and 2026-11-09 (frozen); the
				// week of 2026-11-16 is open.
				if s.WeeksInARow != 2 {
					t.Errorf("weeks in a row %d, want 2", s.WeeksInARow)
				}
			},
		},
		{
			name: "a missed week with no freeze held ends the weeks in a row",
			do: func(w *world) {
				for d := 16; d <= 18; d++ {
					buildOn(w, 2026, 11, d)
				}
				// The week of 2026-11-23 has no build day; its Sunday ends at
				// 2026-11-30T00:00Z.
				w.run(utc(2026, 12, 3, 0, 0))
			},
			check: func(t *testing.T, w *world) {
				if got := w.weekResult("2026-11-23"); got != "" {
					t.Errorf("the week of 2026-11-23 is %q, want no row, as every missed week has", got)
				}
				s := w.summary(utc(2026, 12, 3, 0, 0))
				if s.WeeksInARow != 0 {
					t.Errorf("weeks in a row %d, want 0", s.WeeksInARow)
				}
				var missed bool
				for _, wk := range s.Weeks {
					missed = missed || wk.Start == "2026-11-23" && wk.Result == Missed
				}
				if !missed {
					t.Errorf("the summary's weeks %+v don't show the week of 2026-11-23 missed", s.Weeks)
				}
			},
		},
		{
			name: "a 7th build day while 2 freezes are held earns nothing, and the count starts over",
			do: func(w *world) {
				for d := 2; d <= 22; d++ {
					buildOn(w, 2026, 11, d)
				}
				if f := w.summary(utc(2026, 11, 22, 12, 0)).Freezes; f.Held != 2 || f.Earned != 2 || f.TowardNext != 0 {
					w.t.Errorf("after 21 build days freezes are %+v, want 2 held and earned and the count started over", f)
				}
				buildOn(w, 2026, 11, 23)
			},
			check: func(t *testing.T, w *world) {
				if f := w.summary(utc(2026, 11, 23, 12, 0)).Freezes; f.Held != 2 || f.Earned != 2 || f.TowardNext != 1 {
					t.Errorf("after 22 build days freezes are %+v, want 2 held and earned and 1 toward the next", f)
				}
			},
		},
		{
			name: "a first run over a backlog earns and spends freezes in time order, as runs on time would have",
			do: func(w *world) {
				// 2026-06-01 is a Monday. Two weeks of build days, a week with
				// none, then a week of build days, and no run until July.
				var days []time.Time
				for d := 1; d <= 14; d++ {
					days = append(days, utc(2026, 6, d, 0, 0))
				}
				for d := 22; d <= 28; d++ {
					days = append(days, utc(2026, 6, d, 0, 0))
				}
				for _, d := range days {
					w.online(full("d1", d.Add(10*time.Hour), 15, event.EvidenceA))
				}
				w.run(utc(2026, 7, 8, 12, 0))
			},
			check: func(t *testing.T, w *world) {
				// On time, 2 freezes are held by 06-14, the week of 06-15 spends
				// one at its deadline, 06-25T00:00Z, and the 21st build day,
				// 06-28, earns one back.
				if f := w.summary(utc(2026, 7, 8, 12, 0)).Freezes; f.Held != 2 || f.Earned != 3 {
					t.Errorf("freezes %+v, want 2 held and 3 earned", f)
				}
				if got := w.weekResult("2026-06-15"); got != Frozen {
					t.Errorf("the week of 2026-06-15 is %q, want frozen", got)
				}
			},
		},
		{
			name: "a first run splits a backlog of more than 50 days across ops in time order, so a missed week waits for the freeze the next op's first day earns before its deadline",
			do: func(w *world) {
				if opDays != 50 {
					w.t.Fatalf("the case splits 51 days after the 50th, and opDays is %d", opDays)
				}
				// Seven weeks of days from Monday 2026-09-21, with a build day
				// on each of the first five Mondays and 60 s on the other 44
				// days. Then the week of 2026-11-09, whose only build day is
				// its Sunday, the 50th day to take effect and the 6th build
				// day, and Monday 2026-11-16, the 51st, whose build day is the
				// 7th and earns a freeze at 10:15, before that week's deadline,
				// 2026-11-19T00:00Z.
				for i := range 49 {
					d := utc(2026, 9, 21, 0, 0).AddDate(0, 0, i)
					n := 1
					if i%7 == 0 && i < 35 {
						n = 15
					}
					uploadOn(w, d.Year(), d.Month(), d.Day(), n)
				}
				uploadOn(w, 2026, 11, 15, 15)
				uploadOn(w, 2026, 11, 16, 15)
				w.run(utc(2026, 11, 23, 12, 0))
			},
			check: func(t *testing.T, w *world) {
				// On time, Monday's freeze is held at the week's deadline and
				// spent on it.
				if got := w.weekResult("2026-11-09"); got != Frozen {
					t.Errorf("the week of 2026-11-09 is %q, want frozen by the freeze 2026-11-16 earned", got)
				}
				if f := w.summary(utc(2026, 11, 23, 12, 0)).Freezes; f.Held != 0 || f.Earned != 1 || f.TowardNext != 0 {
					t.Errorf("freezes %+v, want the one earned spent and 0 toward the next", f)
				}
				if got := w.frozenWeeks(); len(got) != 1 {
					t.Errorf("frozen weeks %v, want only 2026-11-09", got)
				}
			},
		},
		{
			name: "a first run over more than 20 due weeks stops at the 20th and goes on in the next run, so freezes cover the weeks runs on time would",
			do: func(w *world) {
				// 25 weeks from Monday 2026-06-01, each with build days on
				// Saturday and Sunday only, so each misses the goal of 3, and
				// every 7th build day earns a freeze in time for its own week.
				for i := range 25 {
					sat := utc(2026, 6, 6, 0, 0).AddDate(0, 0, 7*i)
					sun := sat.AddDate(0, 0, 1)
					uploadOn(w, sat.Year(), sat.Month(), sat.Day(), 15)
					uploadOn(w, sun.Year(), sun.Month(), sun.Day(), 15)
				}
				if opWeeks != 20 {
					w.t.Fatalf("the case needs the first run to stop after 20 weeks, and opWeeks is %d", opWeeks)
				}
				// The 25th week's deadline is 2026-11-26T00:00Z. The first run
				// decides 20 weeks and stops at Saturday 2026-10-31, the first
				// day after the 21st week's deadline.
				at := utc(2026, 11, 26, 12, 0)
				if r := w.run(at); !r.More || r.Weeks != 20 {
					w.t.Errorf("the first run = %+v, want 20 weeks decided and work left", r)
				}
				if _, ok := w.day("2026-10-31"); ok {
					w.t.Errorf("the first run stored 2026-10-31, past the week it couldn't decide")
				}
				for i := 0; ; i++ {
					at = at.Add(time.Minute)
					if !w.run(at).More {
						break
					}
					if i == 3 {
						w.t.Fatal("five runs left work")
					}
				}
			},
			check: func(t *testing.T, w *world) {
				// Weeks 4, 7, 11, 14, 18, 21 and 25.
				want := []string{"2026-06-22", "2026-07-13", "2026-08-10", "2026-08-31", "2026-09-28", "2026-10-19", "2026-11-16"}
				if got := w.frozenWeeks(); !slices.Equal(got, want) {
					t.Errorf("frozen weeks %v, want %v as runs on time freeze them", got, want)
				}
				if f := w.summary(utc(2026, 11, 26, 12, 0)).Freezes; f.Held != 0 || f.Earned != 7 || f.TowardNext != 1 {
					t.Errorf("freezes %+v, want 7 earned, all spent, and 1 toward the next", f)
				}
				if got := w.nextWeek(); got != "2026-11-23" {
					t.Errorf("next_week is %q, want 2026-11-23, every due week decided", got)
				}
			},
		},
		{
			name: "a first run over more than 182 days of rollups decides the week that holds the horizon date whole, its days whose minutes are gone not counted, and the weeks after in time order",
			do: func(w *world) {
				// The run at 2026-11-25T12:00Z looks back 182 days, to
				// Wednesday 2026-05-27, from the Monday of its week. That
				// week's Monday and Tuesday were build days, whose minutes
				// the retention jobs have deleted, and Saturday and Sunday
				// are build days still within the 180 days of minutes. The
				// week of 2026-06-01 has nothing, and the week of 2026-06-08
				// has 5 build days, whose 5th, the 7th in all, earns a freeze
				// after the deadline of the week of 2026-06-01, 2026-06-11T00:00Z.
				for _, d := range []int{25, 26, 30, 31} {
					uploadOn(w, 2026, 5, d, 15)
				}
				for d := 8; d <= 12; d++ {
					uploadOn(w, 2026, 6, d, 15)
				}
				at := utc(2026, 11, 25, 12, 0)
				w.sweepMinutes(at)
				if r := w.run(at); r.More {
					w.t.Errorf("the run left work: %+v", r)
				}
			},
			check: func(t *testing.T, w *world) {
				for _, day := range []string{"2026-05-25", "2026-05-26"} {
					if r, ok := w.day(day); !ok || r.counted || r.a != 0 {
						t.Errorf("%s is %+v (stored %v), want it decided not to count, with no minutes to read", day, r, ok)
					}
				}
				for _, c := range []struct{ week, want string }{
					{"2026-05-25", ""}, {"2026-06-01", ""}, {"2026-06-08", Met}, {"2026-06-15", Frozen}, {"2026-06-22", ""},
				} {
					if got := w.weekResult(c.week); got != c.want {
						t.Errorf("the week of %s is %q, want %q", c.week, got, c.want)
					}
				}
				if f := w.summary(utc(2026, 11, 25, 12, 0)).Freezes; f.Held != 0 || f.Earned != 1 {
					t.Errorf("freezes %+v, want the one earned spent on the week of 2026-06-15", f)
				}
				if got := w.nextWeek(); got != "2026-11-16" {
					t.Errorf("next_week is %q, want 2026-11-16, every due week decided", got)
				}
			},
		},
		{
			name: "a week met early shows every build day it gets after",
			do: func(w *world) {
				for d := 16; d <= 20; d++ {
					buildOn(w, 2026, 11, d)
				}
			},
			check: func(t *testing.T, w *world) {
				var got *Week
				s := w.summary(utc(2026, 11, 20, 12, 0))
				for i := range s.Weeks {
					if s.Weeks[i].Start == "2026-11-16" {
						got = &s.Weeks[i]
					}
				}
				if got == nil || got.Result != Met || got.BuildDays != 5 || got.Goal != 3 {
					t.Errorf("the week of 2026-11-16 is %+v, want met with 5 build days of a goal of 3", got)
				}
			},
		},
		{
			name: "30 days with 900 seconds each read 30 of the last 30",
			do: func(w *world) {
				for d := 0; d < 30; d++ {
					day := utc(2026, 10, 22, 0, 0).AddDate(0, 0, d)
					buildOn(w, day.Year(), day.Month(), day.Day())
				}
			},
			check: func(t *testing.T, w *world) {
				s := w.summary(utc(2026, 11, 20, 12, 0))
				if s.BuildDays != 30 || s.WindowDays != 30 || len(s.Days) != 30 {
					t.Errorf("%d build days of %d (%d days listed), want 30 of 30", s.BuildDays, s.WindowDays, len(s.Days))
				}
			},
		},
	}
	for _, engine := range storetest.Engines() {
		for _, tt := range tests {
			t.Run(string(engine)+"/"+tt.name, func(t *testing.T) {
				w := newWorld(t, engine, "")
				tt.do(w)
				tt.check(t, w)
			})
		}
	}
}

// TestSummary checks the summary's days: a stored day as decided, an open
// day with its credit so far by the as-of read, never rollups_daily's
// figure, and a day past its deadline with nothing uploaded as not counted.
func TestSummary(t *testing.T) {
	tests := []struct {
		name string
		do   func(w *world)
		at   time.Time
		day  string
		want Day
		// check, when set, checks more.
		check func(t *testing.T, w *world)
	}{
		{
			name: "an open day shows its credit so far by the as-of read, its pending minutes once their window has passed",
			do: func(w *world) {
				w.live(utc(2026, 11, 20, 10, 10), cat(
					full("d1", morning, 5, event.EvidenceA),
					withState(full("d1", morning.Add(5*time.Minute), 5, event.EvidenceB), event.StatePending),
				))
			},
			at:   utc(2026, 11, 20, 12, 0),
			day:  friday,
			want: Day{Date: friday, State: OpenDay, AMS: 300_000, BMS: 300_000, Deadline: deadline},
		},
		{
			name: "until its deadline the summary shows a Saturday sent on Monday and labeled backfilled as open with the as-of read's 0 seconds, though rollups_daily holds its minutes at level A",
			do: func(w *world) {
				for _, h := range []int{8, 12, 23} {
					w.batch("d1", utc(2026, 11, 21, h, 0))
				}
				w.live(utc(2026, 11, 23, 8, 0), full("d1", utc(2026, 11, 21, 10, 0), 15, event.EvidenceA))
				w.run(utc(2026, 11, 23, 8, 1))
			},
			at:   utc(2026, 11, 23, 12, 0),
			day:  "2026-11-21",
			want: Day{Date: "2026-11-21", State: OpenDay, Deadline: utc(2026, 11, 25, 0, 0)},
			check: func(t *testing.T, w *world) {
				if got := w.rolled("2026-11-21"); got.AMS != 900_000 {
					t.Errorf("rollups_daily holds %+v for 2026-11-21, want the device's 900 s at level A", got)
				}
			},
		},
		{
			name: "a build day shows as stored, with when it was decided",
			do: func(w *world) {
				buildOn(w, 2026, 11, 20)
			},
			at:   utc(2026, 11, 20, 12, 0),
			day:  friday,
			want: Day{Date: friday, State: BuildDay, AMS: 900_000, Deadline: deadline},
		},
		{
			name: "a day past its deadline with nothing uploaded didn't count",
			at:   utc(2026, 11, 25, 12, 0),
			day:  friday,
			want: Day{Date: friday, State: NotCounted, Deadline: deadline},
		},
		{
			name: "a day with nothing uploaded whose deadline hasn't passed is open",
			at:   utc(2026, 11, 22, 12, 0),
			day:  friday,
			want: Day{Date: friday, State: OpenDay, Deadline: deadline},
		},
	}
	for _, engine := range storetest.Engines() {
		for _, tt := range tests {
			t.Run(string(engine)+"/"+tt.name, func(t *testing.T) {
				w := newWorld(t, engine, "")
				if tt.do != nil {
					tt.do(w)
				}
				s := w.summary(tt.at)
				var got *Day
				for i := range s.Days {
					if s.Days[i].Date == tt.day {
						got = &s.Days[i]
					}
				}
				if got == nil {
					t.Fatalf("the summary at %v doesn't list %s", tt.at, tt.day)
				}
				if got.State != tt.want.State || got.AMS != tt.want.AMS || got.BMS != tt.want.BMS || !got.Deadline.Equal(tt.want.Deadline) {
					t.Errorf("%s is %+v, want %+v", tt.day, *got, tt.want)
				}
				if tt.check != nil {
					tt.check(t, w)
				}
				if (got.DecidedAt != nil) != (tt.want.State != OpenDay && tt.do != nil) {
					t.Errorf("%s decided at %v, want a time only for a stored day", tt.day, got.DecidedAt)
				}
				if s.TimeZone != "UTC" || s.Today != tt.at.Format(time.DateOnly) || s.WindowDays != 30 || len(s.Days) != 30 {
					t.Errorf("summary for %s in %s with %d of %d days, want today's date in UTC and 30 days", s.Today, s.TimeZone, len(s.Days), s.WindowDays)
				}
			})
		}
	}
}

// TestDeleteAccount checks the eraser's call (WP-SV13): DeleteAccount
// leaves no row of the account in any table this package writes.
func TestDeleteAccount(t *testing.T) {
	tables := []string{"build_days", "build_weeks", "build_goals", "zone_changes"}
	tests := []struct {
		name string
		do   func(w *world)
	}{
		{name: "an account with build days, a met week, freezes and a zone change has none of them after the erasure", do: func(w *world) {
			for d := 2; d <= 8; d++ {
				buildOn(w, 2026, 11, d)
			}
			w.setZone("Europe/Paris", utc(2026, 11, 9, 0, 0))
		}},
		{name: "an account with no build days erases cleanly", do: func(*world) {}},
	}
	for _, engine := range storetest.Engines() {
		for _, tt := range tests {
			t.Run(string(engine)+"/"+tt.name, func(t *testing.T) {
				w := newWorld(t, engine, "")
				tt.do(w)
				if err := w.d.Do(context.Background(), 1, func(ctx context.Context, tx writer.Tx) error {
					return DeleteAccount(ctx, tx, "u1")
				}); err != nil {
					t.Fatal(err)
				}
				for _, table := range tables {
					var n int
					if err := w.db.Read.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM `+table+` WHERE user_id = 'u1'`).Scan(&n); err != nil {
						t.Fatal(err)
					}
					if n != 0 {
						t.Errorf("%s holds %d rows of the account", table, n)
					}
				}
			})
		}
	}
}
