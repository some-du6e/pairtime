// SPDX-License-Identifier: AGPL-3.0-or-later

package ledger

import (
	"bytes"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/pkg/ledger"
)

// sweepDayMS is a day in Unix milliseconds.
const sweepDayMS = 24 * 3600 * 1000

// writeDays writes a presence sample a day on each of n UTC days from
// testT on, sealing each day's batch, and returns the days and the
// batches. A presence sample, unlike a prompt, doesn't seal at once, so
// each Seal here seals it.
func writeDays(t *testing.T, w *Writer, c *clock, n int) (days []string, batches []uint64) {
	t.Helper()
	rng := rand.New(rand.NewPCG(uint64(n), 11))
	for i := range n {
		at := testT + int64(i)*sweepDayMS
		c.set(at)
		if _, err := w.Write(sampleInput(rng, "os.presence", at, "", "")); err != nil {
			t.Fatal(err)
		}
		b, ok, err := w.Seal()
		if err != nil || !ok {
			t.Fatalf("sealing day %d: %v, %v", i, ok, err)
		}
		days = append(days, dayOf(time.UnixMilli(at)))
		batches = append(batches, b.Row.B)
	}
	return days, batches
}

// verifyFrom checks the chain in dir from the first batch whose records
// it holds, with the head before it taken as given, as pkg/ledger verifies
// a run that starts later.
func verifyFrom(t *testing.T, dir string) []Batch {
	t.Helper()
	info, rows, err := ReadRows(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	batches := readAll(t, dir)
	if len(batches) == 0 {
		t.Fatal("no batch")
	}
	chain := ledger.Chain{DeviceID: info.DeviceID}
	for _, r := range rows {
		if r.B == batches[0].Row.B {
			chain.Prev = r.Prev
		}
	}
	for _, b := range batches {
		chain.Batches = append(chain.Batches, ledger.BatchOf(b.Row, b.Records))
	}
	if err := chain.Verify(ledger.Options{Keys: keysOf(t, testKDev)}); err != nil {
		t.Fatalf("pkg/ledger refuses the kept chain: %v", err)
	}
	return batches
}

// chainV returns the version batches.seg's chain frame in dir says.
func chainV(t *testing.T, dir string) int {
	t.Helper()
	bf, err := readBatchesFile(filepath.Join(dir, batchesName))
	if err != nil {
		t.Fatal(err)
	}
	return bf.chain.V
}

func TestSweep(t *testing.T) {
	// after is a day after every day writeDays writes, up to 5.
	after := dayOf(time.UnixMilli(testT + 30*sweepDayMS))
	cases := []struct {
		name string
		run  func(t *testing.T, dir string)
	}{
		{
			name: "the batches whose first record is before from go with their segments, and the rest stays, from a base",
			run: func(t *testing.T, dir string) {
				c := newClock(testT)
				w := open(t, dir, c)
				days, batches := writeDays(t, w, c, 5)
				dropped, segs, err := w.Sweep(days[2], 0)
				if err != nil || dropped != 2 || segs != 2 {
					t.Fatalf("Sweep = %d batches, %d segments, %v; want 2 and 2", dropped, segs, err)
				}
				info, err := ReadChain(dir)
				if err != nil || info.Base != batches[2] {
					t.Errorf("ReadChain: base %d, %v; want %d", info.Base, err, batches[2])
				}
				if got, _ := segments(dir); !slices.Equal(got, days[2:]) {
					t.Errorf("segments %v, want %v", got, days[2:])
				}
				kept := verifyFrom(t, dir)
				if len(kept) != 3 || kept[0].Row.B != batches[2] {
					t.Errorf("ReadBatches gives %d batches from %d, want 3 from %d", len(kept), kept[0].Row.B, batches[2])
				}
				if b, err := FirstHeld(dir, 0); err != nil || b != batches[2] {
					t.Errorf("FirstHeld(0) = %d, %v; want %d", b, err, batches[2])
				}
			},
		},
		{
			name: "when every batch is older than from, every row and segment goes, and the base names the next batch, which links to the head before it",
			run: func(t *testing.T, dir string) {
				c := newClock(testT)
				w := open(t, dir, c)
				_, batches := writeDays(t, w, c, 3)
				_, before, err := ReadRows(dir, 0)
				if err != nil {
					t.Fatal(err)
				}
				if dropped, segs, err := w.Sweep(after, 0); err != nil || dropped != 3 || segs != 3 {
					t.Fatalf("Sweep = %d batches, %d segments, %v; want 3 and 3", dropped, segs, err)
				}
				info, rows, err := ReadRows(dir, 0)
				if err != nil || len(rows) != 0 || info.Base != batches[2]+1 {
					t.Fatalf("ReadRows: %d rows from base %d, %v; want none, and base %d", len(rows), info.Base, err, batches[2]+1)
				}
				if got, _ := segments(dir); len(got) != 0 {
					t.Errorf("segments %v left", got)
				}
				c.set(testT + 40*sweepDayMS)
				rng := rand.New(rand.NewPCG(3, 4))
				if _, err := w.Write(sampleInput(rng, "os.presence", testT+40*sweepDayMS, "", "")); err != nil {
					t.Fatal(err)
				}
				b, ok, err := w.Seal()
				if err != nil || !ok || b.Row.B != batches[2]+1 {
					t.Fatalf("the next seal: batch %d, %v, %v; want batch %d", b.Row.B, ok, err, batches[2]+1)
				}
				w.Close()
				w = open(t, dir, c)
				_, rows, err = ReadRows(dir, 0)
				if err != nil || len(rows) != 1 || rows[0].Prev != before[len(before)-1].Head {
					t.Errorf("ReadRows: %+v, %v; want the new batch alone, after the dropped head", rows, err)
				}
				if kept := verifyFrom(t, dir); len(kept) != 1 {
					t.Errorf("%d batches, want the new one", len(kept))
				}
				if next := w.Status().Next; next != rows[0].FirstSeq+rows[0].N {
					t.Errorf("the reopened writer's next record is %d, want %d", next, rows[0].FirstSeq+rows[0].N)
				}
			},
		},
		{
			name: "the rows from floor on stay whatever their age, for the uploads, and their segments go all the same",
			run: func(t *testing.T, dir string) {
				c := newClock(testT)
				w := open(t, dir, c)
				_, batches := writeDays(t, w, c, 5)
				_, before, err := ReadRows(dir, 0)
				if err != nil {
					t.Fatal(err)
				}
				if dropped, segs, err := w.Sweep(after, batches[2]); err != nil || dropped != 2 || segs != 5 {
					t.Fatalf("Sweep = %d batches, %d segments, %v; want 2 and 5", dropped, segs, err)
				}
				_, rows, err := ReadRows(dir, 0)
				if err != nil || len(rows) != 3 || rows[0].B != batches[2] || rows[2].Head != before[4].Head {
					t.Fatalf("ReadRows: %d rows, %v; want the 3 from batch %d", len(rows), err, batches[2])
				}
				if got, _ := segments(dir); len(got) != 0 {
					t.Errorf("segments %v left", got)
				}
				if got := readAll(t, dir); len(got) != 0 {
					t.Errorf("ReadBatches gives %d batches, whose records went", len(got))
				}
				if b, err := FirstHeld(dir, 0); err != nil || b != 0 {
					t.Errorf("FirstHeld(0) = %d, %v; want 0", b, err)
				}
				// The writer goes on after the kept rows, and so does one that
				// opens without their segments.
				c.set(testT + 40*sweepDayMS)
				rng := rand.New(rand.NewPCG(7, 8))
				if _, err := w.Write(sampleInput(rng, "os.presence", testT+40*sweepDayMS, "", "")); err != nil {
					t.Fatal(err)
				}
				if b, ok, err := w.Seal(); err != nil || !ok || b.Row.B != batches[4]+1 {
					t.Fatalf("the next seal: batch %d, %v, %v; want %d", b.Row.B, ok, err, batches[4]+1)
				}
				w.Close()
				w = open(t, dir, c)
				if kept := verifyFrom(t, dir); len(kept) != 1 || kept[0].Row.B != batches[4]+1 {
					t.Errorf("ReadBatches gives %d batches, want batch %d alone", len(kept), batches[4]+1)
				}
				if _, rows, err := ReadRows(dir, 0); err != nil || len(rows) != 4 {
					t.Errorf("ReadRows: %d rows, %v; want the 3 kept and the new one", len(rows), err)
				}
			},
		},
		{
			name: "a floor before from keeps the rows from the floor, and the segments from from",
			run: func(t *testing.T, dir string) {
				c := newClock(testT)
				w := open(t, dir, c)
				days, batches := writeDays(t, w, c, 5)
				if dropped, segs, err := w.Sweep(days[3], batches[1]); err != nil || dropped != 1 || segs != 3 {
					t.Fatalf("Sweep = %d batches, %d segments, %v; want 1 and 3", dropped, segs, err)
				}
				if _, rows, err := ReadRows(dir, 0); err != nil || len(rows) != 4 || rows[0].B != batches[1] {
					t.Errorf("ReadRows: %d rows, %v; want the 4 from batch %d", len(rows), err, batches[1])
				}
				if got, _ := segments(dir); !slices.Equal(got, days[3:]) {
					t.Errorf("segments %v, want %v", got, days[3:])
				}
				if kept := verifyFrom(t, dir); len(kept) != 2 || kept[0].Row.B != batches[3] {
					t.Errorf("ReadBatches gives %d batches from %d, want 2 from %d", len(kept), kept[0].Row.B, batches[3])
				}
				if b, err := FirstHeld(dir, 0); err != nil || b != batches[3] {
					t.Errorf("FirstHeld(0) = %d, %v; want %d", b, err, batches[3])
				}
			},
		},
		{
			name: "the writer goes on after a sweep, and a reopened one goes on from where it stopped",
			run: func(t *testing.T, dir string) {
				c := newClock(testT)
				w := open(t, dir, c)
				days, _ := writeDays(t, w, c, 4)
				if _, _, err := w.Sweep(days[2], 0); err != nil {
					t.Fatal(err)
				}
				c.set(testT + 4*sweepDayMS)
				s, err := w.Write(prompt("after the sweep", 0))
				if err != nil {
					t.Fatal(err)
				}
				if _, _, err := w.Seal(); err != nil {
					t.Fatal(err)
				}
				if err := w.Close(); err != nil {
					t.Fatal(err)
				}
				w = open(t, dir, c)
				if next := w.Status().Next; next != s+1 {
					t.Errorf("the reopened writer's next record is %d, want %d", next, s+1)
				}
				if _, err := w.Write(prompt("and one more", 0)); err != nil {
					t.Fatal(err)
				}
				w.Close()
				if kept := verifyFrom(t, dir); len(kept) != 4 {
					t.Errorf("%d batches, want the 2 kept, the one after the sweep and the one Close sealed", len(kept))
				}
			},
		},
		{
			name: "records a crash left after a sweep that dropped every batch are sealed at the next Open, past the dropped batch's seal in the segment kept",
			run: func(t *testing.T, dir string) {
				c := newClock(testT)
				w := open(t, dir, c)
				rng := rand.New(rand.NewPCG(9, 10))
				// Batch 1's record is on the first day and its seal on the
				// second, where a record no batch holds yet follows it.
				if _, err := w.Write(sampleInput(rng, "os.presence", testT, "", "")); err != nil {
					t.Fatal(err)
				}
				second := testT + sweepDayMS
				c.set(second)
				if _, ok, err := w.Seal(); err != nil || !ok {
					t.Fatalf("sealing batch 1: %v, %v", ok, err)
				}
				_, before, err := ReadRows(dir, 0)
				if err != nil {
					t.Fatal(err)
				}
				s, err := w.Write(sampleInput(rng, "os.presence", second, "", ""))
				if err != nil {
					t.Fatal(err)
				}
				day2 := dayOf(time.UnixMilli(second))
				if dropped, segs, err := w.Sweep(day2, 0); err != nil || dropped != 1 || segs != 1 {
					t.Fatalf("Sweep = %d batches, %d segments, %v; want 1 and 1", dropped, segs, err)
				}
				if got, _ := segments(dir); !slices.Equal(got, []string{day2}) {
					t.Errorf("segments %v, want %s's alone, which holds the record no batch holds yet", got, day2)
				}
				abandon(w)
				w = open(t, dir, c)
				_, rows, err := ReadRows(dir, 0)
				if err != nil || len(rows) != 1 || rows[0].B != 2 || rows[0].FirstSeq != s || rows[0].Prev != before[0].Head {
					t.Fatalf("ReadRows: %+v, %v; want batch 2 of record %d, after batch 1's head", rows, err, s)
				}
				if kept := verifyFrom(t, dir); len(kept) != 1 || len(kept[0].Records) != 1 {
					t.Errorf("ReadBatches gives %d batches, want batch 2 with its one record", len(kept))
				}
			},
		},
		{
			name: "a receipt for a batch the sweep dropped is ignored, and a kept batch's receipt stays",
			run: func(t *testing.T, dir string) {
				c := newClock(testT)
				w := open(t, dir, c)
				days, batches := writeDays(t, w, c, 3)
				rc := ledger.Receipt{ReceivedAt: uint64(testT)}
				if err := w.StoreReceipt(batches[2], rc); err != nil {
					t.Fatal(err)
				}
				if _, _, err := w.Sweep(days[1], 0); err != nil {
					t.Fatal(err)
				}
				if err := w.StoreReceipt(batches[0], rc); err != nil {
					t.Errorf("StoreReceipt of a dropped batch: %v", err)
				}
				w.Close()
				_, rows, err := ReadRows(dir, 0)
				if err != nil {
					t.Fatalf("ReadRows after a receipt for a dropped batch: %v", err)
				}
				if len(rows) != 2 || rows[1].Receipt == nil || rows[0].Receipt != nil {
					t.Errorf("rows %+v, want batch %d's receipt alone", rows, batches[2])
				}
				open(t, dir, c) // Open takes the file as it is
			},
		},
		{
			name: "a crash after the rename leaves segments nothing reads, which Open takes and the next sweep deletes",
			run: func(t *testing.T, dir string) {
				c := newClock(testT)
				w := open(t, dir, c)
				days, batches := writeDays(t, w, c, 4)
				w.Close()
				bf, err := readBatchesFile(filepath.Join(dir, batchesName))
				if err != nil {
					t.Fatal(err)
				}
				// What Sweep writes before it deletes a segment.
				data, err := keptFile(bf, 2, bf.stretches)
				if err != nil {
					t.Fatal(err)
				}
				if err := home.WriteFile(filepath.Join(dir, batchesName), data); err != nil {
					t.Fatal(err)
				}
				w = open(t, dir, c)
				if kept := verifyFrom(t, dir); len(kept) != 2 || kept[0].Row.B != batches[2] {
					t.Errorf("%d batches from %d, want 2 from %d", len(kept), kept[0].Row.B, batches[2])
				}
				if _, segs, err := w.Sweep(days[2], 0); err != nil || segs != 2 {
					t.Errorf("the next Sweep deleted %d segments, %v; want the 2 left", segs, err)
				}
			},
		},
		{
			name: "the temporary file of a rewrite a crash cut short goes at the next Open",
			run: func(t *testing.T, dir string) {
				c := newClock(testT)
				w := open(t, dir, c)
				writeDays(t, w, c, 1)
				w.Close()
				tmp := filepath.Join(dir, "."+batchesName+".tmp-123")
				if err := os.WriteFile(tmp, []byte("rows and receipts"), 0o600); err != nil {
					t.Fatal(err)
				}
				open(t, dir, c)
				if _, err := os.Stat(tmp); !os.IsNotExist(err) {
					t.Errorf("the temporary file is still there: %v", err)
				}
			},
		},
		{
			name: "a batch sealed in the segment after its first record's goes with that first segment, and what it left in the next is skipped",
			run: func(t *testing.T, dir string) {
				c := newClock(testT)
				w := open(t, dir, c)
				days, _ := writeDays(t, w, c, 1)
				// Batch 2's record is written on the second day and sealed on
				// the third, so its seal frame starts the third day's segment.
				// A presence sample doesn't seal at once, as a prompt would.
				c.set(testT + sweepDayMS)
				rng := rand.New(rand.NewPCG(5, 6))
				if _, err := w.Write(sampleInput(rng, "os.presence", testT+sweepDayMS, "", "")); err != nil {
					t.Fatal(err)
				}
				c.set(testT + 2*sweepDayMS)
				if _, _, err := w.Seal(); err != nil {
					t.Fatal(err)
				}
				if _, err := w.Write(sampleInput(rng, "os.presence", testT+2*sweepDayMS, "", "")); err != nil {
					t.Fatal(err)
				}
				b3, ok, err := w.Seal()
				if err != nil || !ok {
					t.Fatalf("sealing the third day: %v, %v", ok, err)
				}
				third := dayOf(time.UnixMilli(testT + 2*sweepDayMS))
				days = append(days, dayOf(time.UnixMilli(testT+sweepDayMS)), third)
				if dropped, segs, err := w.Sweep(third, 0); err != nil || dropped != 2 || segs != 2 {
					t.Fatalf("Sweep = %d batches, %d segments, %v; want 2 and 2", dropped, segs, err)
				}
				if got, _ := segments(dir); !slices.Equal(got, days[2:]) {
					t.Errorf("segments %v, want %v", got, days[2:])
				}
				w.Close()
				open(t, dir, c)
				if kept := verifyFrom(t, dir); len(kept) != 1 || kept[0].Row.B != b3.Row.B {
					t.Errorf("%d batches, want batch %d alone", len(kept), b3.Row.B)
				}
			},
		},
		{
			name: "a chain of another key in old/ keeps the rows of the batches whose segments it keeps, and loses its temporary file",
			run: func(t *testing.T, dir string) {
				c := newClock(testT)
				chain := oldChain(t, dir, c, 4)
				if err := os.WriteFile(filepath.Join(chain.dir, "."+batchesName+".tmp-9"), []byte("rows"), 0o600); err != nil {
					t.Fatal(err)
				}
				w := open(t, dir, c, otherKey(t))
				if dropped, segs, err := w.Sweep(chain.days[2], 0); err != nil || dropped != 2 || segs != 2 {
					t.Fatalf("Sweep = %d batches, %d segments, %v; want 2 and 2", dropped, segs, err)
				}
				info, rows, err := ReadRows(chain.dir, 0)
				if err != nil || info.Base != chain.batches[2] || len(rows) != 2 {
					t.Errorf("the old chain: %d rows from base %d, %v; want 2 from %d", len(rows), info.Base, err, chain.batches[2])
				}
				if got, _ := segments(chain.dir); !slices.Equal(got, chain.days[2:]) {
					t.Errorf("the old chain keeps segments %v, want %v", got, chain.days[2:])
				}
				if _, err := os.Stat(filepath.Join(chain.dir, "."+batchesName+".tmp-9")); !os.IsNotExist(err) {
					t.Errorf("the temporary file is still there: %v", err)
				}
				if v := chainV(t, chain.dir); v != chainVersionBase {
					t.Errorf("the old chain's chain frame is of version %d, want %d", v, chainVersionBase)
				}
			},
		},
		{
			name: "a chain of another key in old/ whose batches are all older than from goes whole",
			run: func(t *testing.T, dir string) {
				c := newClock(testT)
				chain := oldChain(t, dir, c, 3)
				w := open(t, dir, c, otherKey(t))
				if _, _, err := w.Sweep(after, 0); err != nil {
					t.Fatal(err)
				}
				if _, err := os.Stat(chain.dir); !os.IsNotExist(err) {
					t.Errorf("the old chain is still there: %v", err)
				}
			},
		},
		{
			name: "the chains of other keys in old/ lose their segments before from, and a chain left with none goes",
			run: func(t *testing.T, dir string) {
				c := newClock(testT)
				w := open(t, dir, c)
				days, _ := writeDays(t, w, c, 3)
				w.Close()
				for _, key := range []string{otherKeyID, "0011223344556677"} {
					chain := filepath.Join(dir, oldDir, key)
					if err := os.MkdirAll(chain, 0o700); err != nil {
						t.Fatal(err)
					}
					for _, d := range days[:2] {
						if err := os.WriteFile(segPath(chain, d), []byte("x"), 0o600); err != nil {
							t.Fatal(err)
						}
					}
				}
				// The second chain has a segment of the last day, which stays.
				if err := os.WriteFile(segPath(filepath.Join(dir, oldDir, "0011223344556677"), days[2]), []byte("x"), 0o600); err != nil {
					t.Fatal(err)
				}
				w = open(t, dir, c)
				if _, _, err := w.Sweep(days[2], 0); err != nil {
					t.Fatal(err)
				}
				if _, err := os.Stat(filepath.Join(dir, oldDir, otherKeyID)); !os.IsNotExist(err) {
					t.Errorf("the emptied chain is still there: %v", err)
				}
				if got, _ := segments(filepath.Join(dir, oldDir, "0011223344556677")); !slices.Equal(got, days[2:]) {
					t.Errorf("the other chain keeps %v, want %v", got, days[2:])
				}
			},
		},
		{
			name: "old/ is swept while the live chain has no row yet",
			run: func(t *testing.T, dir string) {
				c := newClock(testT)
				chain := oldChain(t, dir, c, 3)
				w := open(t, dir, c, otherKey(t))
				if _, rows, err := ReadRows(dir, 0); err != nil || len(rows) != 0 {
					t.Fatalf("the live chain has %d rows, %v", len(rows), err)
				}
				if _, _, err := w.Sweep(chain.days[1], 0); err != nil {
					t.Fatal(err)
				}
				if got, _ := segments(chain.dir); !slices.Equal(got, chain.days[1:]) {
					t.Errorf("the old chain keeps segments %v, want %v", got, chain.days[1:])
				}
			},
		},
		{
			name: "a record written while a sweep runs keeps the segment it opened, which the sweep doesn't delete",
			run: func(t *testing.T, dir string) {
				c := newClock(testT)
				w := open(t, dir, c)
				writeDays(t, w, c, 3)
				later := testT + 40*sweepDayMS
				c.set(later)
				var s uint64
				// The record opens the segment of its own day, after the
				// sweep read batches.seg and before it deletes.
				sweepWrites = func() {
					var err error
					if s, err = w.Write(sampleInput(rand.New(rand.NewPCG(13, 14)), "os.presence", later, "", "")); err != nil {
						t.Error(err)
					}
				}
				defer func() { sweepWrites = nil }()
				if _, _, err := w.Sweep(dayOf(time.UnixMilli(testT+50*sweepDayMS)), 0); err != nil {
					t.Fatal(err)
				}
				if got, _ := segments(dir); !slices.Contains(got, dayOf(time.UnixMilli(later))) {
					t.Fatalf("segments %v, want the new record's", got)
				}
				if _, ok, err := w.Seal(); err != nil || !ok {
					t.Fatalf("sealing the new record: %v, %v", ok, err)
				}
				w.Close()
				open(t, dir, c)
				if kept := verifyFrom(t, dir); len(kept) != 1 || kept[0].Row.FirstSeq != s {
					t.Errorf("%d batches, want the new record's alone", len(kept))
				}
			},
		},
		{
			name: "a segment that can't be deleted stops the deletes after it, so the segments left are the newest, and the ledger opens",
			run: func(t *testing.T, dir string) {
				c := newClock(testT)
				w := open(t, dir, c)
				days, batches := writeDays(t, w, c, 4)
				removeFile = func(name string) error {
					if filepath.Base(name) == days[0]+segSuffix {
						return errors.New("injected: the file is in use")
					}
					return os.Remove(name)
				}
				defer func() { removeFile = os.Remove }()
				// The floor keeps every row, and every segment is due.
				if _, segs, err := w.Sweep(after, batches[0]); err == nil || segs != 0 {
					t.Fatalf("Sweep deleted %d segments, %v; want none, and the error", segs, err)
				}
				if got, _ := segments(dir); !slices.Equal(got, days) {
					t.Errorf("segments %v, want all %v", got, days)
				}
				w.Close()
				removeFile = os.Remove
				w = open(t, dir, c)
				if _, segs, err := w.Sweep(after, batches[0]); err != nil || segs != 4 {
					t.Errorf("the next Sweep deleted %d segments, %v; want 4", segs, err)
				}
			},
		},
		{
			name: "a sweep refuses a from that isn't a UTC day",
			run: func(t *testing.T, dir string) {
				w := open(t, dir, newClock(testT))
				if _, _, err := w.Sweep("yesterday", 0); err == nil {
					t.Error("Sweep took yesterday")
				}
			},
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) { tt.run(t, t.TempDir()) })
	}
}

