// SPDX-License-Identifier: AGPL-3.0-or-later

package stream

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/some-du6e/pairtime/internal/server/migrations"
	"github.com/some-du6e/pairtime/internal/server/rollup"
	"github.com/some-du6e/pairtime/internal/server/store"
	"github.com/some-du6e/pairtime/internal/server/store/storetest"
	// The package declares a type event of its own.
	pevent "github.com/some-du6e/pairtime/pkg/event"
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
			// c, which nobody reads from here, goes, or a day of its
			// keepalives would fill its writer.
			c.close()
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

// bucketOf is device d1's revision rev of the minute at minute, in state,
// whose first slots credit 15 s each at the levels given, in project p1.
func bucketOf(minute time.Time, rev uint32, state pevent.BucketState, levels ...pevent.Evidence) pevent.MinuteBucket {
	mb := pevent.MinuteBucket{
		V: "1.0.0", Params: "sha256:" + strings.Repeat("ab", 32), Minute: minute.UTC(), Device: "d1",
		Slots: make([]pevent.BucketSlot, 4), State: state, Witness: pevent.Witnessed, Rev: rev,
	}
	for i, level := range levels {
		mb.Slots[i] = pevent.BucketSlot{Project: "p1", Credited: 15, Why: pevent.ReasonPrompt, Kind: pevent.ActivityPrompting, Ev: level}
		mb.CreditedS += 15
	}
	return mb
}

// revision is a bucket revision as the server received it: when, and the
// label it gave it (DESIGN 7.4).
type revision struct {
	at    time.Time
	label pevent.Witness
	mb    pevent.MinuteBucket
}

