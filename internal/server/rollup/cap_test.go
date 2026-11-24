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
			name:        "a day stored over the cap whose first minutes are gone can't be cut again, so a change holds back the credit it adds",
			before:      joinSteps([]step{newYork}, creditFor("d1", "p1", "prompting", "A", local(9, 0), 13, "final")),
			beforeLimit: math.MaxInt64,
			gone:        local(9, 2),
			limit:       600_000,
			steps:       creditFor("d1", "p1", "prompting", "A", local(9, 13), 1, "final"),
			want:        []Day{held("d1/p1", "prompting", 0, 780_000, 60_000)},
		},
		{
			name:        "on a day cut under a lower cap whose first minute is gone, a revision takes the credit it removes out of what is held back, never below zero",
			before:      joinSteps([]step{newYork}, creditFor("d1", "p1", "prompting", "A", local(9, 0), 5, "final"), creditFor("d1", "p2", "testing", "B", local(9, 5), 3, "final")),
			beforeLimit: 300_000,
			gone:        local(9, 1),
			limit:       600_000,
			steps:       []step{bk("d1", local(9, 5), 1, "final", nil)},
			want: []Day{
				final("d1/p1", day, "prompting", 0, 300_000),
				{ProjectID: "d1/p2", Day: day, Kind: "testing", DayCapMS: 120_000},
			},
		},
		{
			name:        "rows keep changed aren't taken for the day's cut: a later revision of the same day takes no column below zero",
			before:      joinSteps([]step{newYork}, creditFor("d1", "p1", "prompting", "A", local(9, 0), 3, "final")),
			beforeLimit: math.MaxInt64,
			gone:        local(9, 1),
			limit:       120_000,
			steps: []step{
				func() step {
					b := sl("p1", 15, "prompting", "B")
					return bk("d1", local(9, 1), 1, "final", nil, b, b, b, b)
				}(),
				bk("d1", local(9, 1), 2, "final", nil),
			},
			want: []Day{final("d1/p1", day, "prompting", 0, 120_000)},
		},
		{
			name:        "a legacy day whose first minute is gone, with a minute moved to another project and then emptied, keeps the cap and takes no column below zero",
			before:      joinSteps([]step{newYork}, creditFor("d1", "p1", "prompting", "A", local(9, 0), 11, "final")),
			beforeLimit: math.MaxInt64,
			gone:        local(9, 1),
			limit:       600_000,
			steps: []step{
				func() step {
					b := sl("p2", 15, "testing", "B")
					return bk("d1", local(9, 1), 1, "final", nil, b, b, b, b)
				}(),
				bk("d1", local(9, 1), 2, "final", nil),
			},
			want: []Day{final("d1/p1", day, "prompting", 0, 600_000)},
		},
		{
			name:        "a legacy day that keep brought back under the cap takes a later revision without any column going below zero",
			before:      joinSteps([]step{newYork}, creditFor("d1", "p1", "prompting", "A", local(9, 0), 3, "final")),
			beforeLimit: math.MaxInt64,
			gone:        local(9, 1),
			limit:       120_000,
			steps: []step{
				func() step {
					b := sl("p1", 15, "prompting", "B")
					return bk("d1", local(9, 1), 1, "final", nil, b, b, b, b)
				}(),
				bk("d1", local(9, 2), 1, "final", nil),
				bk("d1", local(9, 1), 2, "final", nil),
			},
			want: []Day{final("d1/p1", day, "prompting", 0, 120_000)},
		},
		{
			name:  "a slot the cap falls inside is split there",
			limit: 607_500,
			steps: joinSteps([]step{newYork},
				creditFor("d1", "p1", "prompting", "A", local(9, 0), 10, "final"),
				creditFor("d2", "p2", "testing", "B", local(9, 10), 1, "final")),
			want: []Day{
				final("d1/p1", day, "prompting", 0, 600_000),
				held("d2/p2", "testing", 1, 7_500, 52_500),
			},
		},
		{
			name:  "the agent's time on its own stays as it is on a minute past the cap and through its revision",
			limit: 600_000,
			steps: joinSteps([]step{newYork},
				creditFor("d1", "p1", "prompting", "A", local(9, 0), 10, "final"),
				func() []step {
					s := sl("p2", 15, "testing", "B")
					return []step{
						bk("d2", local(9, 10), 0, "final", ao("p2", 20), s, s),
						bk("d2", local(9, 10), 1, "final", ao("p2", 25), s, s),
					}
				}()),
			want: []Day{
				final("d1/p1", day, "prompting", 0, 600_000),
				agent("d2/p2", day, 25_000, 0),
				held("d2/p2", "testing", 1, 0, 30_000),
			},
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
	// Three devices, eight minutes, two revisions each, with credit, levels,
	// states and the agent's time on its own drawn from a fixed seed.
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
				var agentOnly map[string]float64
				if gen.IntN(3) == 0 {
					agentOnly = ao(projects[gen.IntN(2)], float64(gen.IntN(121))/2)
				}
				revs = append(revs, bk(dev, base+m*60, rev, state, agentOnly, slots...))
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

// TestDayCapSumOfBuckets is WP-SV7's third check on days over the cap: on
// random revisions applied in a random order, each project and kind's
// credit plus what the cap held back is the sum of its merged buckets, and
// each day keeps the smaller of its merged credit and the cap. The merge's
// rule (SM 3.4) is worked out here again, apart from the package's.
func TestDayCapSumOfBuckets(t *testing.T) {
	const limit = 400_000
	withCap(t, limit)
	gen := rand.New(rand.NewPCG(29, 31))
	projects := []string{"p1", "p2"}
	kinds := []string{"prompting", "testing"}
	devices := []string{"d1", "d2", "d3"}
	// Two UTC days of twelve minutes, each device's minute in two
	// revisions, the second final.
	var revs []step
	latest := map[string]map[int64]Bucket{}
	for _, d := range devices {
		latest[d] = map[int64]Bucket{}
	}
	for _, dayStart := range []int64{base, base + 86_400} {
		for m := range int64(12) {
			for _, dev := range devices {
				for rev := range 2 {
					var slots []Slot
					for range slotsPerMinute {
						s := Slot{}
						if cr := float64(gen.IntN(31)) / 2; cr > 0 && gen.IntN(4) > 0 {
							s = sl(projects[gen.IntN(2)], cr, kinds[gen.IntN(2)], Levels[gen.IntN(3)])
						}
						slots = append(slots, s)
					}
					state := "pending"
					if rev == 1 {
						state = "final"
					}
					st := bk(dev, dayStart+m*60, rev, state, nil, slots...)
					revs = append(revs, st)
					latest[dev][st.bucket.Minute] = st.bucket
				}
			}
		}
	}
	// What each day, project and kind sums to once the devices merge: in
	// each slot the most credit wins, then level A, then the lower device.
	type key struct{ day, project, kind string }
	want := map[key]int64{}
	dayTotal := map[string]int64{}
	minutes := map[int64]bool{}
	for _, byMinute := range latest {
		for m := range byMinute {
			minutes[m] = true
		}
	}
	for m := range minutes {
		day := time.Unix(m, 0).UTC().Format(time.DateOnly)
		for i := range slotsPerMinute {
			var win Slot
			winDev := ""
			for _, dev := range devices {
				b, ok := latest[dev][m]
				if !ok || i >= len(b.Slots) || b.Slots[i].CreditedS == 0 {
					continue
				}
				s := b.Slots[i]
				better := winDev == "" || s.CreditedS > win.CreditedS ||
					s.CreditedS == win.CreditedS && (s.Level == "A") != (win.Level == "A") && s.Level == "A" ||
					s.CreditedS == win.CreditedS && (s.Level == "A") == (win.Level == "A") && dev < winDev
				if better {
					win, winDev = s, dev
				}
			}
			if winDev != "" {
				ms := int64(math.Round(win.CreditedS * 1000))
				want[key{day, winDev + "/" + win.Project, win.Kind}] += ms
				dayTotal[day] += ms
			}
		}
	}
	over := false
	for _, total := range dayTotal {
		over = over || total > limit
	}
	if !over {
		t.Fatalf("no day's merged credit %v is over the cap of %d; the test needs one", dayTotal, limit)
	}
	for _, engine := range storetest.Engines() {
		t.Run(string(engine)+"/credit plus what the cap held back is the merged buckets, and each day keeps at most the cap", func(t *testing.T) {
			order := slices.Clone(revs)
			rand.New(rand.NewPCG(37, 41)).Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })
			db := newDB(t, engine, "u1")
			if err := run(t, db, "u1", order); err != nil {
				t.Fatal(err)
			}
			got := map[key]int64{}
			kept := map[string]int64{}
			for _, d := range rows(t, db, "u1") {
				if d.Kind == "" {
					continue
				}
				var credit int64
				for i := range Levels {
					credit += d.FinalMS[i] + d.PendingMS[i]
				}
				got[key{d.Day, d.ProjectID, d.Kind}] += credit + d.DayCapMS
				kept[d.Day] += credit
			}
			for k, ms := range want {
				if got[k] != ms {
					t.Errorf("%+v holds %d ms, credited and held back, want the merged buckets' %d", k, got[k], ms)
				}
			}
			for k, ms := range got {
				if _, ok := want[k]; !ok && ms != 0 {
					t.Errorf("%+v holds %d ms, which no merged bucket credits", k, ms)
				}
			}
			for day, total := range dayTotal {
				if kept[day] != min(total, limit) {
					t.Errorf("%s keeps %d ms, want the smaller of its %d and the cap's %d", day, kept[day], total, int64(limit))
				}
			}
		})
	}
}

