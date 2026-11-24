// SPDX-License-Identifier: AGPL-3.0-or-later

package rollup

import (
	"context"
	"math"
	"math/rand/v2"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/store"
	"github.com/some-du6e/pairtime/internal/server/store/storetest"
)

// creditFor is n of device's minutes from start on, Unix seconds, each in
// state and crediting 15 seconds in all four slots to project, at kind and
// level.
func creditFor(device, project, kind, level string, start int64, n int, state string) []step {
	s := sl(project, 15, kind, level)
	out := make([]step, 0, n)
	for i := range n {
		out = append(out, bk(device, start+int64(i)*60, 0, state, nil, s, s, s, s))
	}
	return out
}

// joinSteps is parts, one after another.
func joinSteps(parts ...[]step) []step {
	var out []step
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// withCap sets the day cap to limit milliseconds for the rest of the test.
func withCap(t *testing.T, limit int64) {
	t.Helper()
	was := dayCapMS
	dayCapMS = func() int64 { return limit }
	t.Cleanup(func() { dayCapMS = was })
}

// TestDayCap checks SM 5.3's DAY_CAP_S (WP-SV7): an account's local day, in
// the profile's time zone, keeps at most 12 hours of credit across its
// devices, counted after the merge, so a second device can't lift it, and
// the further credit in time order is X.day_cap.
func TestDayCap(t *testing.T) {
	// The account is in New York, where the local day 2026-11-20 runs from
	// 05:00 UTC to 05:00 UTC the next day. local is a time on it.
	newYork := step{zone: "America/New_York"}
	start := time.Date(2026, 11, 20, 5, 0, 0, 0, time.UTC).Unix()
	local := func(h, m int) int64 { return start + int64(h)*3600 + int64(m)*60 }
	const day = "2026-11-20"
	// held is a row of final credit at level, with heldMS withheld past
	// the cap.
	held := func(project, kind string, level int, finalMS, heldMS int64) Day {
		d := final(project, day, kind, level, finalMS)
		d.DayCapMS = heldMS
		return d
	}
	tests := []struct {
		name string
		// before, when set, runs first under beforeLimit, a cap in
		// milliseconds, where math.MaxInt64 stands for no cap, as before
		// migration 0012.
		before      []step
		beforeLimit int64
		// gone, when set, deletes the account's rollup_minutes rows of
		// minutes before it, Unix seconds, after before, as the retention
		// jobs delete them.
		gone int64
		// limit, when set, is the cap in milliseconds in place of DAY_CAP_S.
		limit int64
		steps []step
		want  []Day
	}{
		{
			name: "two devices crediting 8 hours each on one local day keep 12, and the last 4 in time order are X.day_cap, though the second device's hours span two UTC dates",
			steps: joinSteps([]step{newYork},
				creditFor("d1", "p1", "prompting", "A", local(0, 0), 480, "final"),
				creditFor("d2", "p2", "testing", "B", local(16, 0), 480, "final")),
			want: []Day{
				final("d1/p1", day, "prompting", 0, 8*3_600_000),
				held("d2/p2", "testing", 1, 4*3_600_000, 4*3_600_000),
			},
		},
		{
			name:  "a revision that takes the credit from an early minute moves the cut a minute later",
			limit: 600_000,
			steps: joinSteps([]step{newYork},
				creditFor("d1", "p1", "prompting", "A", local(9, 0), 8, "final"),
				creditFor("d2", "p2", "testing", "B", local(9, 8), 8, "final"),
				[]step{bk("d1", local(9, 1), 1, "final", nil)}),
			want: []Day{
				final("d1/p1", day, "prompting", 0, 420_000),
				held("d2/p2", "testing", 1, 180_000, 300_000),
			},
		},
		{
			name:  "two devices crediting the same minutes keep one of them, so the cap counts the merged credit",
			limit: 600_000,
			steps: joinSteps([]step{newYork},
				creditFor("d1", "p1", "prompting", "A", local(9, 0), 8, "final"),
				creditFor("d2", "p1", "prompting", "A", local(9, 0), 8, "final")),
			want: []Day{final("d1/p1", day, "prompting", 0, 480_000)},
		},
		{
			name:  "a pending minute the cut falls inside keeps its cut when the server settles it",
			limit: 570_000,
			steps: joinSteps([]step{newYork},
				creditFor("d1", "p1", "prompting", "A", local(9, 0), 9, "final"),
				creditFor("d2", "p2", "testing", "B", local(9, 9), 1, "pending"),
				[]step{settleAt(time.Unix(local(9, 10), 0).Add(PendingWindow()).Add(time.Millisecond))}),
			want: []Day{
				final("d1/p1", day, "prompting", 0, 540_000),
				held("d2/p2", "testing", 1, 30_000, 30_000),
			},
		},
		{
			name:  "a day that revisions bring back under the cap withholds nothing",
			limit: 600_000,
			steps: joinSteps([]step{newYork},
				creditFor("d1", "p1", "prompting", "A", local(9, 0), 8, "final"),
				creditFor("d2", "p2", "testing", "B", local(9, 8), 8, "final"),
				[]step{
					bk("d2", local(9, 10), 1, "final", nil), bk("d2", local(9, 11), 1, "final", nil), bk("d2", local(9, 12), 1, "final", nil),
					bk("d2", local(9, 13), 1, "final", nil), bk("d2", local(9, 14), 1, "final", nil), bk("d2", local(9, 15), 1, "final", nil),
				}),
			want: []Day{
				final("d1/p1", day, "prompting", 0, 480_000),
				final("d2/p2", day, "testing", 1, 120_000),
			},
		},
		{
			name:        "a day stored over the cap before migration 0012 is cut whole at its next change, though that change is past the cap",
			before:      joinSteps([]step{newYork}, creditFor("d1", "p1", "prompting", "A", local(9, 0), 13, "final")),
			beforeLimit: math.MaxInt64,
			limit:       600_000,
			steps:       []step{bk("d1", local(9, 10), 1, "final", nil)},
			want:        []Day{held("d1/p1", "prompting", 0, 600_000, 120_000)},
		},
		{
			name:        "a day cut under a lower cap is cut again whole under a higher one",
			before:      joinSteps([]step{newYork}, creditFor("d1", "p1", "prompting", "A", local(9, 0), 8, "final")),
			beforeLimit: 300_000,
			limit:       600_000,
			steps:       creditFor("d1", "p1", "prompting", "A", local(9, 8), 1, "final"),
			want:        []Day{final("d1/p1", day, "prompting", 0, 540_000)},
		},
		{
			name:        "a day stored over the cap whose first minutes are gone keeps its rows, and a change adds to them without holding anything back",
			before:      joinSteps([]step{newYork}, creditFor("d1", "p1", "prompting", "A", local(9, 0), 13, "final")),
			beforeLimit: math.MaxInt64,
			gone:        local(9, 2),
			limit:       600_000,
			steps:       creditFor("d1", "p1", "prompting", "A", local(9, 13), 1, "final"),
			want:        []Day{final("d1/p1", day, "prompting", 0, 840_000)},
		},
		{
			name:  "each local day has a cap of its own",
			limit: 600_000,
			steps: joinSteps([]step{newYork},
				creditFor("d1", "p1", "prompting", "A", local(9, 0), 8, "final"),
				creditFor("d1", "p1", "prompting", "A", local(24+9, 0), 8, "final")),
			want: []Day{
				final("d1/p1", day, "prompting", 0, 480_000),
				final("d1/p1", "2026-11-21", "prompting", 0, 480_000),
			},
		},
	}
	for _, engine := range storetest.Engines() {
		for _, tt := range tests {
			t.Run(string(engine)+"/"+tt.name, func(t *testing.T) {
				db := newDB(t, engine, "u1")
				if tt.before != nil {
					withCap(t, tt.beforeLimit)
					if err := run(t, db, "u1", tt.before); err != nil {
						t.Fatal(err)
					}
				}
				if tt.gone != 0 {
					if _, err := db.Write.ExecContext(context.Background(), `DELETE FROM rollup_minutes WHERE user_id = 'u1' AND minute_ms < $1`, tt.gone*1000); err != nil {
						t.Fatal(err)
					}
				}
				if tt.limit > 0 {
					withCap(t, tt.limit)
				}
				if err := run(t, db, "u1", tt.steps); err != nil {
					t.Fatal(err)
				}
				if got := rows(t, db, "u1"); !reflect.DeepEqual(got, tt.want) {
					t.Errorf("rows:\n got %+v\nwant %+v", got, tt.want)
				}
			})
		}
	}
}

// TestDayCapReplayConverges is WP-SV7's first check on a day over the cap:
// revisions applied in any order cut the day in the same place, by the
// minutes' own time order.
func TestDayCapReplayConverges(t *testing.T) {
	withCap(t, 300_000)
	// Three devices, eight minutes, two revisions each, with credit, levels
	// and states drawn from a fixed seed.
	gen := rand.New(rand.NewPCG(13, 17))
	projects := []string{"p1", "p2"}
	kinds := []string{"prompting", "testing"}
	var revs []step
	for m := range int64(8) {
		for _, dev := range []string{"d1", "d2", "d3"} {
			for rev := range 2 {
				var slots []Slot
				for range slotsPerMinute {
					s := Slot{}
					if cr := float64(gen.IntN(31)) / 2; cr > 0 {
						s = sl(projects[gen.IntN(2)], cr, kinds[gen.IntN(2)], Levels[gen.IntN(3)])
					}
					slots = append(slots, s)
				}
				state := "pending"
				if rev == 1 {
					state = "final"
				}
				revs = append(revs, bk(dev, base+m*60, rev, state, nil, slots...))
			}
		}
	}
	replay := func(t *testing.T, engine store.Engine, order []step) []Day {
		db := newDB(t, engine, "u1")
		if err := run(t, db, "u1", order); err != nil {
			t.Fatal(err)
		}
		return rows(t, db, "u1")
	}
	for _, engine := range storetest.Engines() {
		want := replay(t, engine, revs)
		if !slices.ContainsFunc(want, func(d Day) bool { return d.DayCapMS > 0 }) {
			t.Fatalf("the revisions in order left %+v; the test needs a day over the cap", want)
		}
		for seed := range uint64(5) {
			t.Run(string(engine)+"/shuffle "+string(rune('a'+seed))+" cuts the day where applying the revisions in order does", func(t *testing.T) {
				order := slices.Clone(revs)
				r := rand.New(rand.NewPCG(seed, 23))
				r.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })
				if got := replay(t, engine, order); !reflect.DeepEqual(got, want) {
					t.Errorf("shuffled:\n got %+v\nwant %+v", got, want)
				}
			})
		}
	}
}
