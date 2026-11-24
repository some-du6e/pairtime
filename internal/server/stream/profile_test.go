// SPDX-License-Identifier: AGPL-3.0-or-later

package stream

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/some-du6e/pairtime/internal/server/migrations"
	"github.com/some-du6e/pairtime/internal/server/store"
	"github.com/some-du6e/pairtime/internal/server/store/storetest"
)

// profileTopic is rowan's public profile, which fakeProfiles serves.
const profileTopic = Path + "?topics=profile:@rowan"

// watch runs the clock for d in steps of a minute, calling change, when
// set, at the start of each minute with the time since the bubble began,
// and returns when each profile event reached c and its data.
func watch(t *testing.T, c *client, start time.Time, d time.Duration, change func(at time.Duration)) (when []time.Duration, data []string) {
	t.Helper()
	for at := time.Since(start); at < d; at = time.Since(start) {
		if change != nil {
			change(at)
		}
		time.Sleep(time.Minute)
		synctest.Wait()
		for _, e := range events(c.read()) {
			if e.event != "profile" {
				t.Errorf("an event %q on a profile's stream", e.event)
			}
			when = append(when, time.Since(start))
			data = append(data, e.data)
		}
	}
	return when, data
}

// day is a profile event's data: one day, the bubble's first, of s
// seconds.
func day(s string) string {
	return `{"days":[{"date":"2000-01-01","credited_s":` + s + `}]}`
}

func TestProfileTopic(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T, h *Hub, fp *fakeProfiles, c *client, start time.Time)
	}{
		{name: "changes during an hour go out once, on the next hour, with the totals as of then", run: func(t *testing.T, h *Hub, fp *fakeProfiles, c *client, start time.Time) {
			when, data := watch(t, c, start, 3*time.Hour, func(at time.Duration) {
				switch at {
				case 10 * time.Minute, 20 * time.Minute:
					fp.version.Add(1)
					h.Changed("a1")
				case 50 * time.Minute:
					h.Changed("a1")
				}
			})
			if want := []time.Duration{time.Hour}; !reflect.DeepEqual(when, want) || !reflect.DeepEqual(data, []string{day("2")}) {
				t.Errorf("profile events at %v with %q, want one at %v with %s", when, data, want, day("2"))
			}
		}},
		{name: "a public topic never emits twice within an hour, however often its account changes, and emits on each hour it changed", run: func(t *testing.T, h *Hub, fp *fakeProfiles, c *client, start time.Time) {
			when, _ := watch(t, c, start, 4*time.Hour, func(time.Duration) {
				fp.version.Add(1)
				h.Changed("a1")
			})
			want := []time.Duration{time.Hour, 2 * time.Hour, 3 * time.Hour, 4 * time.Hour}
			if !reflect.DeepEqual(when, want) {
				t.Errorf("profile events at %v, want %v", when, want)
			}
			for i := 1; i < len(when); i++ {
				if when[i]-when[i-1] < time.Hour {
					t.Errorf("events %v and %v are less than an hour apart", when[i-1], when[i])
				}
			}
		}},
		{name: "a change in an hour's last millisecond goes out on that hour, and not a moment before", run: func(t *testing.T, h *Hub, fp *fakeProfiles, c *client, start time.Time) {
			time.Sleep(time.Hour - time.Millisecond)
			fp.version.Store(7)
			h.Changed("a1")
			synctest.Wait()
			if got := events(c.read()); len(got) != 0 {
				t.Fatalf("an event before the hour: %+v", got)
			}
			time.Sleep(time.Millisecond)
			synctest.Wait()
			if got := events(c.read()); len(got) != 1 || got[0].data != day("7") {
				t.Errorf("on the hour the stream got %+v, want %s", got, day("7"))
			}
		}},
		{name: "a change that leaves the totals as the last event had them emits nothing", run: func(t *testing.T, h *Hub, fp *fakeProfiles, c *client, start time.Time) {
			when, _ := watch(t, c, start, 3*time.Hour, func(at time.Duration) {
				switch at {
				case 10 * time.Minute:
					fp.version.Store(5)
					h.Changed("a1")
				case 70 * time.Minute, 130 * time.Minute:
					h.Changed("a1")
				}
			})
			if want := []time.Duration{time.Hour}; !reflect.DeepEqual(when, want) {
				t.Errorf("profile events at %v, want only %v", when, want)
			}
		}},
		{name: "another account's change doesn't touch the profile, and nothing changed means no read", run: func(t *testing.T, h *Hub, fp *fakeProfiles, c *client, start time.Time) {
			when, _ := watch(t, c, start, 2*time.Hour, func(at time.Duration) {
				if at == 10*time.Minute {
					h.Changed("a2")
				}
			})
			if len(when) != 0 || fp.reads.Load() != 0 {
				t.Errorf("profile events at %v and %d reads, want none", when, fp.reads.Load())
			}
		}},
		{name: "a profile that stops being public ends its streams on the next hour", run: func(t *testing.T, h *Hub, fp *fakeProfiles, c *client, start time.Time) {
			time.Sleep(10 * time.Minute)
			fp.private.Store(true)
			h.Changed("a1")
			time.Sleep(50 * time.Minute)
			synctest.Wait()
			if !c.ended() || h.Streams() != 0 {
				t.Errorf("after the profile went private its stream ended: %v, with %d streams open", c.ended(), h.Streams())
			}
			h.mu.Lock()
			_, kept := h.topics[profileKey+"a1"]
			h.mu.Unlock()
			if kept {
				t.Error("the private profile's topic is still there")
			}
		}},
		{name: "its events carry daily totals alone: no project, no account and nothing finer than a day", run: func(t *testing.T, h *Hub, fp *fakeProfiles, c *client, start time.Time) {
			_, data := watch(t, c, start, time.Hour, func(at time.Duration) {
				if at == 0 {
					fp.version.Store(60)
					h.Changed("a1")
				}
			})
			if len(data) != 1 {
				t.Fatalf("got %d events, want 1", len(data))
			}
			var v map[string][]map[string]any
			if err := json.Unmarshal([]byte(data[0]), &v); err != nil {
				t.Fatal(err)
			}
			keys := func(m map[string]any) []string {
				var out []string
				for k := range m {
					out = append(out, k)
				}
				slices.Sort(out)
				return out
			}
			if len(v) != 1 || len(v["days"]) != 1 || !reflect.DeepEqual(keys(v["days"][0]), []string{"credited_s", "date"}) {
				t.Errorf("profile data %s, want only days of a date and credited_s", data[0])
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				fp := &fakeProfiles{}
				h := New(Options{Account: testAccount, Profiles: fp})
				defer h.Close()
				start := time.Now()
				// Anonymous: no session.
				c := open(t, h, profileTopic, nil)
				c.read()
				if c.code != 200 {
					t.Fatalf("an anonymous profile stream got %d", c.code)
				}
				tt.run(t, h, fp, c, start)
			})
		})
	}
}

