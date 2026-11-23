// SPDX-License-Identifier: AGPL-3.0-or-later

package witness

import (
	"context"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/migrations"
	"github.com/some-du6e/pairtime/internal/server/store"
	"github.com/some-du6e/pairtime/internal/server/store/storetest"
	"github.com/some-du6e/pairtime/pkg/event"
)

// migrated returns an empty database with pairtimed's schema.
func migrated(t *testing.T, engine store.Engine) *store.DB {
	t.Helper()
	db := storetest.Open(t, engine)
	ms, err := migrations.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrations.Apply(context.Background(), db, ms, time.Now); err != nil {
		t.Fatal(err)
	}
	return db
}

// receipts stores a witness row of device for each batch, received at the
// given minute of day0.
func receipts(t *testing.T, db *store.DB, device string, got map[uint64]float64) {
	t.Helper()
	for b, m := range got {
		_, err := db.Write.ExecContext(context.Background(), `INSERT INTO witness_rows
(device_id, b, account_id, n, root, head, prev, received_at_ms, receipt_sig, receipt_key_id)
VALUES ($1, $2, 'account', 1, 'root', 'head', 'prev', $3, 'sig', 'key')`, device, int64(b), at(m))
		if err != nil {
			t.Fatal(err)
		}
	}
}

// minuteOf is a bucket of device for minute m of day0 that credits 15
// seconds at level ev, with the client's reading w.
func minuteOf(device string, m float64, w event.Witness, ev event.Evidence) event.MinuteBucket {
	return event.MinuteBucket{
		Minute: time.UnixMilli(at(m)).UTC(), Device: device,
		Slots: []event.BucketSlot{
			{Project: "p_one", Credited: 15, Why: event.ReasonPrompt, Kind: event.ActivityPrompting, Ev: ev},
			{}, {}, {},
		},
		CreditedS: 15, State: event.StatePending, Witness: w,
	}
}

// The minute the cases label starts at minute 100 of day0 and ends at 101.
const minute = 100.0

func TestBucket(t *testing.T) {
	// cutOff is a device the server last heard from before the minute,
	// and next two hours on, in the catch-up.
	cutOff := map[uint64]float64{1: 99.5, 2: 220, 3: 220, 4: 220}
	final := func(b event.MinuteBucket) event.MinuteBucket {
		b.State, b.Rev = event.StateFinal, b.Rev+1
		return b
	}
	more := func(b event.MinuteBucket) event.MinuteBucket {
		b.Slots = append([]event.BucketSlot(nil), b.Slots...)
		b.Slots[1] = event.BucketSlot{Project: "p_one", Credited: 15, Why: event.ReasonRead, Kind: event.ActivityReadingOutput, Ev: b.Slots[0].Ev}
		b.CreditedS, b.Rev = 30, b.Rev+1
		return b
	}
	witnessed := minuteOf("d", minute, event.Witnessed, event.EvidenceA)
	offline := minuteOf("d", minute, event.Offline, event.EvidenceA)
	tests := []struct {
		name     string
		got      map[uint64]float64 // the device's witness rows
		rev      event.MinuteBucket
		received float64 // the minute of day0 the revision arrived
		cur      *Stored
		want     event.Witness
	}{
		{"a revision that reached the server within WITNESS_LAG_S of its minute's end is witnessed",
			nil, witnessed, 116, nil, event.Witnessed},
		{"a revision that arrived later is backfilled",
			nil, witnessed, 116.5, nil, event.Backfilled},
		{"a late revision with the same credit as the one it replaces keeps that one's label",
			nil, final(witnessed), 117, &Stored{Bucket: witnessed, Label: event.Witnessed}, event.Witnessed},
		{"a late revision that changes the credit reads by its own arrival",
			nil, more(witnessed), 117, &Stored{Bucket: witnessed, Label: event.Witnessed}, event.Backfilled},
		{"a revision stored before the server labeled buckets passes no label on",
			nil, final(witnessed), 117, &Stored{Bucket: witnessed}, event.Backfilled},
		{"a late revision the client reads offline is offline when the device was cut off around it and caught up",
			cutOff, offline, 220.1, nil, event.Offline},
		{"a late revision with the same credit as an offline one stays offline, though it came well after the catch-up",
			cutOff, final(offline), 240, &Stored{Bucket: offline, Label: event.Offline}, event.Offline},
		{"a late revision the client reads witnessed is backfilled, cut off or not, since only the client knows of its failures",
			cutOff, witnessed, 220.1, nil, event.Backfilled},
		{"the client's offline is backfilled when a batch of the device got through within the minute's lag",
			map[uint64]float64{1: 99.5, 2: 105, 3: 220}, offline, 220.1, nil, event.Backfilled},
		{"the client's offline is backfilled when the revision came more than the lag after the catch-up began",
			cutOff, offline, 236, nil, event.Backfilled},
		{"the client's offline is backfilled when the catch-up starts the device's chain",
			map[uint64]float64{5: 220, 6: 220}, offline, 220.1, nil, event.Backfilled},
		{"the client's offline is backfilled when the catch-up skips a local-only gap",
			map[uint64]float64{1: 99.5, 5: 220, 6: 220}, offline, 220.1, nil, event.Backfilled},
		{"the client's offline is backfilled when the batch before the catch-up came after the minute, as a server clock set back can leave it",
			map[uint64]float64{1: 220.5, 2: 220}, offline, 220.1, nil, event.Backfilled},
		{"a label is never better than the client's reading: on time and read offline is offline",
			nil, offline, 101, nil, event.Offline},
		{"a label is never better than the client's reading: on time and read backfilled is backfilled",
			nil, minuteOf("d", minute, event.Backfilled, event.EvidenceC), 101, nil, event.Backfilled},
	}
	for _, engine := range storetest.Engines() {
		t.Run(string(engine), func(t *testing.T) {
			db := migrated(t, engine)
			for k, tc := range tests {
				t.Run(tc.name, func(t *testing.T) {
					// Each case has a device of its own, and so rows of its own.
					device := "d_case_" + string(rune('a'+k))
					tc.rev.Device = device
					receipts(t, db, device, tc.got)
					got, err := Bucket(context.Background(), db.Read, Revision{Bucket: tc.rev, ReceivedAt: at(tc.received)}, tc.cur)
					if err != nil {
						t.Fatal(err)
					}
					if got != tc.want {
						t.Errorf("label %q, want %q", got, tc.want)
					}
				})
			}
		})
	}
}