// TestRecutDays checks the settle job's one cut of the days stored over the
// cap before migration 0012 (RecutDays): each day of the last 182 whose rows
// aren't a cut is cut again whole while rollup_minutes holds all of it, and
// a run with nothing to cut makes no transaction.
func TestRecutDays(t *testing.T) {
	newYork := step{zone: "America/New_York"}
	start := time.Date(2026, 11, 20, 5, 0, 0, 0, time.UTC).Unix()
	local := func(h, m int) int64 { return start + int64(h)*3600 + int64(m)*60 }
	const day = "2026-11-20"
	now := time.Unix(local(36, 0), 0)
	// Thirteen minutes stored with no cap, and agent time in a fourteenth.
	legacy := joinSteps([]step{newYork},
		creditFor("d1", "p1", "prompting", "A", local(9, 0), 13, "final"),
		[]step{bk("d1", local(9, 13), 0, "final", ao("p1", 20))})
	tests := []struct {
		name   string
		before []step
		// gone, when set, deletes rollup_minutes rows before it, Unix seconds.
		gone     int64
		runs     int
		wantCuts []int
		wantOps  int
		want     []Day
	}{
		{
			name:     "a day stored over the cap before 0012 is cut again whole in one transaction, and its agent time stays",
			before:   legacy,
			runs:     1,
			wantCuts: []int{1},
			wantOps:  1,
			want:     []Day{agent("d1/p1", day, 20_000, 0), {ProjectID: "d1/p1", Day: day, Kind: "prompting", FinalMS: [3]int64{600_000}, DayCapMS: 180_000}},
		},
		{
			name:     "a second run finds the day cut and makes no transaction",
			before:   legacy,
			runs:     2,
			wantCuts: []int{1, 0},
			wantOps:  1,
			want:     []Day{agent("d1/p1", day, 20_000, 0), {ProjectID: "d1/p1", Day: day, Kind: "prompting", FinalMS: [3]int64{600_000}, DayCapMS: 180_000}},
		},
		{
			name:     "a day whose first minutes are gone keeps its rows, and the run makes no transaction",
			before:   legacy,
			gone:     local(9, 2),
			runs:     1,
			wantCuts: []int{0},
			wantOps:  0,
			want:     []Day{agent("d1/p1", day, 20_000, 0), final("d1/p1", day, "prompting", 0, 780_000)},
		},
		{
			name:     "a day within the cap makes no transaction",
			before:   joinSteps([]step{newYork}, creditFor("d1", "p1", "prompting", "A", local(9, 0), 5, "final")),
			runs:     1,
			wantCuts: []int{0},
			wantOps:  0,
			want:     []Day{final("d1/p1", day, "prompting", 0, 300_000)},
		},
	}
	for _, engine := range storetest.Engines() {
		for _, tt := range tests {
			t.Run(string(engine)+"/"+tt.name, func(t *testing.T) {
				db := newDB(t, engine, "u1")
				withCap(t, math.MaxInt64)
				if err := run(t, db, "u1", tt.before); err != nil {
					t.Fatal(err)
				}
				if tt.gone != 0 {
					if _, err := db.Write.ExecContext(context.Background(), `DELETE FROM rollup_minutes WHERE user_id = 'u1' AND minute_ms < $1`, tt.gone*1000); err != nil {
						t.Fatal(err)
					}
				}
				withCap(t, 600_000)
				ops := 0
				var cuts []int
				for range tt.runs {
					n, err := RecutDays(context.Background(), txDoer{db: db, ops: &ops}, db.Read, now)
					if err != nil {
						t.Fatal(err)
					}
					cuts = append(cuts, n)
				}
				if !slices.Equal(cuts, tt.wantCuts) || ops != tt.wantOps {
					t.Errorf("the runs cut %v days in %d transactions, want %v in %d", cuts, ops, tt.wantCuts, tt.wantOps)
				}
				if got := rows(t, db, "u1"); !reflect.DeepEqual(got, tt.want) {
					t.Errorf("rows:\n got %+v\nwant %+v", got, tt.want)
				}
			})
		}
	}
}

