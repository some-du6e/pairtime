// SPDX-License-Identifier: AGPL-3.0-or-later

package ledger

import (
	"crypto/ed25519"
	"math/rand/v2"
	"testing"

	"github.com/some-du6e/pairtime/pkg/ledger"
)

func TestReadRows(t *testing.T) {
	seed := make([]byte, ed25519.SeedSize)
	copy(seed, "pairtime ledger rows test server")
	server := ed25519.NewKeyFromSeed(seed)
	dir := t.TempDir()
	c := newClock(testT)
	var delivered []Batch
	w := open(t, dir, c, func(o *Options) { o.Sealed = func(b Batch) { delivered = append(delivered, b) } })
	rng := rand.New(rand.NewPCG(3, 3))
	for i := range 3 {
		c.set(testT + int64(i)*60_000)
		if _, _, err := write(w, rng, "os.presence", testT+int64(i)*60_000); err != nil {
			t.Fatal(err)
		}
		if _, ok, err := w.Seal(); err != nil || !ok {
			t.Fatalf("seal %d: %v, %v", i, ok, err)
		}
	}
	rc, err := ledger.SignReceipt(server, w.DeviceID(), 1, delivered[0].Row.Head, uint64(testT)+250)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.StoreReceipt(1, rc); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		from uint64
		test func(t *testing.T, info ChainInfo, rows []Row)
	}{
		{"every row comes back in order, from 0 as from 1", 0, func(t *testing.T, info ChainInfo, rows []Row) {
			if info.DeviceID != w.DeviceID() || len(rows) != 3 {
				t.Fatalf("device %s, %d rows", info.DeviceID, len(rows))
			}
			for i, r := range rows {
				if r.B != uint64(i+1) || r.Head != delivered[i].Row.Head || r.Root != delivered[i].Row.Root {
					t.Errorf("row %d is batch %d", i, r.B)
				}
			}
		}},
		{"each row's prev is the head before it, zero for batch 1", 1, func(t *testing.T, _ ChainInfo, rows []Row) {
			if rows[0].Prev != (ledger.Hash{}) || rows[1].Prev != rows[0].Head || rows[2].Prev != rows[1].Head {
				t.Error("the prevs don't chain")
			}
		}},
		{"a stored receipt comes back with its row, and the others have none", 1, func(t *testing.T, _ ChainInfo, rows []Row) {
			if rows[0].Receipt == nil || *rows[0].Receipt != rc || rows[1].Receipt != nil {
				t.Errorf("receipts %v, %v", rows[0].Receipt, rows[1].Receipt)
			}
		}},
		{"a read from a later batch starts there", 3, func(t *testing.T, _ ChainInfo, rows []Row) {
			if len(rows) != 1 || rows[0].B != 3 {
				t.Errorf("rows %v", rows)
			}
		}},
		{"a read past the last batch gets none", 9, func(t *testing.T, _ ChainInfo, rows []Row) {
			if len(rows) != 0 {
				t.Errorf("rows %v", rows)
			}
		}},
		{"RowOf gives a sealed batch's row as ReadRows reads it", 2, func(t *testing.T, _ ChainInfo, rows []Row) {
			got := RowOf(delivered[1], delivered[0].Row.Head)
			want := rows[0]
			if got.B != want.B || got.Head != want.Head || got.Prev != want.Prev || !got.SealedAt.Equal(want.SealedAt) {
				t.Errorf("RowOf = %+v, ReadRows = %+v", got, want)
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			info, rows, err := ReadRows(dir, tc.from)
			if err != nil {
				t.Fatal(err)
			}
			tc.test(t, info, rows)
		})
	}
}
