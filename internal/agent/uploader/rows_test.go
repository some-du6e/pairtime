// SPDX-License-Identifier: AGPL-3.0-or-later

package uploader

import (
	"slices"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/ledger"
	"github.com/some-du6e/pairtime/pkg/event"
	pkgledger "github.com/some-du6e/pairtime/pkg/ledger"
)

// day0 is the start of the synthetic day the tests use.
var day0 = time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC)

// at is minute m of day0, in Unix ms.
func at(m int) int64 { return day0.Add(time.Duration(m) * time.Minute).UnixMilli() }

// indexOf builds a row index of batches sealed at the given minutes of
// day0, with receipts received at got[i] minutes, or none where got[i] is
// negative.
func indexOf(t *testing.T, sealed []float64, got []float64) *rowIndex {
	t.Helper()
	ix := &rowIndex{device: "d_test"}
	for i, s := range sealed {
		r := ledger.Row{
			BatchRow: pkgledger.BatchRow{B: uint64(i + 1), FirstSeq: uint64(i + 1), N: 1},
			SealedAt: day0.Add(time.Duration(s * float64(time.Minute))),
		}
		if got[i] >= 0 {
			r.Receipt = &pkgledger.Receipt{ReceivedAt: uint64(day0.Add(time.Duration(got[i] * float64(time.Minute))).UnixMilli())}
		}
		if err := ix.add(r); err != nil {
			t.Fatal(err)
		}
	}
	return ix
}

func TestWitness(t *testing.T) {
	const lag = 900_000
	tests := []struct {
		name    string
		sealed  []float64 // minutes of day0
		got     []float64 // minutes of day0 on the server's clock, -1 for no receipt
		r       reading   // lag is set for every case
		created int       // the minute of day0 the chain began, 0 for unknown
		batch   uint64    // the batch that wrote the revision, 0 for B*
		minute  int
		want    event.Witness
		known   bool
	}{
		{"a minute whose next batch arrived within the lag is witnessed",
			[]float64{1, 2}, []float64{1.1, 2.1}, reading{}, 0, 0, 0, event.Witnessed, true},
		{"the batch that counts is the first one sealed after the minute ends",
			[]float64{0.5, 1, 30}, []float64{0.6, 40, 40}, reading{}, 0, 0, 0, event.Backfilled, true},
		{"a batch received just at the lag is still witnessed",
			[]float64{1}, []float64{16}, reading{}, 0, 0, 0, event.Witnessed, true},
		{"a batch sealed on time that failed to go within the lag, and went late, is offline",
			[]float64{1}, []float64{120}, reading{offline: []span{{From: at(2), To: at(110)}}}, 0, 0, 0, event.Offline, true},
		{"a late batch with no failure notes is backfilled, as in local-only mode",
			[]float64{1}, []float64{120}, reading{}, 0, 0, 0, event.Backfilled, true},
		{"a failure only in the catch-up after a stretch of sending nothing doesn't make it offline",
			[]float64{1}, []float64{120}, reading{offline: []span{{From: at(119), To: at(119)}}}, 0, 0, 0, event.Backfilled, true},
		{"a minute sealed long after it, as lookback is, is backfilled even with failure notes",
			[]float64{600}, []float64{601}, reading{offline: []span{{From: at(600), To: at(601)}}}, 0, 0, 0, event.Backfilled, true},
		{"a minute before the server's chain starts is backfilled",
			[]float64{1, 2}, []float64{-1, 2.1}, reading{chainStart: 2}, 0, 0, 0, event.Backfilled, true},
		{"a minute whose batch is at or after a fork is backfilled, since the server never takes it",
			[]float64{1, 2}, []float64{1.1, -1}, reading{fork: 2}, 0, 0, 1, event.Backfilled, true},
		{"a minute whose batch is before the fork reads by its batch",
			[]float64{1, 2}, []float64{1.1, -1}, reading{fork: 2}, 0, 0, 0, event.Witnessed, true},
		{"a device clock 20 minutes slow reads its live minutes late, as the server reads them by the device's clock",
			[]float64{1}, []float64{21.1}, reading{}, 0, 0, 0, event.Backfilled, true},
		{"a minute that ends more than 5 minutes after the server had its batch is backfilled, since a submission refuses its records",
			[]float64{10}, []float64{3}, reading{}, 0, 0, 9, event.Backfilled, true},
		{"a minute that ends up to 5 minutes after the server had its batch is witnessed",
			[]float64{10}, []float64{6}, reading{}, 0, 0, 9, event.Witnessed, true},
		// A clock 6 minutes fast that seals at :50, each root received 15 s
		// after its seal: B* reached the server 4 min 55 s before the
		// minute's end, but the batch sealed at 10:50 did 5 min 45 s before
		// its seal, which holds most of the minute's records.
		{"a minute is backfilled when an earlier batch of it reached the server more than 5 minutes before its seal, though B* didn't",
			[]float64{9 + 50.0/60, 10 + 50.0/60, 11 + 50.0/60}, []float64{4 + 5.0/60, 5 + 5.0/60, 6 + 5.0/60}, reading{}, 0, 0, 10, event.Backfilled, true},
		{"the same minute on a clock 4.5 minutes fast is witnessed",
			[]float64{9 + 50.0/60, 10 + 50.0/60, 11 + 50.0/60}, []float64{5 + 35.0/60, 6 + 35.0/60, 7 + 35.0/60}, reading{}, 0, 0, 10, event.Witnessed, true},
		{"a minute no batch was sealed after waits",
			[]float64{1}, []float64{1.1}, reading{}, 0, 0, 5, "", false},
		{"a minute whose batch has no receipt yet waits",
			[]float64{1}, []float64{-1}, reading{}, 0, 0, 0, "", false},
		{"a clock that went back between seals doesn't hide the batch after",
			[]float64{10, 3, 20}, []float64{10.1, 10.2, 20.1}, reading{}, 0, 0, 5, event.Witnessed, true},
		{"a minute that ended before the chain began is lookback's, and backfilled however soon its batch arrived",
			[]float64{6}, []float64{6.1}, reading{}, 5, 0, 3, event.Backfilled, true},
		{"a minute that ended after the chain began reads by its batch",
			[]float64{6}, []float64{6.1}, reading{}, 5, 0, 5, event.Witnessed, true},
		{"a failure while its batch was due, then a night asleep and a sync the next morning, is offline whatever the flush delay (D6)",
			[]float64{1}, []float64{600}, reading{offline: []span{{From: at(1) + 30_000, To: at(1) + 30_000}}}, 0, 0, 0, event.Offline, true},
		{"a later revision written by a batch that waited out an outage is offline, though its minute's first batch went at once",
			[]float64{1, 5}, []float64{1.1, 90}, reading{offline: []span{{From: at(6), To: at(85)}}}, 0, 2, 0, event.Offline, true},
		{"a later revision written by a batch the server got at once keeps its minute's reading",
			[]float64{1, 5}, []float64{1.1, 5.1}, reading{}, 0, 2, 0, event.Witnessed, true},
		{"a later revision written by a batch with no receipt yet waits",
			[]float64{1, 5}, []float64{1.1, -1}, reading{}, 0, 2, 0, "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ix := indexOf(t, tc.sealed, tc.got)
			if tc.created > 0 {
				ix.created = at(tc.created)
			}
			tc.r.lag = lag
			w, ok := witnessOf(ix, at(tc.minute), tc.batch, tc.r)
			if w != tc.want || ok != tc.known {
				t.Errorf("witnessOf = %q, %v; want %q, %v", w, ok, tc.want, tc.known)
			}
		})
	}
}

