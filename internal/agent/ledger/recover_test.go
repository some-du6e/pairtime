// SPDX-License-Identifier: AGPL-3.0-or-later

package ledger

import (
	"bytes"
	"errors"
	"io/fs"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"
)

// quiet are kinds whose records don't seal at once, so a test controls
// which records a batch holds.
var quiet = []string{"os.presence", "agent.tool", "agent.final", "checkpoint", "note", "cli.track", "shell.command"}

// crashed is a ledger a crash left: batch 1 holds records 1 to 5 and was
// sealed; records 6 to 8 were written after it and not sealed; and then
// the writer stopped as a killed process does.
type crashed struct {
	dir, seg string
	batch1   Batch
	// ends[s] is where record s's frame ends in the segment.
	ends map[uint64]int64
}

func crash(t *testing.T) crashed {
	t.Helper()
	dir := t.TempDir()
	w := open(t, dir, newClock(testT))
	rng := rand.New(rand.NewPCG(11, 12))
	c := crashed{dir: dir, seg: segPath(dir, "2026-11-19"), ends: map[uint64]int64{}}
	for i := range 8 {
		s, _, err := write(w, rng, quiet[i%len(quiet)], testT+int64(i)*1000)
		if err != nil {
			t.Fatal(err)
		}
		c.ends[s] = fileSize(t, c.seg)
		if s == 5 {
			if c.batch1, _, err = w.Seal(); err != nil {
				t.Fatal(err)
			}
		}
	}
	abandon(w)
	return c
}

// framesOf reads every whole frame of a file.
func framesOf(t *testing.T, name string) []frame {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	var out []frame
	fr := newFrameReader(bytes.NewReader(data), 0)
	for {
		fm, err := fr.next()
		if err != nil {
			return out
		}
		out = append(out, fm)
	}
}

func truncate(t *testing.T, name string, size int64) {
	t.Helper()
	if err := os.Truncate(name, size); err != nil {
		t.Fatal(err)
	}
}

