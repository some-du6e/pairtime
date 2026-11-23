// SPDX-License-Identifier: AGPL-3.0-or-later

package ledger

import (
	"bytes"
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

// verifyFrom checks the chain in dir from its base, with the head before
// it taken as given, as pkg/ledger verifies a run that starts later.
func verifyFrom(t *testing.T, dir string) []Batch {
	t.Helper()
	info, rows, err := ReadRows(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 {
		t.Fatal("no row")
	}
	batches := readAll(t, dir)
	chain := ledger.Chain{DeviceID: info.DeviceID, Prev: rows[0].Prev}
	for _, b := range batches {
		chain.Batches = append(chain.Batches, ledger.BatchOf(b.Row, b.Records))
	}
	if err := chain.Verify(ledger.Options{Keys: keysOf(t, testKDev)}); err != nil {
		t.Fatalf("pkg/ledger refuses the kept chain: %v", err)
	}
	return batches
}

func TestSweep(t *testing.T) {
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
				dropped, segs, err := w.Sweep(days[2])
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
			name: "the newest batch stays when every batch is older than from",
			run: func(t *testing.T, dir string) {
				c := newClock(testT)
				w := open(t, dir, c)
				days, batches := writeDays(t, w, c, 3)
				if _, _, err := w.Sweep(dayOf(time.UnixMilli(testT + 30*sweepDayMS))); err != nil {
					t.Fatal(err)
				}
				_, rows, err := ReadRows(dir, 0)
				if err != nil || len(rows) != 1 || rows[0].B != batches[2] {
					t.Errorf("ReadRows: %d rows, %v; want batch %d alone", len(rows), err, batches[2])
				}
				if got, _ := segments(dir); !slices.Equal(got, days[2:]) {
					t.Errorf("segments %v, want %v", got, days[2:])
				}
			},
		},
		{
			name: "the writer goes on after a sweep, and a reopened one goes on from where it stopped",
			run: func(t *testing.T, dir string) {
				c := newClock(testT)
				w := open(t, dir, c)
				days, _ := writeDays(t, w, c, 4)
				if _, _, err := w.Sweep(days[2]); err != nil {
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
			name: "a receipt for a batch the sweep dropped is ignored, and a kept batch's receipt stays",
			run: func(t *testing.T, dir string) {
				c := newClock(testT)
				w := open(t, dir, c)
				days, batches := writeDays(t, w, c, 3)
				rc := ledger.Receipt{ReceivedAt: uint64(testT)}
				if err := w.StoreReceipt(batches[2], rc); err != nil {
					t.Fatal(err)
				}
				if _, _, err := w.Sweep(days[1]); err != nil {
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
				data, err := keptFile(bf, 2)
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
				if _, segs, err := w.Sweep(days[2]); err != nil || segs != 2 {
					t.Errorf("the next Sweep deleted %d segments, %v; want the 2 left", segs, err)
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
				if dropped, segs, err := w.Sweep(third); err != nil || dropped != 2 || segs != 2 {
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
				if _, _, err := w.Sweep(days[2]); err != nil {
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
			name: "a sweep refuses a from that isn't a UTC day",
			run: func(t *testing.T, dir string) {
				w := open(t, dir, newClock(testT))
				if _, _, err := w.Sweep("yesterday"); err == nil {
					t.Error("Sweep took yesterday")
				}
			},
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) { tt.run(t, t.TempDir()) })
	}
}

func TestBaseFrame(t *testing.T) {
	cases := []struct {
		name string
		edit func(chain, base, row []byte) []byte // builds batches.seg from its frames
		ok   bool
	}{
		{"a base frame right after the chain frame is read", func(c, b, r []byte) []byte { return cat(c, b, r) }, true},
		{"a base frame anywhere else is damage", func(c, b, r []byte) []byte { return cat(c, r, b) }, false},
		{"a base frame with no row after it is damage", func(c, b, r []byte) []byte { return cat(c, b) }, false},
		{"a row that doesn't start at the base is damage", func(c, b, r []byte) []byte { return cat(c, b, r, r) }, false},
	}
	dir := t.TempDir()
	c := newClock(testT)
	w := open(t, dir, c)
	_, _ = writeDays(t, w, c, 3)
	w.Close()
	bf, err := readBatchesFile(filepath.Join(dir, batchesName))
	if err != nil {
		t.Fatal(err)
	}
	data, err := keptFile(bf, 1)
	if err != nil {
		t.Fatal(err)
	}
	var frames [][]byte
	fr := newFrameReader(bytes.NewReader(data), 0)
	for {
		fm, err := fr.next()
		if err != nil {
			break
		}
		frames = append(frames, data[fm.off:fm.end])
	}
	if len(frames) < 3 {
		t.Fatalf("keptFile wrote %d frames", len(frames))
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			name := filepath.Join(t.TempDir(), batchesName)
			if err := os.WriteFile(name, tt.edit(frames[0], frames[1], frames[2]), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := readBatchesFile(name)
			if (err == nil) != tt.ok {
				t.Errorf("readBatchesFile: %v, want ok %v", err, tt.ok)
			}
		})
	}
}

func cat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}