// TestOutage is PLAN's two-hour outage, read on the device: a batch a
// minute for four hours, the server down from hour 1 to hour 3 while the
// uploader's tries fail every few minutes, and everything held back sent
// in one catch-up when it comes back. The minutes of the outage read
// offline, and the minutes before and after it witnessed.
func TestOutage(t *testing.T) {
	const lag = 900_000
	var sealed, got []float64
	for m := 1; m <= 240; m++ {
		sealed = append(sealed, float64(m))
		switch {
		case m < 60 || m > 180:
			got = append(got, float64(m)+0.05)
		default:
			got = append(got, 180.5) // the catch-up
		}
	}
	ix := indexOf(t, sealed, got)
	var spans []span
	s := saved{}
	for m := 60.0; m <= 180; m += 4 {
		t := at(0) + int64(m*60_000)
		s.noteFailure(t, t, len(s.Offline) > 0)
	}
	spans = s.Offline
	tests := []struct {
		name   string
		minute int
		want   event.Witness
	}{
		{"a minute an hour before the outage is witnessed", 0, event.Witnessed},
		{"the minute just before the outage, whose batch waited through it, is offline", 59, event.Offline},
		{"a minute in the first hour of the outage is offline", 90, event.Offline},
		{"a minute in its second hour is offline", 150, event.Offline},
		{"a minute whose batch reached the server within the lag of its end, even at the outage's end, is witnessed", 170, event.Witnessed},
		{"a minute after the server came back is witnessed", 200, event.Witnessed},
	}
	if len(spans) != 1 {
		t.Fatalf("the failures make %d spans, not one", len(spans))
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w, ok := witnessOf(ix, at(tc.minute), 0, reading{lag: lag, offline: spans})
			if !ok || w != tc.want {
				t.Errorf("witnessOf = %q, %v; want %q", w, ok, tc.want)
			}
		})
	}
}

