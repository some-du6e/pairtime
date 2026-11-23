// SPDX-License-Identifier: AGPL-3.0-or-later

package uploader

import (
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/ledger"
	pkgledger "github.com/some-du6e/pairtime/pkg/ledger"
)

// TestRowIndexBase checks a row index of a ledger whose oldest batches a
// retention sweep dropped, so its rows start at batch 5 (WP-LG6): it takes
// them from there and answers for them, and for nothing before.
func TestRowIndexBase(t *testing.T) {
	ix := &rowIndex{device: "d_test", base: 5}
	for b := uint64(5); b <= 7; b++ {
		r := ledger.Row{
			BatchRow: pkgledger.BatchRow{B: b, FirstSeq: 10 * b, N: 1},
			SealedAt: day0.Add(time.Duration(b) * time.Minute),
		}
		if b == 6 {
			r.Receipt = &pkgledger.Receipt{ReceivedAt: uint64(at(6))}
		}
		if err := ix.add(r); err != nil {
			t.Fatalf("add(%d): %v", b, err)
		}
	}
	received, ok6 := ix.receivedAt(6)
	_, ok5 := ix.receivedAt(5)
	sealed, okSealed := ix.sealedAt(7)
	after, okAfter := ix.sealedAfter(at(6))
	cases := []struct {
		name string
		ok   bool
	}{
		{"the newest batch is 7", ix.last() == 7},
		{"a batch before the base isn't held", !ix.holdsRow(4)},
		{"the base and the batches after it are held", ix.holdsRow(5) && ix.holdsRow(7) && !ix.holdsRow(8)},
		{"batch 6's receipt time reads back", ok6 && received == at(6)},
		{"batch 5 has no receipt", !ok5},
		{"batch 7's seal time reads back", okSealed && sealed == at(7)},
		{"the first batch sealed at or after minute 6 is 6", okAfter && after == 6},
		{"a row that doesn't follow the newest is refused", ix.add(ledger.Row{BatchRow: pkgledger.BatchRow{B: 9}}) != nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !c.ok {
				t.Error("doesn't hold")
			}
		})
	}
	t.Run("an index whose first row isn't its base refuses it", func(t *testing.T) {
		if err := (&rowIndex{base: 5}).add(ledger.Row{BatchRow: pkgledger.BatchRow{B: 1}}); err == nil {
			t.Error("took batch 1 into an index that starts at 5")
		}
	})
}
