// SPDX-License-Identifier: AGPL-3.0-or-later

package ingest

import (
	"database/sql"
	"encoding/json"
	"math/rand/v2"
	"slices"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/store"
	"github.com/some-du6e/pairtime/pkg/event"
	"github.com/some-du6e/pairtime/pkg/ledger"
)

// TestServerWitness checks that ingest stores the server's own witness
// label with each bucket revision, beside the client's reading, which the
// bucket keeps as sent (WP-SV6; ingest-v1 7 item 13). Package witness
// tests the rule itself.
func TestServerWitness(t *testing.T) {
	engines(t, func(t *testing.T, engine store.Engine) {
		h := newHarness(t, engine)
		live := h.device("d_live", false)
		cut := h.device("d_cut_off", false)
		start := h.clock
		minute := start.Truncate(time.Minute)
		// stored returns the server's label for a minute's current revision,
		// its label for every revision in rev order, and the client's reading
		// the current revision holds.
		stored := func(t *testing.T, dev *testDevice, m time.Time) (string, []string, event.Witness) {
			t.Helper()
			var cur sql.NullString
			var js string
			if err := h.db.Read.QueryRow(`SELECT server_witness, bucket FROM minute_buckets WHERE device_id = $1 AND minute_ms = $2`,
				dev.ID, m.UnixMilli()).Scan(&cur, &js); err != nil {
				t.Fatal(err)
			}
			var mb event.MinuteBucket
			if err := json.Unmarshal([]byte(js), &mb); err != nil {
				t.Fatal(err)
			}
			rows, err := h.db.Read.Query(`SELECT server_witness FROM minute_bucket_revisions WHERE device_id = $1 AND minute_ms = $2 ORDER BY rev`,
				dev.ID, m.UnixMilli())
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			var revs []string
			for rows.Next() {
				var l sql.NullString
				if err := rows.Scan(&l); err != nil {
					t.Fatal(err)
				}
				revs = append(revs, l.String)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			return cur.String, revs, mb.Witness
		}
		send := func(t *testing.T, dev *testDevice, mbs ...event.MinuteBucket) {
			t.Helper()
			for i, r := range h.ok(t, upload{dev: dev, body: bucketsBody(t, mbs...)}).Buckets {
				if r.Code != CodeAccepted {
					t.Fatalf("bucket %d: %+v", i, r)
				}
			}
		}
		pending := sampleBucket(live.ID, minute, 0)
		pending.State = event.StatePending
		final := sampleBucket(live.ID, minute, 1)
		more := sampleBucket(live.ID, minute, 2)
		more.Slots[2] = event.BucketSlot{Project: "p_one", Credited: 10, Why: event.ReasonRead, Kind: event.ActivityReadingOutput, Ev: event.EvidenceB}
		more.CreditedS += 10
		steps := []struct {
			name  string
			run   func(t *testing.T)
			dev   *testDevice
			m     time.Time
			cur   string
			revs  []string
			taken event.Witness // the client's reading the bucket keeps
		}{
			{"a revision that arrives within the lag of its minute's end, after a batch of its time, is stored witnessed", func(t *testing.T) {
				// The uploader sends a minute once a batch sealed after it has
				// a receipt.
				h.ok(t, upload{dev: live, body: batchesBody(chain(rand.New(rand.NewPCG(5, 5)), ledger.Hash{}, 1, 1)...)})
				send(t, live, pending)
			}, live, minute, "witnessed", []string{"witnessed"}, event.Witnessed},
			{"the revision that turns it final after the lag keeps that label, since its credit is the same", func(t *testing.T) {
				h.clock = start.Add(20 * time.Minute)
				send(t, live, final)
			}, live, minute, "witnessed", []string{"witnessed", "witnessed"}, event.Witnessed},
			{"a late revision with more credit is stored backfilled, and the client's own reading stays as sent", func(t *testing.T) {
				send(t, live, more)
			}, live, minute, "backfilled", []string{"witnessed", "witnessed", "backfilled"}, event.Witnessed},
			{"after a rollback, a revision that a release from before migration 0009 stored passes no label on", func(t *testing.T) {
				m := minute.Add(2 * time.Minute)
				h.clock = m.Add(30 * time.Second)
				send(t, live, sampleBucket(live.ID, m, 0))
				// The release before 0009 stores rev 1, with more credit, 40
				// minutes on. It rewrites the minute's row without its
				// server_witness, which keeps rev 0's, and stores rev 1's
				// revision row without one.
				rolled := sampleBucket(live.ID, m, 1)
				rolled.Slots[2] = event.BucketSlot{Project: "p_one", Credited: 10, Why: event.ReasonRead, Kind: event.ActivityReadingOutput, Ev: event.EvidenceB}
				rolled.CreditedS += 10
				js, at := string(marshal(t, rolled.Shareable())), m.Add(40*time.Minute).UnixMilli()
				h.exec(t, `UPDATE minute_buckets SET rev = 1, received_at_ms = $3, bucket = $4, revisions = 2 WHERE device_id = $1 AND minute_ms = $2`,
					live.ID, m.UnixMilli(), at, js)
				h.exec(t, `INSERT INTO minute_bucket_revisions (device_id, minute_ms, rev, account_id, received_at_ms, bucket) VALUES ($1, $2, 1, $3, $4, $5)`,
					live.ID, m.UnixMilli(), live.Account, at, js)
				// Back on this release, rev 2 has rev 1's credit and comes late.
				h.clock = m.Add(41 * time.Minute)
				again := rolled
				again.Rev = 2
				send(t, live, again)
			}, live, minute.Add(2 * time.Minute), "backfilled", []string{"witnessed", "", "backfilled"}, event.Witnessed},
			{"a minute the client reads offline, sent in the catch-up after the server lost the device for two hours, is stored offline", func(t *testing.T) {
				h.clock = start
				roots := chain(rand.New(rand.NewPCG(6, 6)), ledger.Hash{}, 1, 4)
				h.ok(t, upload{dev: cut, body: batchesBody(roots[0])})
				h.clock = start.Add(2 * time.Hour)
				h.ok(t, upload{dev: cut, body: batchesBody(roots[1:]...)})
				h.clock = h.clock.Add(time.Second)
				mb := sampleBucket(cut.ID, minute.Add(5*time.Minute), 0)
				mb.Witness = event.Offline
				send(t, cut, mb)
			}, cut, minute.Add(5 * time.Minute), "offline", []string{"offline"}, event.Offline},
			{"a minute of the same stretch the client reads backfilled is stored backfilled", func(t *testing.T) {
				mb := sampleBucket(cut.ID, minute.Add(6*time.Minute), 0)
				mb.Witness = event.Backfilled
				for i := range mb.Slots {
					if mb.Slots[i].Credited > 0 {
						mb.Slots[i].Ev = event.EvidenceC
					}
				}
				send(t, cut, mb)
			}, cut, minute.Add(6 * time.Minute), "backfilled", []string{"backfilled"}, event.Backfilled},
		}
		for _, st := range steps {
			t.Run(st.name, func(t *testing.T) {
				st.run(t)
				cur, revs, taken := stored(t, st.dev, st.m)
				if cur != st.cur || !slices.Equal(revs, st.revs) || taken != st.taken {
					t.Errorf("stored label %q, revisions %q, the client's reading %q; want %q, %q, %q", cur, revs, taken, st.cur, st.revs, st.taken)
				}
			})
		}
	})
}
