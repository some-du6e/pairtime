// SPDX-License-Identifier: AGPL-3.0-or-later

package ledger

import (
	"encoding/binary"
	"errors"
	"math/rand/v2"
	"os"
	"slices"
	"testing"

	"github.com/some-du6e/pairtime/pkg/ledger"
)

func TestReadRecords(t *testing.T) {
	const (
		late = testT + 11*3_600_000 + 59*60_000 // 2026-11-19 23:59 UTC
		next = testT + 12*3_600_000 + 60_000    // 2026-11-20 00:01 UTC
	)
	// shown is what ReadRecords gave: each record's seq, segment and batch.
	type shown struct {
		seq     uint64
		segment string
		batch   uint64
	}
	// writeAt writes an os.presence record with the clock at ms, and
	// returns its seq.
	writeAt := func(t *testing.T, w *Writer, c *clock, rng *rand.Rand, ms int64) uint64 {
		t.Helper()
		c.set(ms)
		s, _, err := write(w, rng, "os.presence", ms)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	seal := func(t *testing.T, w *Writer, c *clock, ms int64) {
		t.Helper()
		c.set(ms)
		if _, _, err := w.Seal(); err != nil {
			t.Fatal(err)
		}
	}
	tests := []struct {
		name    string
		from    string
		setup   func(t *testing.T, dir string, w *Writer, c *clock, rng *rand.Rand)
		want    []shown
		wantErr error
	}{
		{
			name: "every record is read in order, sealed ones with their batch and the tail no batch holds yet with none",
			setup: func(t *testing.T, dir string, w *Writer, c *clock, rng *rand.Rand) {
				writeAt(t, w, c, rng, testT)
				writeAt(t, w, c, rng, testT+1000)
				seal(t, w, c, testT+2000)
				writeAt(t, w, c, rng, testT+3000)
			},
			want: []shown{{1, "2026-11-19", 1}, {2, "2026-11-19", 1}, {3, "2026-11-19", 0}},
		},
		{
			name: "a batch whose records span midnight gives each its own segment and the batch of the seal in the next one",
			setup: func(t *testing.T, dir string, w *Writer, c *clock, rng *rand.Rand) {
				writeAt(t, w, c, rng, late)
				writeAt(t, w, c, rng, next)
				seal(t, w, c, next+1000)
			},
			want: []shown{{1, "2026-11-19", 1}, {2, "2026-11-20", 1}},
		},
		{
			name: "after the segment that held a batch's first records is deleted, its last records still take its number",
			setup: func(t *testing.T, dir string, w *Writer, c *clock, rng *rand.Rand) {
				writeAt(t, w, c, rng, late)
				writeAt(t, w, c, rng, late+1000)
				writeAt(t, w, c, rng, next)
				seal(t, w, c, next+1000)
				writeAt(t, w, c, rng, next+2000)
				seal(t, w, c, next+3000)
				w.Close()
				if err := os.Remove(segPath(dir, "2026-11-19")); err != nil {
					t.Fatal(err)
				}
			},
			want: []shown{{3, "2026-11-20", 1}, {4, "2026-11-20", 2}},
		},
		{
			name: "from leaves out the segments before its day, and the batch read from the middle keeps its number",
			from: "2026-11-20",
			setup: func(t *testing.T, dir string, w *Writer, c *clock, rng *rand.Rand) {
				writeAt(t, w, c, rng, late)
				writeAt(t, w, c, rng, next)
				seal(t, w, c, next+1000)
			},
			want: []shown{{2, "2026-11-20", 1}},
		},
		{
			name: "a ledger with no segments has no records",
			setup: func(t *testing.T, dir string, w *Writer, c *clock, rng *rand.Rand) {
				w.Close()
				if err := os.RemoveAll(dir); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "a segment before the newest that ends torn is damage, said once the rest is read, and its waiting records lose their seal",
			setup: func(t *testing.T, dir string, w *Writer, c *clock, rng *rand.Rand) {
				writeAt(t, w, c, rng, late)
				writeAt(t, w, c, rng, late+1000)
				seal(t, w, c, late+2000)
				writeAt(t, w, c, rng, late+3000)
				writeAt(t, w, c, rng, next)
				seal(t, w, c, next+1000)
				w.Close()
				name := segPath(dir, "2026-11-19")
				fi, err := os.Stat(name)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Truncate(name, fi.Size()-3); err != nil {
					t.Fatal(err)
				}
			},
			want:    []shown{{1, "2026-11-19", 1}, {2, "2026-11-19", 1}, {4, "2026-11-20", 2}},
			wantErr: ErrDamaged,
		},
		{
			name: "a seal that closes more records than its segment has, after a seal it read, is damage",
			setup: func(t *testing.T, dir string, w *Writer, c *clock, rng *rand.Rand) {
				writeAt(t, w, c, rng, testT)
				seal(t, w, c, testT+1000)
				writeAt(t, w, c, rng, testT+2000)
				seal(t, w, c, testT+3000)
				w.Close()
				// A third seal claims a record the segment doesn't have.
				sp := []byte(`{"b":3,"first_seq":3,"n":1}`)
				appendTo(t, segPath(dir, "2026-11-19"), appendFrame(nil, kindSeal, sp))
			},
			want:    []shown{{1, "2026-11-19", 1}, {2, "2026-11-19", 2}},
			wantErr: ErrDamaged,
		},
		{
			name: "a record of a kind added after this version is read, with its rtype",
			setup: func(t *testing.T, dir string, w *Writer, c *clock, rng *rand.Rand) {
				writeAt(t, w, c, rng, testT)
				seal(t, w, c, testT+1000)
				w.Close()
				var h ledger.Header
				binary.BigEndian.PutUint64(h[:8], 2)
				h[8] = 0x13
				body := []byte(`{"n":"` + string(make64('0')) + `","e":{"t":1795089602000}}`)
				appendTo(t, segPath(dir, "2026-11-19"), appendFrame(nil, kindRecord, append(h[:], body...)))
			},
			want: []shown{{1, "2026-11-19", 1}, {2, "2026-11-19", 0}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			c := newClock(testT)
			w := open(t, dir, c)
			tt.setup(t, dir, w, c, rand.New(rand.NewPCG(1, 2)))
			var got []shown
			err := ReadRecords(dir, tt.from, func(s Stored) error {
				got = append(got, shown{s.Record.Header.Seq(), s.Segment, s.Batch})
				return nil
			})
			if (tt.wantErr == nil) != (err == nil) || (tt.wantErr != nil && !errors.Is(err, tt.wantErr)) {
				t.Errorf("ReadRecords returned %v, want %v", err, tt.wantErr)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("ReadRecords gave %+v, want %+v", got, tt.want)
			}
		})
	}
}

// appendTo appends b to the file name.
func appendTo(t *testing.T, name string, b []byte) {
	t.Helper()
	f, err := os.OpenFile(name, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Write(b); err != nil {
		t.Fatal(err)
	}
}

// make64 is 64 copies of c, a nonce's length in hex.
func make64(c byte) []byte {
	b := make([]byte, 64)
	for i := range b {
		b[i] = c
	}
	return b
}