// rollupRow is one rollups_daily row, in milliseconds.
type rollupRow struct {
	project, day, kind       string
	final, pending           [3]int64
	agentFinal, agentPending int64
}

func TestPublicTotals(t *testing.T) {
	// It is the 24th in the profile's zone, UTC+14, and the 23rd in UTC.
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	const zone = "Pacific/Kiritimati"
	tests := []struct {
		name string
		rows []rollupRow
		want []ProfileDay
	}{
		{name: "final time at levels A and B counts, by the profile's local day",
			rows: []rollupRow{{project: "p1", day: "2026-11-24", kind: "prompting", final: [3]int64{60000, 30000, 0}}},
			want: []ProfileDay{{Date: "2026-11-24", CreditedS: 90}}},
		{name: "level C never reaches a public total",
			rows: []rollupRow{{project: "p1", day: "2026-11-24", kind: "prompting", final: [3]int64{0, 0, 45000}}},
			want: []ProfileDay{}},
		{name: "pending minutes don't show on a public page",
			rows: []rollupRow{{project: "p1", day: "2026-11-24", kind: "prompting", pending: [3]int64{60000, 15000, 0}}},
			want: []ProfileDay{}},
		{name: "the agent's time on its own doesn't count",
			rows: []rollupRow{{project: "p1", day: "2026-11-24", kind: "", agentFinal: 120000, agentPending: 30000}},
			want: []ProfileDay{}},
		{name: "a day's projects and kinds add up to one total, which names none of them",
			rows: []rollupRow{
				{project: "p1", day: "2026-11-24", kind: "prompting", final: [3]int64{10000, 0, 0}},
				{project: "p2", day: "2026-11-24", kind: "testing", final: [3]int64{0, 20500, 7000}},
			},
			want: []ProfileDay{{Date: "2026-11-24", CreditedS: 30.5}}},
		{name: "the days are the last 30 in the profile's zone, oldest first",
			rows: []rollupRow{
				{project: "p1", day: "2026-11-24", kind: "testing", final: [3]int64{1000, 0, 0}},
				{project: "p1", day: "2026-10-26", kind: "testing", final: [3]int64{2000, 0, 0}},
				{project: "p1", day: "2026-10-25", kind: "testing", final: [3]int64{3000, 0, 0}},
				{project: "p1", day: "2026-11-25", kind: "testing", final: [3]int64{4000, 0, 0}},
			},
			want: []ProfileDay{{Date: "2026-10-26", CreditedS: 2}, {Date: "2026-11-24", CreditedS: 1}}},
	}
	for _, engine := range storetest.Engines() {
		for _, tt := range tests {
			t.Run(string(engine)+"/"+tt.name, func(t *testing.T) {
				db := publicDB(t, engine, zone)
				for _, r := range tt.rows {
					if _, err := db.Write.ExecContext(context.Background(), `INSERT INTO rollups_daily (user_id, project_id, day, activity_kind,
final_a_ms, final_b_ms, final_c_ms, pending_a_ms, pending_b_ms, pending_c_ms, agent_only_final_ms, agent_only_pending_ms)
VALUES ('u1', $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`, r.project, r.day, r.kind,
						r.final[0], r.final[1], r.final[2], r.pending[0], r.pending[1], r.pending[2], r.agentFinal, r.agentPending); err != nil {
						t.Fatal(err)
					}
				}
				got, err := PublicTotals(context.Background(), db.Read, "u1", now)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got.Days, tt.want) {
					t.Errorf("PublicTotals = %+v, want %+v", got.Days, tt.want)
				}
			})
		}
	}
}

// publicDB returns a migrated database with account "u1" in zone.
func publicDB(t *testing.T, engine store.Engine, zone string) *store.DB {
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
