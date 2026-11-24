// SPDX-License-Identifier: AGPL-3.0-or-later

package stream

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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

// watch runs the clock for d from start in steps of step, calling change,
// when set, at the start of each step with the time since start, and
// returns when each profile event reached c, since start, and its data.
// A bubble's clock starts at 2000-01-01 00:00 UTC, so with a step that
// divides an hour, a step ends on every 12:00 UTC.
func watch(t *testing.T, c *client, start time.Time, d, step time.Duration, change func(at time.Duration)) (when []time.Duration, data []string) {
	t.Helper()
	for at := time.Since(start); at < d; at = time.Since(start) {
		if change != nil {
			change(at)
		}
		time.Sleep(step)
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

// days is a profile event's data: each date with its seconds, as
// "date=seconds".
func days(t *testing.T, data string) []string {
	t.Helper()
	var v ProfileTotals
	if err := json.Unmarshal([]byte(data), &v); err != nil {
		t.Fatal(err)
	}
	out := []string{}
	for _, d := range v.Days {
		out = append(out, fmt.Sprintf("%s=%v", d.Date, d.CreditedS))
	}
	return out
}

func TestProfileTopic(t *testing.T) {
	const (
		day  = 24 * time.Hour
		noon = 12 * time.Hour
	)
	tests := []struct {
		name string
		run  func(t *testing.T, h *Hub, fp *fakeProfiles, c *client, start time.Time)
	}{
		{name: "a day's time shows at the first 12:00 UTC after its date ended in every zone, and never sooner", run: func(t *testing.T, h *Hub, fp *fakeProfiles, c *client, start time.Time) {
			when, data := watch(t, c, start, 3*day, 10*time.Minute, func(at time.Duration) {
				if at == 10*time.Minute {
					fp.set("2000-01-01", 60)
				}
			})
			// 2000-01-01 ends at UTC-12 at 12:00 UTC on 2000-01-02.
			if want := []time.Duration{day + noon}; !reflect.DeepEqual(when, want) {
				t.Fatalf("profile events at %v, want %v", when, want)
			}
			if got := days(t, data[0]); !reflect.DeepEqual(got, []string{"2000-01-01=60"}) {
				t.Errorf("the event shows %q, want 2000-01-01's 60 s", got)
			}
		}},
		{name: "the topic emits only at 12:00 UTC, at most once a day, however often its account's time changes", run: func(t *testing.T, h *Hub, fp *fakeProfiles, c *client, start time.Time) {
			seconds := 0.0
			when, _ := watch(t, c, start, 3*day, 10*time.Minute, func(at time.Duration) {
				if at%time.Hour == 0 {
					seconds += 15
					fp.set("1999-12-30", seconds)
				}
			})
			want := []time.Duration{noon, day + noon, 2*day + noon}
			if !reflect.DeepEqual(when, want) {
				t.Errorf("profile events at %v, want %v", when, want)
			}
			for i := 1; i < len(when); i++ {
				if when[i]-when[i-1] < day {
					t.Errorf("events %v and %v are less than a day apart", when[i-1], when[i])
				}
			}
		}},
		{name: "a date's later change, after it was shown, shows at the next 12:00 UTC", run: func(t *testing.T, h *Hub, fp *fakeProfiles, c *client, start time.Time) {
			when, data := watch(t, c, start, 3*day, 10*time.Minute, func(at time.Duration) {
				switch at {
				case 10 * time.Minute:
					fp.set("2000-01-01", 60)
				case day + 15*time.Hour:
					fp.set("2000-01-01", 90)
				}
			})
			if want := []time.Duration{day + noon, 2*day + noon}; !reflect.DeepEqual(when, want) {
				t.Fatalf("profile events at %v, want %v", when, want)
			}
			if got := days(t, data[1]); !reflect.DeepEqual(got, []string{"2000-01-01=90"}) {
				t.Errorf("the second event shows %q, want 2000-01-01's 90 s", got)
			}
		}},
		{name: "once shown, a page that stays the same makes no more events", run: func(t *testing.T, h *Hub, fp *fakeProfiles, c *client, start time.Time) {
			// A date that ended everywhere before the bubble began, with
			// its time first there now: the first publication shows it.
			fp.set("1999-12-29", 120)
			when, _ := watch(t, c, start, 3*day, 10*time.Minute, nil)
			if want := []time.Duration{noon}; !reflect.DeepEqual(when, want) {
				t.Errorf("profile events at %v, want only %v", when, want)
			}
		}},
		{name: "EndProfile ends the profile's streams at once, as a removal does", run: func(t *testing.T, h *Hub, fp *fakeProfiles, c *client, start time.Time) {
			time.Sleep(10 * time.Hour)
			synctest.Wait()
			c.read()
			fp.private.Store(true)
			h.EndProfile("a1")
			synctest.Wait()
			if !c.ended() || h.Streams() != 0 {
				t.Errorf("after EndProfile the stream ended: %v, with %d streams open", c.ended(), h.Streams())
			}
		}},
		{name: "a profile that turned private without EndProfile ends its streams at the next 12:00 UTC", run: func(t *testing.T, h *Hub, fp *fakeProfiles, c *client, start time.Time) {
			time.Sleep(10 * time.Hour)
			synctest.Wait()
			c.read()
			fp.private.Store(true)
			time.Sleep(2*time.Hour - time.Second)
			synctest.Wait()
			c.read()
			if c.ended() {
				t.Fatal("the stream ended before 12:00 UTC")
			}
			time.Sleep(time.Second)
			synctest.Wait()
			if !c.ended() {
				t.Error("at 12:00 UTC the private profile's stream is still open")
			}
		}},
		{name: "a profile's id is the same for every viewer, whether or not anyone watched before", run: func(t *testing.T, h *Hub, fp *fakeProfiles, c *client, start time.Time) {
			time.Sleep(13 * time.Hour)
			synctest.Wait()
			c.read()
			// c has watched since the start; a second viewer comes now,
			// and a third after every stream left and the topic went.
			second := open(t, h, profileTopic, nil)
			id2 := lastID(second.read())
			c.close()
			second.close()
			time.Sleep(2 * DefaultWindow)
			synctest.Wait()
			h.mu.Lock()
			_, kept := h.topics[profileKey+"a1"]
			h.mu.Unlock()
			if kept {
				t.Fatal("the topic nobody watched for two windows is still there")
			}
			third := open(t, h, profileTopic, nil)
			id3 := lastID(third.read())
			if id2 != id3 {
				t.Errorf("a viewer of a watched profile got id %q and one of an unwatched one %q, want the same", id2, id3)
			}
		}},
		{name: "a reconnect the same day needs nothing, and one after a 12:00 UTC refetches with resync", run: func(t *testing.T, h *Hub, fp *fakeProfiles, c *client, start time.Time) {
			time.Sleep(13 * time.Hour)
			synctest.Wait()
			c.read()
			// A stream opened after the day's publication has it as its
			// place.
			first := open(t, h, profileTopic, nil)
			last := lastID(first.read())
			first.close()
			reconnect := func() []sse {
				header := http.Header{"Last-Event-Id": {last}}
				r := open(t, h, profileTopic, header)
				defer r.close()
				return events(r.read())
			}
			time.Sleep(time.Hour)
			if got := reconnect(); len(got) != 0 {
				t.Errorf("a reconnect the same day got %+v, want nothing", got)
			}
			time.Sleep(day)
			if got := reconnect(); len(got) != 1 || got[0].event != "resync" {
				t.Errorf("a reconnect a day later got %+v, want resync", got)
			}
		}},
		{name: "an event carries daily totals alone: no project, no account and nothing finer than a day", run: func(t *testing.T, h *Hub, fp *fakeProfiles, c *client, start time.Time) {
			fp.set("2000-01-01", 45)
			_, data := watch(t, c, start, 2*day, 10*time.Minute, nil)
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

func TestPublication(t *testing.T) {
	tests := []struct {
		name  string
		at    time.Time
		last  time.Time
		ended string
	}{
		{name: "before 12:00 UTC the last publication was the day before's", at: time.Date(2026, 11, 25, 11, 59, 59, 0, time.UTC),
			last: time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC), ended: "2026-11-23"},
		{name: "at 12:00 UTC the day's publication is the last, and yesterday's date has ended in every zone", at: time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC),
			last: time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC), ended: "2026-11-24"},
		{name: "another zone's clock reads the same instant", at: time.Date(2026, 11, 26, 1, 30, 0, 0, time.FixedZone("UTC+14", 14*3600)),
			last: time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC), ended: "2026-11-23"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := lastPublication(tt.at); !got.Equal(tt.last) {
				t.Errorf("lastPublication(%v) = %v, want %v", tt.at, got, tt.last)
			}
			if got := endedEverywhere(tt.at); got != tt.ended {
				t.Errorf("endedEverywhere(%v) = %s, want %s", tt.at, got, tt.ended)
			}
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
	// The publication of 2026-11-25, when 2026-11-24 has just ended in
	// every zone. The profile is in UTC+14, where the 25th ended hours ago,
	// which changes nothing.
	at := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	const zone = "Pacific/Kiritimati"
	tests := []struct {
		name string
		at   time.Time
		rows []rollupRow
		want []ProfileDay
	}{
		{name: "final time at levels A and B counts, by local day", at: at,
			rows: []rollupRow{{project: "p1", day: "2026-11-24", kind: "prompting", final: [3]int64{60000, 30000, 0}}},
			want: []ProfileDay{{Date: "2026-11-24", CreditedS: 90}}},
		{name: "level C never reaches a public total", at: at,
			rows: []rollupRow{{project: "p1", day: "2026-11-24", kind: "prompting", final: [3]int64{0, 0, 45000}}},
			want: []ProfileDay{}},
		{name: "pending minutes don't show on a public page", at: at,
			rows: []rollupRow{{project: "p1", day: "2026-11-24", kind: "prompting", pending: [3]int64{60000, 15000, 0}}},
			want: []ProfileDay{}},
		{name: "the agent's time on its own doesn't count", at: at,
			rows: []rollupRow{{project: "p1", day: "2026-11-24", kind: "", agentFinal: 120000, agentPending: 30000}},
			want: []ProfileDay{}},
		{name: "a day's projects and kinds add up to one total, which names none of them", at: at,
			rows: []rollupRow{
				{project: "p1", day: "2026-11-24", kind: "prompting", final: [3]int64{10000, 0, 0}},
				{project: "p2", day: "2026-11-24", kind: "testing", final: [3]int64{0, 20500, 7000}},
			},
			want: []ProfileDay{{Date: "2026-11-24", CreditedS: 30.5}}},
		{name: "the dates are the 30 up to the newest ended in every zone, oldest first, whatever the profile's own zone", at: at,
			rows: []rollupRow{
				{project: "p1", day: "2026-11-24", kind: "testing", final: [3]int64{1000, 0, 0}},
				{project: "p1", day: "2026-10-26", kind: "testing", final: [3]int64{2000, 0, 0}},
				{project: "p1", day: "2026-10-25", kind: "testing", final: [3]int64{3000, 0, 0}},
				{project: "p1", day: "2026-11-25", kind: "testing", final: [3]int64{4000, 0, 0}},
			},
			want: []ProfileDay{{Date: "2026-10-26", CreditedS: 2}, {Date: "2026-11-24", CreditedS: 1}}},
		{name: "a second before the publication, its newest date hasn't ended in every zone and doesn't show", at: at.Add(-time.Second),
			rows: []rollupRow{
				{project: "p1", day: "2026-11-24", kind: "testing", final: [3]int64{1000, 0, 0}},
				{project: "p1", day: "2026-11-23", kind: "testing", final: [3]int64{5000, 0, 0}},
			},
			want: []ProfileDay{{Date: "2026-11-23", CreditedS: 5}}},
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
				got, err := PublicTotals(context.Background(), db.Read, "u1", tt.at)
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
