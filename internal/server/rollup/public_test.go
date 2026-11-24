// SPDX-License-Identifier: AGPL-3.0-or-later

package rollup

import (
	"context"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/store"
	"github.com/some-du6e/pairtime/internal/server/store/storetest"
	"github.com/some-du6e/pairtime/pkg/event"
)

// dashboardAB is the level A and B credit, final and pending, that the
// account's rollups_daily rows keep for day: the owner's dashboard's
// number for it.
func dashboardAB(t *testing.T, db *store.DB, account, day string) int64 {
	t.Helper()
	all, err := Read(context.Background(), db.Read, account, day, day)
	if err != nil {
		t.Fatal(err)
	}
	var n int64
	for _, d := range all {
		n += d.FinalMS[0] + d.FinalMS[1] + d.PendingMS[0] + d.PendingMS[1]
	}
	return n
}

func TestPublicCreditAsOf(t *testing.T) {
	// The day's minutes start at 15:00 UTC on 2026-11-20, the cap is 12
	// minutes, and the read is a day later unless a case says when.
	const limit = 12 * 60_000
	const day = "2026-11-20"
	asOf := time.Unix(base, 0).Add(24 * time.Hour)
	// minutes is n of device's minutes from start, each crediting all four
	// slots 15 s at level, received as each ends, which the server labeled
	// and the client read as label.
	minutes := func(device string, start int64, n int, state, level string, label event.Witness) []revision {
		s := sl("p1", 15, "prompting", level)
		out := make([]revision, 0, n)
		for i := range n {
			m := start + int64(i)*60
			out = append(out, revision{bucket: bk(device, m, 0, state, nil, s, s, s, s).bucket, received: time.Unix(m+60, 0), label: label, client: label})
		}
		return out
	}
	// one is device's revision rev of the minute at base, with slot 0
	// crediting cr seconds at level, received at received, which the
	// server labeled label and the client read as client.
	one := func(device string, rev int, cr float64, level string, received time.Time, label, client event.Witness) revision {
		return revision{bucket: bk(device, base, rev, "final", nil, sl("p_"+device, cr, "prompting", level)).bucket, received: received, label: label, client: client}
	}
	soon := time.Unix(base+120, 0)
	tests := []struct {
		name string
		revs []revision
		// at is the read's time, asOf when unset.
		at   time.Time
		want Credit
		// dashboard says the dashboard credits the day want's total too.
		dashboard bool
	}{
		{
			name:      "a day past the cap shows the cap's level A and B credit, which with no backfilled revision is what the dashboard credits",
			revs:      minutes("d1", base, 13, "final", "A", event.Witnessed),
			want:      Credit{AMS: limit},
			dashboard: true,
		},
		{
			name: "backfilled lookback minutes before the witnessed ones take their room under the cap, so the page shows the 10 minutes the dashboard keeps of 11",
			revs: append(minutes("d1", base, 2, "final", "C", event.Backfilled),
				minutes("d1", base+180, 11, "final", "A", event.Witnessed)...),
			want:      Credit{AMS: 10 * 60_000},
			dashboard: true,
		},
		{
			name: "level C credit takes its room under the cap too, though it never shows",
			revs: append(minutes("d1", base, 3, "final", "C", event.Witnessed),
				minutes("d1", base+180, 10, "final", "B", event.Witnessed)...),
			want:      Credit{BMS: 9 * 60_000},
			dashboard: true,
		},
		{
			name: "a slot that a backfilled revision wins shows nothing, though a witnessed revision on another device credits it too",
			revs: []revision{
				one("d1", 0, 10, "A", soon, event.Witnessed, event.Witnessed),
				one("d2", 0, 15, "C", soon, event.Backfilled, event.Backfilled),
			},
			dashboard: true,
		},
		{
			name: "a slot that a witnessed revision wins shows, though a backfilled revision on another device credits it too",
			revs: []revision{
				one("d1", 0, 15, "A", soon, event.Witnessed, event.Witnessed),
				one("d2", 0, 10, "C", soon, event.Backfilled, event.Backfilled),
			},
			want:      Credit{AMS: 15_000},
			dashboard: true,
		},
		{
			name: "a device's later revision that the server labeled backfilled replaces its witnessed one, as on the dashboard, and shows nothing",
			revs: []revision{
				one("d1", 0, 15, "A", soon, event.Witnessed, event.Witnessed),
				one("d1", 1, 5, "A", soon.Add(2*time.Hour), event.Backfilled, event.Witnessed),
			},
		},
		{
			name: "a revision received after the read takes no room under the cap",
			revs: append([]revision{one("d1", 0, 15, "C", asOf.Add(time.Millisecond), event.Witnessed, event.Witnessed)},
				minutes("d1", base+60, 12, "final", "A", event.Witnessed)...),
			want: Credit{AMS: limit},
		},
		{
			name: "a pending slot not yet PendingWindow old takes its room under the cap but shows nothing",
			revs: append(minutes("d1", base, 1, "pending", "A", event.Witnessed),
				minutes("d1", base+60, 12, "final", "A", event.Witnessed)...),
			at:   time.Unix(base+60, 0).Add(PendingWindow() - time.Millisecond),
			want: Credit{AMS: 11 * 60_000},
		},
	}
	for _, engine := range storetest.Engines() {
		for _, tt := range tests {
			t.Run(string(engine)+"/"+tt.name, func(t *testing.T) {
				withCap(t, limit)
				db := newDB(t, engine, "u1")
				storeRevisions(t, db, tt.revs)
				at := tt.at
				if at.IsZero() {
					at = asOf
				}
				got, err := PublicCreditAsOf(context.Background(), db.Read, "u1", day, at.UnixMilli())
				if err != nil {
					t.Fatal(err)
				}
				if got != tt.want {
					t.Errorf("PublicCreditAsOf = %+v, want %+v", got, tt.want)
				}
				// The rows are the dashboard's at the read's time when no
				// revision came after it.
				current := true
				for _, r := range tt.revs {
					if r.received.After(at) {
						current = false
					}
				}
				switch dash := dashboardAB(t, db, "u1", day); {
				case tt.dashboard && dash != tt.want.Total():
					t.Errorf("the dashboard credits the day %d ms at levels A and B, want %d", dash, tt.want.Total())
				case current && dash < got.Total():
					t.Errorf("the page shows %d ms, more than the %d ms the dashboard credits", got.Total(), dash)
				}
			})
		}
	}
}
