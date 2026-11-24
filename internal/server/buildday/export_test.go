// SPDX-License-Identifier: AGPL-3.0-or-later

package buildday

import (
	"context"
	"maps"
	"testing"

	"github.com/some-du6e/pairtime/internal/server/store/storetest"
	"github.com/some-du6e/pairtime/pkg/event"
)

// rowCounts counts the rows of every table Export and DeleteAccount cover.
func rowCounts(t *testing.T, w *world) map[string]int {
	t.Helper()
	out := make(map[string]int)
	for _, table := range []string{"build_days", "build_weeks", "build_goals", "zone_changes", "users"} {
		var n int
		if err := w.db.Read.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM `+table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		out[table] = n
	}
	return out
}

// TestExport checks the export's read (DESIGN 8.9; WP-SV13): it holds every
// row this package keeps for the account and changes nothing.
func TestExport(t *testing.T) {
	tests := []struct {
		name  string
		do    func(w *world)
		check func(t *testing.T, got Exported)
	}{
		{
			name: "an export holds the account's stored days, decided weeks, weekly goal, freezes and zone changes",
			do: func(w *world) {
				for d := 2; d <= 8; d++ {
					buildOn(w, 2026, 11, d)
				}
				w.live(utc(2026, 11, 9, 10, 10), full("d1", utc(2026, 11, 9, 10, 0), 10, event.EvidenceA))
				w.run(utc(2026, 11, 13, 0, 0))
				w.setZone("Europe/Paris", utc(2026, 11, 13, 1, 0))
			},
			check: func(t *testing.T, got Exported) {
				counted := 0
				for _, d := range got.Days {
					if d.Counted {
						counted++
					}
				}
				if len(got.Days) != 8 || counted != 7 {
					t.Errorf("%d days, %d of them build days; want 8 and 7", len(got.Days), counted)
				}
				if last := got.Days[len(got.Days)-1]; last.Date != "2026-11-09" || last.Counted || last.AMS != 600_000 || !last.DecidedAt.Equal(utc(2026, 11, 13, 0, 0)) {
					t.Errorf("2026-11-09 exports as %+v, want 600 s at level A decided not to count at its deadline", last)
				}
				if len(got.Weeks) != 1 || got.Weeks[0].Start != "2026-11-02" || got.Weeks[0].Result != Met || got.Weeks[0].BuildDays != 3 {
					t.Errorf("weeks %+v, want the week of 2026-11-02, met at its third build day", got.Weeks)
				}
				if g := got.Goal; g == nil || g.WeeklyGoal != 3 || g.FreezesHeld != 1 || g.FreezesEarned != 1 || g.NextWeek != "2026-11-09" {
					t.Errorf("goal %+v, want a goal of 3, one freeze held and earned, and the week of 2026-11-09 next", g)
				}
				if len(got.ZoneChanges) != 1 || got.ZoneChanges[0].Before != "UTC" || !got.ZoneChanges[0].At.Equal(utc(2026, 11, 13, 1, 0)) {
					t.Errorf("zone changes %+v, want the change from UTC at 2026-11-13T01:00Z", got.ZoneChanges)
				}
			},
		},
		{
			name: "an account with nothing stored exports nothing",
			do:   func(*world) {},
			check: func(t *testing.T, got Exported) {
				if len(got.Days)+len(got.Weeks)+len(got.ZoneChanges) != 0 || got.Goal != nil {
					t.Errorf("export %+v, want nothing", got)
				}
			},
		},
	}
	for _, engine := range storetest.Engines() {
		for _, tt := range tests {
			t.Run(string(engine)+"/"+tt.name, func(t *testing.T) {
				w := newWorld(t, engine, "")
				tt.do(w)
				before := rowCounts(t, w)
				got, err := Export(context.Background(), w.db.Read, "u1")
				if err != nil {
					t.Fatal(err)
				}
				tt.check(t, got)
				if after := rowCounts(t, w); !maps.Equal(before, after) {
					t.Errorf("the export changed the tables' rows from %v to %v", before, after)
				}
			})
		}
	}
}
