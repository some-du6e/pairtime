// SPDX-License-Identifier: AGPL-3.0-or-later

package dashboard

import (
	"context"
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/migrations"
	"github.com/some-du6e/pairtime/internal/server/rollup"
	"github.com/some-du6e/pairtime/internal/server/store"
	"github.com/some-du6e/pairtime/internal/server/store/storetest"
)

// rollupsDB returns a migrated database with account "u1" in zone.
func rollupsDB(t *testing.T, engine store.Engine, zone string) *store.DB {
	t.Helper()
	ctx := context.Background()
	db := storetest.Open(t, engine)
	ms, err := migrations.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrations.Apply(ctx, db, ms, time.Now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write.ExecContext(ctx, `INSERT INTO users (id, hca_sub, verification_status, created_at_ms, updated_at_ms, signed_in_at_ms, tz)
VALUES ('u1', 'sub-u1', 'verified', 0, 0, 0, $1)`, zone); err != nil {
		t.Fatal(err)
	}
	return db
}

// link gives the "device/key" pairs one project id, as project
// registration (WP-SV10) will for the keys of one repo.
func link(t *testing.T, db *store.DB, id string, pairs ...string) {
	t.Helper()
	for _, pair := range pairs {
		device, key, _ := strings.Cut(pair, "/")
		if _, err := db.Write.ExecContext(context.Background(), `INSERT INTO rollup_projects (user_id, device_id, project_key, project_id, last_minute_ms)
VALUES ('u1', $1, $2, $3, 0)`, device, key, id); err != nil {
			t.Fatal(err)
		}
	}
}

// applyAll applies each bucket in a transaction of its own, as ingest
// does.
func applyAll(t *testing.T, db *store.DB, bs []rollup.Bucket) {
	t.Helper()
	ctx := context.Background()
	for _, b := range bs {
		tx, err := db.Write.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := rollup.Apply(ctx, tx, nil, b); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
}

// TestRollupsSumOfBuckets is WP-SV7's third check: the dashboard's totals
// equal the sum of the buckets stored, final apart from pending. Each
// device credits its own slots, so no minute merges anything away, and
// the devices' keys p1 and p2 are linked as two projects, as registration
// will link a repo's keys, so the agent's time merges across them.
func TestRollupsSumOfBuckets(t *testing.T) {
	now := time.Date(2026, 11, 20, 18, 0, 0, 0, time.UTC)
	gen := rand.New(rand.NewPCG(3, 5))
	kinds := []string{"prompting", "reading_output", "testing", "hand_edit"}
	var buckets []rollup.Bucket
	var final, pending, agentTotal float64
	monthStart := now.AddDate(0, 0, -(MonthDays - 1)).Truncate(24 * time.Hour).Unix()
	for i := range 300 {
		// Minutes spread over the 30 days, two devices each crediting
		// slots of its own, and a minute drawn twice gets a new revision.
		// Only d1 reports agent_only, so its time is the minute's, less
		// what both devices credited in that project.
		minute := monthStart + int64(gen.IntN(MonthDays*1440))*60
		for _, dev := range []struct {
			id  string
			own [2]int
		}{{id: "d1", own: [2]int{0, 1}}, {id: "d2", own: [2]int{2, 3}}} {
			b := rollup.Bucket{Account: "u1", Device: dev.id, Minute: minute, Rev: i, State: "final", Slots: make([]rollup.Slot, 4)}
			if minute >= now.Unix()-900 {
				b.State = "pending"
			}
			if dev.id == "d1" {
				b.AgentOnly = map[string]float64{[]string{"p1", "p2"}[gen.IntN(2)]: float64(gen.IntN(121)) / 2}
			}
			for _, s := range dev.own {
				cr := float64(gen.IntN(31)) / 2
				if cr > 0 {
					b.Slots[s] = rollup.Slot{Project: []string{"p1", "p2"}[gen.IntN(2)], CreditedS: cr, Kind: kinds[gen.IntN(4)], Level: rollup.Levels[gen.IntN(3)]}
				}
			}
			buckets = append(buckets, b)
		}
	}
	// Only each device's latest revision of a minute counts, and a minute
	// drawn twice has two.
	latest := make(map[[2]any]rollup.Bucket)
	for _, b := range buckets {
		latest[[2]any{b.Device, b.Minute}] = b
	}
	credited := make(map[[2]any]float64) // minute and project to seconds
	for _, b := range latest {
		for _, s := range b.Slots {
			if b.State == "pending" {
				pending += s.CreditedS
			} else {
				final += s.CreditedS
			}
			credited[[2]any{b.Minute, s.Project}] += s.CreditedS
		}
	}
	// A second credited to a project isn't the agent's alone there (SM 2.6).
	for _, b := range latest {
		for p, a := range b.AgentOnly {
			agentTotal += max(0, min(a, 60-credited[[2]any{b.Minute, p}]))
		}
	}
	for _, engine := range storetest.Engines() {
		t.Run(string(engine)+"/the month's totals are the sum of the latest revisions' credit, final apart from pending", func(t *testing.T) {
			db := rollupsDB(t, engine, "UTC")
			link(t, db, "P1", "d1/p1", "d2/p1")
			link(t, db, "P2", "d1/p2", "d2/p2")
			applyAll(t, db, buckets)
			v, err := Rollups{DB: db.Read}.View(context.Background(), "u1", now)
			if err != nil {
				t.Fatal(err)
			}
			if v.Month.You.FinalS != final || v.Month.You.PendingS != pending {
				t.Errorf("month %+v, want %v final and %v pending", v.Month.You, final, pending)
			}
			var levels, kinds float64
			for _, l := range v.Month.Levels {
				levels += l.Total()
			}
			for _, k := range v.Month.Kinds {
				kinds += k.Total()
			}
			if levels != final+pending || kinds != final+pending {
				t.Errorf("levels add up to %v and kinds to %v, want %v", levels, kinds, final+pending)
			}
			if v.Month.Agent.Total() != agentTotal {
				t.Errorf("agent %v, want %v", v.Month.Agent.Total(), agentTotal)
			}
		})
	}
}

func TestRollupsView(t *testing.T) {
	// 2027-09-06 03:30 UTC is 2027-09-06 00:30 in Santiago, the day after
	// its midnight that doesn't exist.
	santiagoNow := time.Date(2027, 9, 6, 3, 30, 0, 0, time.UTC)
	tests := []struct {
		name    string
		zone    string
		now     time.Time
		buckets []rollup.Bucket
		check   func(*testing.T, View)
	}{
		{
			name: "every real dashboard says a repo used on two computers shows as two projects, and does show it so",
			zone: "UTC",
			now:  time.Date(2026, 11, 20, 18, 0, 0, 0, time.UTC),
			buckets: []rollup.Bucket{
				{Account: "u1", Device: "d1", Minute: time.Date(2026, 11, 20, 17, 0, 0, 0, time.UTC).Unix(), State: "final",
					Slots: []rollup.Slot{{Project: "g_laptop_key", CreditedS: 15, Kind: "prompting", Level: "A"}}},
				{Account: "u1", Device: "d2", Minute: time.Date(2026, 11, 20, 17, 1, 0, 0, time.UTC).Unix(), State: "final",
					Slots: []rollup.Slot{{Project: "g_desktop_key", CreditedS: 15, Kind: "prompting", Level: "A"}}},
			},
			check: func(t *testing.T, v View) {
				if !v.SplitByDevice || len(v.Projects) != 2 {
					t.Errorf("split by device %v, projects %+v; want split and two projects", v.SplitByDevice, v.Projects)
				}
			},
		},
		{
			name: "an account with no rollups gets 30 empty days and no projects, for the page's empty state",
			zone: "UTC",
			now:  time.Date(2026, 11, 20, 18, 0, 0, 0, time.UTC),
			check: func(t *testing.T, v View) {
				if len(v.Days) != MonthDays || len(v.Projects) != 0 || v.Month.You.Total() != 0 || v.Sample {
					t.Errorf("view %+v, want 30 empty days, no projects and no sample", v)
				}
			},
		},
		{
			name: "today is the local date in the account's zone, and the zone is in the view",
			zone: "America/Santiago",
			now:  santiagoNow,
			buckets: []rollup.Bucket{
				{Account: "u1", Device: "d1", Minute: 1820197800, State: "final", Slots: []rollup.Slot{{Project: "p_9f2c", CreditedS: 15, Kind: "prompting", Level: "A"}}},
			},
			check: func(t *testing.T, v View) {
				if v.Today != "2027-09-06" || v.TZ != "America/Santiago" {
					t.Errorf("today %s in %s, want 2027-09-06 in America/Santiago", v.Today, v.TZ)
				}
				// The minute at 23:30 on the 5th is yesterday's.
				if got := v.Days[MonthDays-2]; got.Date != "2027-09-05" || got.You.FinalS != 15 {
					t.Errorf("yesterday %+v, want 2027-09-05 with 15 s", got)
				}
			},
		},
		{
			name: "a project is called by its device's key, cut short, since the server never learns its name",
			zone: "UTC",
			now:  time.Date(2026, 11, 20, 18, 0, 0, 0, time.UTC),
			buckets: []rollup.Bucket{
				{Account: "u1", Device: "d1", Minute: time.Date(2026, 11, 20, 17, 0, 0, 0, time.UTC).Unix(), State: "final", AgentOnly: map[string]float64{"p_0123456789abcdef": 30},
					Slots: []rollup.Slot{{Project: "p_0123456789abcdef", CreditedS: 15, Kind: "prompting", Level: "A"}}},
			},
			check: func(t *testing.T, v View) {
				if len(v.Projects) != 1 || v.Projects[0].Name != "p_0123456789…" || v.Agent != AgentName {
					t.Fatalf("projects %+v and agent %q", v.Projects, v.Agent)
				}
				// The project's id is the account's, not the device's key.
				if p := v.Projects[0]; p.ID == "p_0123456789abcdef" || !reflect.DeepEqual(p.Keys, []string{"p_0123456789abcdef"}) {
					t.Errorf("project %+v, want an id of its own and the device's key", p)
				}
				if v.Week.Agent.FinalS != 30 {
					t.Errorf("agent %+v, want 30 s", v.Week.Agent)
				}
			},
		},
		{
			name: "pending minutes stay apart from final ones",
			zone: "UTC",
			now:  time.Date(2026, 11, 20, 18, 0, 0, 0, time.UTC),
			buckets: []rollup.Bucket{
				{Account: "u1", Device: "d1", Minute: time.Date(2026, 11, 20, 17, 55, 0, 0, time.UTC).Unix(), State: "pending",
					Slots: []rollup.Slot{{Project: "p1", CreditedS: 12.5, Kind: "testing", Level: "B"}}},
			},
			check: func(t *testing.T, v View) {
				if got := v.Days[MonthDays-1].You; got != (Time{PendingS: 12.5}) {
					t.Errorf("today %+v, want 12.5 s pending", got)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := rollupsDB(t, store.SQLite, tt.zone)
			applyAll(t, db, tt.buckets)
			v, err := Rollups{DB: db.Read}.View(context.Background(), "u1", tt.now)
			if err != nil {
				t.Fatal(err)
			}
			tt.check(t, v)
		})
	}
}