// store stores r for account u1 as ingest does: in minute_bucket_revisions,
// with its received time and label, and in the rollups, through
// rollup.ApplyEvent, in one transaction.
func (r revision) store(t *testing.T, db *store.DB) {
	t.Helper()
	ctx := context.Background()
	tx, err := db.Write.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	data, err := json.Marshal(r.mb)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO minute_bucket_revisions (device_id, minute_ms, rev, account_id, received_at_ms, bucket, server_witness)
VALUES ($1, $2, $3, 'u1', $4, $5, $6)`, r.mb.Device, r.mb.Minute.UnixMilli(), int64(r.mb.Rev), r.at.UnixMilli(), string(data), string(r.label)); err != nil {
		t.Fatal(err)
	}
	if err := rollup.ApplyEvent(ctx, tx, "u1", nil, r.mb); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestPublicTotals(t *testing.T) {
	// The profile is in UTC+14, so its local dates run from 10:00 UTC the
	// day before. local gives a time in it.
	const zone = "Pacific/Kiritimati"
	kiritimati, err := time.LoadLocation(zone)
	if err != nil {
		t.Fatal(err)
	}
	local := func(day string, hour int) time.Time {
		d, err := time.ParseInLocation(time.DateOnly, day, kiritimati)
		if err != nil {
			t.Fatal(err)
		}
		return d.Add(time.Duration(hour) * time.Hour)
	}
	// The publication of 2026-11-25, when 2026-11-24 has just ended in
	// every zone.
	at := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	final, pending := pevent.StateFinal, pevent.StatePending
	A, B, C := pevent.EvidenceA, pevent.EvidenceB, pevent.EvidenceC
	seen := func(day string) time.Time { return local(day, 13) }
	tests := []struct {
		name string
		at   time.Time
		revs []revision
		want []ProfileDay
	}{
		{name: "a day's witnessed final minutes count at levels A and B, by local day", at: at,
			revs: []revision{{seen("2026-11-24"), pevent.Witnessed, bucketOf(local("2026-11-24", 12), 1, final, A, B)}},
			want: []ProfileDay{{Date: "2026-11-24", CreditedS: 30}}},
		{name: "level C never reaches a public total", at: at,
			revs: []revision{{seen("2026-11-24"), pevent.Witnessed, bucketOf(local("2026-11-24", 12), 1, final, C, C)}},
			want: []ProfileDay{}},
		{name: "a revision the server labeled backfilled counts for nothing, whatever level its device claims", at: at,
			revs: []revision{{seen("2026-11-24"), pevent.Backfilled, bucketOf(local("2026-11-24", 12), 1, final, A, A)}},
			want: []ProfileDay{}},
		{name: "an offline catch-up the server labeled offline counts", at: at,
			revs: []revision{{seen("2026-11-24"), pevent.Offline, bucketOf(local("2026-11-24", 12), 1, final, A)}},
			want: []ProfileDay{{Date: "2026-11-24", CreditedS: 15}}},
		{name: "a revision received after the publication doesn't show at it", at: at,
			revs: []revision{{at.Add(time.Hour), pevent.Witnessed, bucketOf(local("2026-11-24", 12), 1, final, A)}},
			want: []ProfileDay{}},
		{name: "a revision received after one publication shows at the next", at: at.Add(24 * time.Hour),
			revs: []revision{{at.Add(time.Hour), pevent.Witnessed, bucketOf(local("2026-11-24", 12), 1, final, A)}},
			want: []ProfileDay{{Date: "2026-11-24", CreditedS: 15}}},
		{name: "a later revision received before the publication replaces the earlier, and one received after it doesn't yet",
			at: at,
			revs: []revision{
				{seen("2026-11-24"), pevent.Witnessed, bucketOf(local("2026-11-24", 12), 1, final, A)},
				{local("2026-11-25", 1), pevent.Witnessed, bucketOf(local("2026-11-24", 12), 2, final, A, A, A)},
				{at.Add(time.Minute), pevent.Witnessed, bucketOf(local("2026-11-24", 12), 3, final, A, A, A, A)},
			},
			want: []ProfileDay{{Date: "2026-11-24", CreditedS: 45}}},
		{name: "a pending minute counts, since every minute of a date ended everywhere is past its pending window", at: at,
			revs: []revision{{seen("2026-11-24"), pevent.Witnessed, bucketOf(local("2026-11-24", 12), 1, pending, B)}},
			want: []ProfileDay{{Date: "2026-11-24", CreditedS: 15}}},
		{name: "the agent's time on its own doesn't count", at: at,
			revs: []revision{{seen("2026-11-24"), pevent.Witnessed, func() pevent.MinuteBucket {
				mb := bucketOf(local("2026-11-24", 12), 1, final)
				mb.AgentRunningS, mb.AgentOnly = 60, map[string]float64{"p1": 60}
				return mb
			}()}},
			want: []ProfileDay{}},
		{name: "the dates are the 30 up to the newest ended in every zone, oldest first, whatever the profile's own zone", at: at,
			revs: []revision{
				{seen("2026-11-24"), pevent.Witnessed, bucketOf(local("2026-11-24", 12), 1, final, A)},
				{seen("2026-10-26"), pevent.Witnessed, bucketOf(local("2026-10-26", 12), 1, final, A, A)},
				{seen("2026-10-25"), pevent.Witnessed, bucketOf(local("2026-10-25", 12), 1, final, A, A, A)},
				// Ended in the profile's zone, not yet in UTC-12.
				{seen("2026-11-25"), pevent.Witnessed, bucketOf(local("2026-11-25", 12), 1, final, A, A, A, A)},
			},
			want: []ProfileDay{{Date: "2026-10-26", CreditedS: 30}, {Date: "2026-11-24", CreditedS: 15}}},
		{name: "a second before the publication, its newest date hasn't ended in every zone and doesn't show", at: at.Add(-time.Second),
			revs: []revision{
				{seen("2026-11-24"), pevent.Witnessed, bucketOf(local("2026-11-24", 12), 1, final, A)},
				{seen("2026-11-23"), pevent.Witnessed, bucketOf(local("2026-11-23", 12), 1, final, A, A)},
			},
			want: []ProfileDay{{Date: "2026-11-23", CreditedS: 30}}},
	}
	for _, engine := range storetest.Engines() {
		for _, tt := range tests {
			t.Run(string(engine)+"/"+tt.name, func(t *testing.T) {
				db := publicDB(t, engine, zone)
				for _, r := range tt.revs {
					r.store(t, db)
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

// rollupProfiles serves account u1's public profile, named "rowan", with
// PublicTotals over db.
type rollupProfiles struct{ db *store.DB }

func (p rollupProfiles) Resolve(_ context.Context, name string) (string, bool, error) {
	return "u1", name == "rowan", nil
}

func (p rollupProfiles) Totals(ctx context.Context, account string, at time.Time) (ProfileTotals, bool, error) {
	t, err := PublicTotals(ctx, p.db.Read, account, at)
	return t, true, err
}

// TestProfileTopicOnRollups runs the profile topic on PublicTotals over a
// real database, as WP-SV9 will (D-public-daily; #307).
func TestProfileTopicOnRollups(t *testing.T) {
	const (
		day  = 24 * time.Hour
		noon = 12 * time.Hour
	)
	// A bubble's clock starts here.
	start := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	A := pevent.EvidenceA
	tests := []struct {
		name string
		// revs are stored once the clock reaches each one's at.
		revs []revision
		// when is when each profile event comes, since start, and days
		// what each shows.
		when []time.Duration
		days [][]string
	}{
		{name: "a late revision to a shown date shows at the next 12:00 UTC, never at the hour it came, and a backfilled one counts for nothing",
			revs: []revision{
				// 2000-01-01's minute, witnessed soon after it ended.
				{start.Add(10 * time.Minute), pevent.Witnessed, bucketOf(start, 1, pevent.StateFinal, A, A)},
				// 2000-01-02's minute, which the server labeled backfilled.
				{start.Add(day + 10*time.Minute), pevent.Backfilled, bucketOf(start.Add(day), 1, pevent.StateFinal, A)},
				// A catch-up at 21:10 raises 2000-01-01 after it was shown.
				{start.Add(day + 21*time.Hour + 10*time.Minute), pevent.Witnessed, bucketOf(start, 2, pevent.StateFinal, A, A, A, A)},
			},
			when: []time.Duration{day + noon, 2*day + noon},
			days: [][]string{{"2000-01-01=30"}, {"2000-01-01=60"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// The database is the outer test's, so its files and its pool
			// stay out of the bubble.
			db := publicDB(t, store.SQLite, "UTC")
			synctest.Test(t, func(t *testing.T) {
				if now := time.Now(); !now.Equal(start) {
					t.Fatalf("the bubble starts at %v, want %v", now, start)
				}
				h := New(Options{Account: testAccount, Profiles: rollupProfiles{db}})
				defer h.Close()
				c := open(t, h, profileTopic, nil)
				c.read()
				next := 0
				when, data := watch(t, c, start, 4*day, 10*time.Minute, func(at time.Duration) {
					for next < len(tt.revs) && !tt.revs[next].at.After(start.Add(at)) {
						tt.revs[next].store(t, db)
						next++
					}
				})
				if !reflect.DeepEqual(when, tt.when) {
					t.Fatalf("profile events at %v, want %v", when, tt.when)
				}
				for i := range data {
					if got := days(t, data[i]); !reflect.DeepEqual(got, tt.days[i]) {
						t.Errorf("event %d shows %q, want %q", i, got, tt.days[i])
					}
				}
			})
		})
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