// TestRunSettleRecuts checks that the settle job's first run cuts again the
// days stored over the cap before migration 0012 (RecutDays), and tells the
// live stream whose day it cut.
func TestRunSettleRecuts(t *testing.T) {
	t.Run("the settle job's first run cuts a day stored over the cap before 0012, and tells changed its account once the cut committed", func(t *testing.T) {
		db := newDB(t, store.SQLite, "u1")
		withCap(t, math.MaxInt64)
		if err := run(t, db, "u1", creditFor("d1", "p1", "prompting", "A", base, 13, "final")); err != nil {
			t.Fatal(err)
		}
		withCap(t, 600_000)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		clock := func() time.Time { return time.Unix(base, 0).Add(24 * time.Hour) }
		var changed []string
		done := make(chan struct{})
		go func() {
			defer close(done)
			RunSettle(ctx, txDoer{db: db}, db.Read, time.Hour, clock, func(_ int, err error) {
				if err != nil {
					t.Error(err)
				}
				cancel()
			}, func(account string) {
				changed = append(changed, account)
			})
		}()
		select {
		case <-done:
		case <-time.After(30 * time.Second):
			t.Fatal("RunSettle didn't stop after its context ended")
		}
		want := []Day{{ProjectID: "d1/p1", Day: "2026-11-20", Kind: "prompting", FinalMS: [3]int64{600_000}, DayCapMS: 180_000}}
		if got := rows(t, db, "u1"); !reflect.DeepEqual(got, want) {
			t.Errorf("rows:\n got %+v\nwant %+v", got, want)
		}
		// The minutes are final, so only the cut tells it.
		if !slices.Equal(changed, []string{"u1"}) {
			t.Errorf("changed heard %q, want the account whose day was cut, once", changed)
		}
	})
}