func appendBytes(t *testing.T, name string, b []byte) {
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

func flipByte(t *testing.T, name string, off int64) {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	data[off] ^= 0x01
	if err := os.WriteFile(name, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestRecover checks what Open does with each thing a crash or a power cut
// can leave, and refuses what neither can (decision E-sm-1.1-seq-gaps).
func TestRecover(t *testing.T) {
	batches := func(c crashed) string { return filepath.Join(c.dir, batchesName) }
	cases := []struct {
		name string
		// edit changes the files of a crashed ledger.
		edit func(t *testing.T, c crashed)
		// survivors is the number of records that survive, and next the
		// sequence number the next record gets.
		survivors int
	}{
		{"records a crash left unsealed survive and are sealed at Open as the next batch",
			func(*testing.T, crashed) {}, 8},
		{"a torn record at the end of the newest segment is cut off, and the next record takes the number after the last whole one",
			func(t *testing.T, c crashed) { truncate(t, c.seg, c.ends[8]-7) }, 7},
		{"a record cut inside its frame's header is cut off",
			func(t *testing.T, c crashed) { truncate(t, c.seg, c.ends[7]+3) }, 7},
		{"the zeros a power cut leaves after the last frame are cut off",
			func(t *testing.T, c crashed) { appendBytes(t, c.seg, make([]byte, 4096)) }, 8},
		{"records a power cut lost after the last seal take their numbers again, from the last one that survived",
			func(t *testing.T, c crashed) {
				// Frames 0 to 4 are records 1 to 5, and frame 5 is batch
				// 1's seal.
				truncate(t, c.seg, framesOf(t, c.seg)[5].end)
			}, 5},
		{"a row a power cut lost from batches.seg is rebuilt from its seal frame",
			func(t *testing.T, c crashed) { truncate(t, batches(c), framesOf(t, batches(c))[0].end) }, 8},
		{"a torn row at the end of batches.seg is cut off and rebuilt",
			func(t *testing.T, c crashed) { truncate(t, batches(c), fileSize(t, batches(c))-5) }, 8},
		{"a torn seal frame leaves the records it closed unsealed, and Open seals them as that batch again",
			func(t *testing.T, c crashed) {
				// The seal frame of batch 1 is torn, so its row was never
				// written; records 6 to 8 came after and go too.
				truncate(t, batches(c), framesOf(t, batches(c))[0].end)
				truncate(t, c.seg, c.ends[5]+9)
			}, 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := crash(t)
			tc.edit(t, c)
			w := open(t, c.dir, newClock(testT+60_000))
			st := w.Status()
			if st.Unsealed != 0 || st.Next != uint64(tc.survivors)+1 {
				t.Fatalf("after Open: %+v, want everything sealed and next_seq %d", st, tc.survivors+1)
			}
			rng := rand.New(rand.NewPCG(13, 14))
			if s, _, err := write(w, rng, "os.presence", testT+60_000); err != nil || s != uint64(tc.survivors)+1 {
				t.Fatalf("the next record got s = %d and %v, want %d", s, err, tc.survivors+1)
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			got := verify(t, c.dir, keysOf(t, testKDev))
			if len(records(got)) != tc.survivors+1 {
				t.Errorf("%d records are sealed, want %d", len(records(got)), tc.survivors+1)
			}
			// Batch 1 holds records 1 to 5 whichever way it was sealed:
			// the same records give the same head.
			if got[0].Row.Head != c.batch1.Row.Head || got[0].Row.N != 5 {
				t.Errorf("batch 1 is %+v, want the batch sealed before the crash, %+v", got[0].Row, c.batch1.Row)
			}
		})
	}
}

// TestRecoverRefuses checks the ledgers Open won't write to.
func TestRecoverRefuses(t *testing.T) {
	cases := []struct {
		name string
		edit func(t *testing.T, c crashed)
		opts func(*Options)
		want error
	}{
		{name: "a changed byte in a sealed record is damage",
			edit: func(t *testing.T, c crashed) { flipByte(t, c.seg, c.ends[2]-20) },
			want: ErrDamaged},
		{name: "a seal whose records are missing is damage",
			edit: func(t *testing.T, c crashed) { truncate(t, c.seg, c.ends[3]) },
			want: ErrDamaged},
		{name: "segments without batches.seg are damage, since a new chain under the same key would number them again",
			edit: func(t *testing.T, c crashed) {
				if err := os.Remove(filepath.Join(c.dir, batchesName)); err != nil {
					t.Fatal(err)
				}
			},
			want: ErrDamaged},
		{name: "the chain's key id with another key's keys is damage",
			opts: func(o *Options) { o.Keys = keysOf(t, otherKDev) },
			want: ErrDamaged},
		{name: "an empty batches.seg beside segments is damage, not a chain of no key, since a new chain under the same key would number them again",
			edit: func(t *testing.T, c crashed) { truncate(t, filepath.Join(c.dir, batchesName), 0) },
			want: ErrDamaged},
		{name: "an empty batches.seg with no segments is damage too, since its chain frame was written whole before any record",
			edit: func(t *testing.T, c crashed) {
				truncate(t, filepath.Join(c.dir, batchesName), 0)
				if err := os.Remove(c.seg); err != nil {
					t.Fatal(err)
				}
			},
			want: ErrDamaged},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := crash(t)
			if tc.edit != nil {
				tc.edit(t, c)
			}
			edit := []func(*Options){}
			if tc.opts != nil {
				edit = append(edit, tc.opts)
			}
			w, err := openErr(c.dir, newClock(testT+60_000), edit...)
			if err == nil {
				w.Close()
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("Open: %v, want %v", err, tc.want)
			}
		})
	}

	t.Run("an empty batches.seg leaves every file where it is, and ReadChain refuses it too", func(t *testing.T) {
		c := crash(t)
		name := filepath.Join(c.dir, batchesName)
		truncate(t, name, 0)
		if w, err := openErr(c.dir, newClock(testT+60_000)); !errors.Is(err, ErrDamaged) {
			if err == nil {
				w.Close()
			}
			t.Fatalf("Open: %v, want ErrDamaged", err)
		}
		if _, err := os.Stat(filepath.Join(c.dir, oldDir)); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Open made %s: %v", oldDir, err)
		}
		if _, err := os.Stat(name); err != nil {
			t.Errorf("batches.seg moved: %v", err)
		}
		if _, err := os.Stat(c.seg); err != nil {
			t.Errorf("the segment moved: %v", err)
		}
		if info, err := ReadChain(c.dir); !errors.Is(err, ErrDamaged) {
			t.Errorf("ReadChain: %+v, %v; want ErrDamaged", info, err)
		}
	})

	t.Run("a torn frame in a segment older than the newest is damage", func(t *testing.T) {
		dir := t.TempDir()
		clk := newClock(testT)
		w := open(t, dir, clk)
		rng := rand.New(rand.NewPCG(15, 16))
		for i := range 3 {
			if _, _, err := write(w, rng, "os.presence", testT+int64(i)); err != nil {
				t.Fatal(err)
			}
		}
		if _, _, err := w.Seal(); err != nil {
			t.Fatal(err)
		}
		// Unsealed records on the first day, and then on the next.
		for range 2 {
			if _, _, err := write(w, rng, "os.presence", testT); err != nil {
				t.Fatal(err)
			}
		}
		clk.set(testT + 86_400_000)
		if _, _, err := write(w, rng, "os.presence", testT+86_400_000); err != nil {
			t.Fatal(err)
		}
		abandon(w)
		day1 := segPath(dir, "2026-11-19")
		truncate(t, day1, fileSize(t, day1)-3)
		if w, err := openErr(dir, clk); !errors.Is(err, ErrDamaged) {
			if err == nil {
				w.Close()
			}
			t.Fatalf("Open: %v, want ErrDamaged", err)
		}
	})
}