// archived is a chain of testKDev's key moved into a ledger's old/.
type archived struct {
	dir     string
	days    []string
	batches []uint64
}

// oldChain writes n days of testKDev's chain and moves it to dir's
// old/<testKeyID>, as a ledger that otherKey opens next archives it.
func oldChain(t *testing.T, dir string, c *clock, n int) archived {
	t.Helper()
	src := t.TempDir()
	w := open(t, src, c)
	days, batches := writeDays(t, w, c, n)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	a := archived{dir: filepath.Join(dir, oldDir, testKeyID), days: days, batches: batches}
	if err := os.MkdirAll(a.dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range append(slices.Clone(days), "") {
		from, to := filepath.Join(src, batchesName), filepath.Join(a.dir, batchesName)
		if name != "" {
			from, to = segPath(src, name), segPath(a.dir, name)
		}
		if err := os.Rename(from, to); err != nil {
			t.Fatal(err)
		}
	}
	return a
}

// otherKey opens a writer under otherKDev's key.
func otherKey(t *testing.T) func(*Options) {
	return func(o *Options) { o.Keys, o.KeyID = keysOf(t, otherKDev), otherKeyID }
}

func TestBaseFrame(t *testing.T) {
	dir := t.TempDir()
	c := newClock(testT)
	w := open(t, dir, c)
	_, _ = writeDays(t, w, c, 3)
	w.Close()
	bf, err := readBatchesFile(filepath.Join(dir, batchesName))
	if err != nil {
		t.Fatal(err)
	}
	frames := func(data []byte) [][]byte {
		var out [][]byte
		fr := newFrameReader(bytes.NewReader(data), 0)
		for {
			fm, err := fr.next()
			if err != nil {
				return out
			}
			out = append(out, data[fm.off:fm.end])
		}
	}
	orig, err := os.ReadFile(filepath.Join(dir, batchesName))
	if err != nil {
		t.Fatal(err)
	}
	// o: the chain frame of version 1, then the rows of batches 1 to 3.
	o := frames(orig)
	kept, err := keptFile(bf, 1, bf.stretches)
	if err != nil {
		t.Fatal(err)
	}
	// k: the chain frame of version 2, a base frame naming batch 2, then
	// the rows of batches 2 and 3.
	k := frames(kept)
	none, err := keptFile(bf, len(bf.rows), bf.stretches)
	if err != nil {
		t.Fatal(err)
	}
	if len(o) != 4 || len(k) != 4 || len(frames(none)) != 2 {
		t.Fatalf("batches.seg has %d frames, keptFile %d and %d", len(o), len(k), len(frames(none)))
	}
	base := func(b, firstSeq uint64, prev ledger.Hash) []byte {
		p, err := json.Marshal(basePayload{B: b, FirstSeq: firstSeq, Prev: prev})
		if err != nil {
			t.Fatal(err)
		}
		return appendFrame(nil, kindBase, p)
	}
	row2 := bf.rows[1]
	cases := []struct {
		name string
		file []byte
		ok   bool
	}{
		{"a base frame right after a chain frame of version 2 is read", cat(k...), true},
		{"a base frame that names the batch after the last, with no row, is read", none, true},
		{"a base frame after a row is damage", cat(k[0], o[1], k[1]), false},
		{"a base frame after a chain frame of version 1 is damage", cat(o[0], k[1], k[2], k[3]), false},
		{"a chain frame of version 2 with no base frame after it is damage", cat(k[0], o[1], o[2], o[3]), false},
		{"a row that doesn't start at the base is damage", cat(k[0], k[1], k[3]), false},
		{"a base frame whose first record isn't its batch's first is damage", cat(k[0], base(row2.B, row2.FirstSeq+1, row2.Prev), k[2], k[3]), false},
		{"a base frame whose prev isn't its batch's is damage", cat(k[0], base(row2.B, row2.FirstSeq, ledger.Hash{1}), k[2], k[3]), false},
		{"a base frame naming batch 1 with a prev is damage", cat(k[0], base(1, 1, ledger.Hash{1})), false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			name := filepath.Join(t.TempDir(), batchesName)
			if err := os.WriteFile(name, tt.file, 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := readBatchesFile(name)
			if (err == nil) != tt.ok {
				t.Errorf("readBatchesFile: %v, want ok %v", err, tt.ok)
			}
		})
	}
	// pairtime 0.1.0-alpha.5's readBatchesFile takes only a chain frame of
	// version 1, and refuses any other before it reads on, so it never
	// meets the base frame it doesn't know, which it would take for a torn
	// tail and cut off with every row after it.
	t.Run("a swept file's chain frame is of a version pairtime 0.1.0-alpha.5 refuses", func(t *testing.T) {
		var cp chainPayload
		if err := json.Unmarshal(k[0][frameHeadSize:len(k[0])-frameTailSize], &cp); err != nil {
			t.Fatal(err)
		}
		if cp.V == 1 || cp.V != chainVersionBase {
			t.Errorf("the chain frame is of version %d", cp.V)
		}
	})
}

func cat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}