// TestTwoCatchUps is TestOutage's outage on a network that let the device
// through at minute 180.5 for batches 60 to 120 only, then dropped it
// again, the uploader failing every four minutes, until minute 200.5, when
// the rest went: the device reads both catch-ups offline, as the server
// does from what DESIGN 6.5's rule (g) reveals (internal/server/witness).
func TestTwoCatchUps(t *testing.T) {
	const lag = 900_000
	var sealed, got []float64
	for m := 1; m <= 240; m++ {
		sealed = append(sealed, float64(m))
		switch {
		case m >= 60 && m <= 120:
			got = append(got, 180.5)
		case m >= 121 && m <= 200:
			got = append(got, 200.5)
		default:
			got = append(got, float64(m)+0.05)
		}
	}
	ix := indexOf(t, sealed, got)
	s := saved{}
	for m := 60.0; m <= 200; m += 4 {
		ms := at(0) + int64(m*60_000)
		// The catch-up at 180.5 got through, so the failure after it opens
		// a span of its own.
		s.noteFailure(ms, ms, len(s.Offline) > 0 && m != 184)
	}
	if len(s.Offline) != 2 {
		t.Fatalf("the failures make %d spans, not two", len(s.Offline))
	}
	tests := []struct {
		name   string
		minute int
		want   event.Witness
	}{
		{"a minute of the first catch-up is offline", 119, event.Offline},
		{"a minute of the second catch-up is offline", 125, event.Offline},
		{"a minute of the second catch-up that a submission of minutes 170 to 180 holds is offline", 170, event.Offline},
		{"a minute whose batch reached the server within the lag of its end is witnessed", 190, event.Witnessed},
		{"a minute after the server came back is witnessed", 210, event.Witnessed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w, ok := witnessOf(ix, at(tc.minute), 0, reading{lag: lag, offline: s.Offline})
			if !ok || w != tc.want {
				t.Errorf("witnessOf = %q, %v; want %q", w, ok, tc.want)
			}
		})
	}
}

func TestSpans(t *testing.T) {
	hour := int64(time.Hour / time.Millisecond)
	tests := []struct {
		name     string
		failures []int64 // ms after day0
		waits    []int64 // how long after each the uploader waits, 0 when nil
		open     []bool  // whether each follows a failure with no success between
		want     []span
	}{
		{"failures in a row make one span", []int64{0, 60_000, 600_000}, nil, []bool{false, true, true},
			[]span{{From: at(0), To: at(0) + 600_000}}},
		{"a success between failures starts a new span", []int64{0, 60_000}, nil, []bool{false, false},
			[]span{{From: at(0), To: at(0)}, {From: at(0) + 60_000, To: at(0) + 60_000}}},
		{"failures further apart than any backoff, as across a night asleep, are two spans", []int64{0, 5 * hour}, nil, []bool{false, true},
			[]span{{From: at(0), To: at(0)}, {From: at(0) + 5*hour, To: at(0) + 5*hour}}},
		{"a failure the server answered with an hour's Retry-After covers the wait", []int64{0}, []int64{hour}, []bool{false},
			[]span{{From: at(0), To: at(0) + hour}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var s saved
			for i, f := range tc.failures {
				var wait int64
				if tc.waits != nil {
					wait = tc.waits[i]
				}
				s.noteFailure(at(0)+f, at(0)+f+wait, tc.open[i])
			}
			if !slices.Equal(s.Offline, tc.want) {
				t.Errorf("spans %v, want %v", s.Offline, tc.want)
			}
		})
	}
}

func TestRowIndex(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T, ix *rowIndex)
	}{
		{"rows come back from the batch asked for", func(t *testing.T, ix *rowIndex) {
			rows := ix.rowsFrom(2, 10)
			if len(rows) != 3 || rows[0].B != 2 || rows[2].B != 4 {
				t.Errorf("rowsFrom(2) = %v", rows)
			}
		}},
		{"rowsFrom gives at most n", func(t *testing.T, ix *rowIndex) {
			if rows := ix.rowsFrom(1, 2); len(rows) != 2 {
				t.Errorf("%d rows", len(rows))
			}
		}},
		{"a trimmed index forgets the rows before and keeps their times", func(t *testing.T, ix *rowIndex) {
			ix.trim(3)
			if ix.holds(2) || !ix.holds(3) || !ix.holds(5) {
				t.Error("holds is wrong after trim")
			}
			if rows := ix.rowsFrom(3, 10); len(rows) != 2 || rows[0].B != 3 {
				t.Errorf("rowsFrom(3) after trim = %v", rows)
			}
			if _, ok := ix.receivedAt(1); !ok {
				t.Error("batch 1's receipt time went with its row")
			}
		}},
		{"a batch that doesn't follow the newest is refused", func(t *testing.T, ix *rowIndex) {
			if err := ix.add(ledger.Row{BatchRow: pkgledger.BatchRow{B: 9}}); err == nil {
				t.Error("batch 9 followed batch 4")
			}
		}},
		{"the next row's prev is the newest head", func(t *testing.T, ix *rowIndex) {
			if r, _ := ix.row(4); ix.lastHead != r.Head {
				t.Error("lastHead isn't batch 4's head")
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ix := &rowIndex{device: "d_test"}
			for b := uint64(1); b <= 4; b++ {
				var head pkgledger.Hash
				head[0] = byte(b)
				r := ledger.Row{BatchRow: pkgledger.BatchRow{B: b, Head: head, Receipt: &pkgledger.Receipt{ReceivedAt: uint64(b)}}, SealedAt: day0}
				if err := ix.add(r); err != nil {
					t.Fatal(err)
				}
			}
			tc.run(t, ix)
		})
	}
}