// TestKeyChange checks decision E-sm-1.1-seq-gaps for a new K_dev: the old
// chain is kept for reading, and a new device starts at 1.
func TestKeyChange(t *testing.T) {
	dir := t.TempDir()
	clk := newClock(testT)
	w := open(t, dir, clk)
	rng := rand.New(rand.NewPCG(17, 18))
	writeRandom(t, w, rng, 20, 4, testT)
	oldDevice := w.DeviceID()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	oldRecords := len(records(readAll(t, dir)))
	other := func(o *Options) { o.Keys, o.KeyID = keysOf(t, otherKDev), otherKeyID }

	t.Run("a new key moves the old chain to old/<key id>, and its new device's chain starts at 1", func(t *testing.T) {
		w := open(t, dir, clk, other)
		if w.DeviceID() == oldDevice {
			t.Errorf("the new chain kept device id %s", oldDevice)
		}
		if s, _, err := write(w, rng, "os.presence", testT); err != nil || s != 1 {
			t.Fatalf("the new chain's first record got s = %d and %v, want 1", s, err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		verify(t, dir, keysOf(t, otherKDev))
		old := filepath.Join(dir, oldDir, testKeyID)
		if got := len(records(verify(t, old, keysOf(t, testKDev)))); got != oldRecords {
			t.Errorf("the old chain kept %d records, want %d", got, oldRecords)
		}
	})
	t.Run("the old key can't start a chain again, and the chain it finds stays where it is", func(t *testing.T) {
		if w, err := openErr(dir, clk); !errors.Is(err, ErrKeyReuse) {
			if err == nil {
				w.Close()
			}
			t.Fatalf("Open with the old key: %v, want ErrKeyReuse", err)
		}
		info, err := ReadChain(dir)
		if err != nil || info.KeyID != otherKeyID {
			t.Errorf("the ledger's chain is now %+v, %v; want the new key's", info, err)
		}
	})
	t.Run("an archive a crash cut off after moving batches.seg is finished at the next Open", func(t *testing.T) {
		old := filepath.Join(dir, oldDir, testKeyID)
		if err := os.Rename(old, old+partialSuffix); err != nil {
			t.Fatal(err)
		}
		w := open(t, dir, clk, other)
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(old, batchesName)); err != nil {
			t.Errorf("the archive wasn't finished: %v", err)
		}
	})
}

// TestReadBatchesFrom checks reading the batches from one on, as the
// scorer does to catch up after a restart.
func TestReadBatchesFrom(t *testing.T) {
	dir := t.TempDir()
	w := open(t, dir, newClock(testT))
	rng := rand.New(rand.NewPCG(19, 20))
	for i := range 4 {
		for range i + 1 {
			if _, _, err := write(w, rng, "os.presence", testT); err != nil {
				t.Fatal(err)
			}
		}
		if _, _, err := w.Seal(); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := write(w, rng, "os.presence", testT); err != nil {
		t.Fatal(err)
	}
	abandon(w) // record 11 is left unsealed
	all := readAll(t, dir)
	cases := []struct {
		name string
		from uint64
		want []uint64
	}{
		{"from batch 1, every sealed batch, and no unsealed record", 1, []uint64{1, 2, 3, 4}},
		{"from batch 0, which doesn't exist, every sealed batch too", 0, []uint64{1, 2, 3, 4}},
		{"from batch 3, batches 3 and 4", 3, []uint64{3, 4}},
		{"from a batch not sealed yet, none", 5, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got []uint64
			if err := ReadBatches(dir, c.from, func(b Batch) error {
				got = append(got, b.Row.B)
				if b.Row != all[b.Row.B-1].Row || len(b.Records) != int(b.Row.N) {
					t.Errorf("batch %d reads back as %+v with %d records, not %+v", b.Row.B, b.Row, len(b.Records), all[b.Row.B-1].Row)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if len(got) != len(c.want) || (len(got) > 0 && got[0] != c.want[0]) {
				t.Errorf("read batches %v, want %v", got, c.want)
			}
		})
	}
	if n := len(records(all)); n != 10 {
		t.Errorf("%d sealed records, want 10", n)
	}
}
