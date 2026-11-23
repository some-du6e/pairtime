// SPDX-License-Identifier: AGPL-3.0-or-later

package scorer

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/state"
	"github.com/some-du6e/pairtime/internal/agent/store"
	"github.com/some-du6e/pairtime/pkg/event"
	"github.com/some-du6e/pairtime/pkg/score"
)

// TestRollupAfterDrop checks that a rollup the scorer writes again, as in
// a rebuild, comes only from a day whose minutes the bucket store still
// keeps whole: in UTC-8, the local day 2026-05-26 spans the UTC days 05-26
// and 05-27, so after a Drop that keeps 05-27 on, its rollup stays as it
// was written when both files were there.
func TestRollupAfterDrop(t *testing.T) {
	zone := time.FixedZone("UTC-8", -8*3600)
	dir := t.TempDir()
	st, _, err := state.Open(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	bs, err := store.OpenBuckets(filepath.Join(dir, "buckets"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { bs.Close() })
	days := filepath.Join(dir, "days")
	l, err := New(Options{Ledger: filepath.Join(dir, "ledger"), Params: testTable(), State: st, Buckets: bs, Days: days,
		Zone: func() *time.Location { return zone }})
	if err != nil {
		t.Fatal(err)
	}
	put := func(at time.Time) {
		t.Helper()
		b := store.Bucket{V: score.Version(), Params: testTable().Hash(), Minute: at.UTC(), Device: "d_test",
			Slots: make([]event.BucketSlot, 4), State: event.StateFinal, CreditedS: 15,
			Projects: map[string]store.ProjectMinute{"p_test": {Credited: []store.Credited{{Why: event.ReasonPrompt, Ev: event.EvidenceB, Halves: 30}}}}}
		if _, _, err := bs.Put(b); err != nil {
			t.Fatal(err)
		}
	}
	rollup := func(date string) []byte {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(days, date+".json"))
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	// Two minutes of the local 05-26, one in each UTC day's file, and one
	// of the local 05-27.
	put(time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC))
	put(time.Date(2026, 5, 27, 4, 0, 0, 0, time.UTC))
	put(time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC))
	for _, date := range []string{"2026-05-26", "2026-05-27"} {
		if err := l.writeDay(date, zone); err != nil {
			t.Fatal(err)
		}
	}
	whole, before := rollup("2026-05-26"), rollup("2026-05-27")
	if _, err := bs.Drop("2026-05-27"); err != nil {
		t.Fatal(err)
	}
	put(time.Date(2026, 5, 27, 20, 0, 0, 0, time.UTC))
	cases := []struct {
		name  string
		date  string
		check func(t *testing.T, got []byte)
	}{
		{
			name: "a local day that starts before the first UTC day the store keeps keeps the rollup written while its minutes were all there",
			date: "2026-05-26",
			check: func(t *testing.T, got []byte) {
				if !bytes.Equal(got, whole) {
					t.Errorf("the rollup is\n%s\nwant\n%s", got, whole)
				}
			},
		},
		{
			name: "a local day from the first kept UTC day on is written again from its minutes",
			date: "2026-05-27",
			check: func(t *testing.T, got []byte) {
				if bytes.Equal(got, before) {
					t.Error("the rollup wasn't written again with the new minute")
				}
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := l.writeDay(c.date, zone); err != nil {
				t.Fatal(err)
			}
			c.check(t, rollup(c.date))
		})
	}
}
