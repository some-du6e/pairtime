// SPDX-License-Identifier: AGPL-3.0-or-later

package uploader

import (
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
		name       string
		sealed     []float64 // minutes of day0
		got        []float64 // minutes of day0, -1 for no receipt
		chainStart uint64
		offline    []span
		minute     int
		want       event.Witness
		known      bool
	}{
		{"a minute whose next batch arrived within the lag is witnessed",
			[]float64{1, 2}, []float64{1.1, 2.1}, 0, nil, 0, event.Witnessed, true},
		{"the batch that counts is the first one sealed after the minute ends",
			[]float64{0.5, 1, 30}, []float64{0.6, 40, 40}, 0, nil, 0, event.Backfilled, true},
		{"a batch received just at the lag is still witnessed",
			[]float64{1}, []float64{16}, 0, nil, 0, event.Witnessed, true},
		{"a batch sealed on time, received late, with failure notes in between, is offline",
			[]float64{1}, []float64{120}, 0, []span{{From: at(30), To: at(90)}}, 0, event.Offline, true},
		{"a late batch with no failure notes is backfilled, as in local-only mode",
			[]float64{1}, []float64{120}, 0, nil, 0, event.Backfilled, true},
		{"failure notes outside the wait don't make it offline",
			[]float64{1}, []float64{120}, 0, []span{{From: at(200), To: at(210)}}, 0, event.Backfilled, true},
		{"a minute sealed long after it, as lookback is, is backfilled even with failure notes",
			[]float64{600}, []float64{601}, 0, []span{{From: at(600), To: at(600)}}, 0, event.Backfilled, true},
		{"a minute before the server's chain starts is backfilled",
			[]float64{1, 2}, []float64{-1, 2.1}, 2, nil, 0, event.Backfilled, true},
		{"a minute no batch was sealed after waits",
			[]float64{1}, []float64{1.1}, 0, nil, 5, "", false},
		{"a minute whose batch has no receipt yet waits",
			[]float64{1}, []float64{-1}, 0, nil, 0, "", false},
		{"a clock that went back between seals doesn't hide the batch after",
			[]float64{10, 3, 20}, []float64{10.1, 10.2, 20.1}, 0, nil, 5, event.Witnessed, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ix := indexOf(t, tc.sealed, tc.got)
			w, ok := witnessOf(ix, at(tc.minute), lag, tc.chainStart, tc.offline)
			if w != tc.want || ok != tc.known {
				t.Errorf("witnessOf = %q, %v; want %q, %v", w, ok, tc.want, tc.known)
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