func TestSameCredit(t *testing.T) {
	base := minuteOf("d", minute, event.Witnessed, event.EvidenceA)
	tests := []struct {
		name string
		edit func(b *event.MinuteBucket)
		same bool
	}{
		{"the state, the revision and the client's reading don't count", func(b *event.MinuteBucket) {
			b.State, b.Rev, b.Witness = event.StateFinal, 7, event.Offline
		}, true},
		{"uncredited and agent time don't count", func(b *event.MinuteBucket) {
			b.Uncredited = map[event.Reason]float64{event.ReasonAbsent: 15}
			b.AgentRunningS, b.AgentOnly = 30, map[string]float64{"p_one": 30}
		}, true},
		{"another level is other credit", func(b *event.MinuteBucket) { b.Slots[0].Ev = event.EvidenceB }, false},
		{"another project is other credit", func(b *event.MinuteBucket) { b.Slots[0].Project = "p_two" }, false},
		{"another credited_s is other credit", func(b *event.MinuteBucket) { b.CreditedS = 15.5 }, false},
		{"other seconds in a slot are other credit", func(b *event.MinuteBucket) { b.Slots[0].Credited = 14.5 }, false},
		{"a label more is other credit", func(b *event.MinuteBucket) { b.Slots[0].Lbl = []event.Label{event.LabelViaTrack} }, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := base
			b.Slots = append([]event.BucketSlot(nil), base.Slots...)
			tc.edit(&b)
			if got := SameCredit(base, b); got != tc.same {
				t.Errorf("SameCredit = %v, want %v", got, tc.same)
			}
		})
	}
}

func TestOrder(t *testing.T) {
	tests := []struct {
		name          string
		a, b          event.Witness
		worse, better event.Witness
	}{
		{"backfilled is weaker than offline", event.Offline, event.Backfilled, event.Backfilled, event.Offline},
		{"offline is weaker than witnessed", event.Witnessed, event.Offline, event.Offline, event.Witnessed},
		{"anything that isn't a label counts as backfilled", event.Witnessed, "", event.Backfilled, event.Witnessed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Worse(tc.a, tc.b); got != tc.worse {
				t.Errorf("Worse = %q, want %q", got, tc.worse)
			}
			if got := Better(tc.a, tc.b); got != tc.better {
				t.Errorf("Better = %q, want %q", got, tc.better)
			}
		})
	}
}
