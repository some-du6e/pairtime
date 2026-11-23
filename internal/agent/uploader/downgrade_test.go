// SPDX-License-Identifier: AGPL-3.0-or-later

package uploader

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/crc32"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/ledger"
)

// TestDowngradeAndBack is an install that starts local-only, a downgrade
// to a version whose seal frames record no local-only stretches, pairtime
// login there, and an upgrade back (D-late-local). The newest row this
// version sealed records the install's stretch going on, and the rows the
// older version sealed after it end that stretch where their moment rose,
// at the read that found config.toml online, and pass it. So the upgraded
// writer seals the next batch online and the batch roots go on, rather
// than stop at a local-only batch after the older version's online ones
// until the next sign-in. A late record made after that end is sealed
// online, and one made in the install's stretch is dropped.
func TestDowngradeAndBack(t *testing.T) {
	cases := []struct {
		name string
		// made is when the late record was made, after the rig's start.
		made time.Duration
		// dropped is whether the upgraded writer drops it.
		dropped bool
	}{
		{"a record made while the older version uploaded, which reaches the upgraded writer late, is sealed online, and the roots go on", 50 * time.Minute, false},
		{"one made in the install's stretch is dropped, and the roots go on", 5 * time.Minute, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newLiveRig(t, 90)
			// The install: local-only and signed out. Batch 1's row records
			// the install's stretch going on.
			r.linked.Store(false)
			r.local.Store(true)
			r.u.Start()
			r.seal(t, 1)
			// The downgrade, and pairtime login there, which sets config.toml
			// online: the older version seals batches 2 to 4, whose rows
			// record the stretch's end as their moment, and uploads them from
			// the first sealed online.
			r.link(r.sealedAt(1).Add(time.Minute))
			r.local.Store(false)
			for k := 2; k <= 4; k++ {
				r.seal(t, k)
			}
			r.u.Wake()
			r.wait(t, "batch 4", func(f *fakeIngest) bool { return f.head != nil && f.head.b == 4 })
			// The upgrade. The older version's rows record no stretches.
			r.up.Store(nil)
			r.u.Close()
			r.w.Close()
			olderRows(t, r.dirs.Ledger(), 2)
			r.openWriter(t, r.dirs)
			r.openUploader(t, r.dirs)
			r.u.Start()
			r.setClock(r.sealedAt(5).Add(-time.Minute))
			_, err := r.w.Write(hookCall(r.start.Add(c.made)))
			if err != nil && !errors.Is(err, ledger.ErrLateLocal) {
				t.Fatal(err)
			}
			dropped := err != nil
			r.seal(t, 5)
			r.u.Wake()
			r.wait(t, "batch 5", func(f *fakeIngest) bool { return f.head != nil && f.head.b == 5 })
			r.drain()
			_, rows, err := ledger.ReadRows(r.dirs.Ledger(), 5)
			if err != nil || len(rows) != 1 {
				t.Fatalf("ReadRows: %d rows, %v", len(rows), err)
			}
			r.f.mu.Lock()
			defer r.f.mu.Unlock()
			checks := []struct {
				name string
				ok   bool
			}{
				{"the upgraded writer drops the late record only when the install's stretch holds it", dropped == c.dropped},
				{"it seals batch 5 online", rows[0].Online},
				{"the server took batch 5's root, so the batch roots go on", r.f.head.b == 5 && r.f.rows[5].b == 5},
				{"doctor doesn't hear that uploads stopped", !strings.Contains(r.doctor(), "stopped uploading")},
				{"the uploader doesn't say the roots stop", r.u.Status().Stopped == 0},
			}
			for _, ch := range checks {
				t.Run(ch.name, func(t *testing.T) {
					if !ch.ok {
						t.Errorf("no: dropped %v; batch 5 online %v; the server holds head %d from %d; doctor hears %q",
							dropped, rows[0].Online, r.f.head.b, r.f.start, r.doctor())
					}
				})
			}
		})
	}
}

// olderRows rewrites the rows of the ledger's batches.seg in dir from batch
// from on without the local-only stretches they record, as an older
// version's rows, which record none. The segments keep their seal frames:
// Open rebuilds no row here, so it reads none of theirs. A frame is
// u8(kind) || u32(len) || payload || u32(CRC-32C), big-endian, and a row's
// kind is 'b'.
func olderRows(t *testing.T, dir string, from uint64) {
	t.Helper()
	name := filepath.Join(dir, "batches.seg")
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	table := crc32.MakeTable(crc32.Castagnoli)
	var out []byte
	for len(data) > 0 {
		if len(data) < 9 {
			t.Fatalf("batches.seg ends in %d bytes that aren't a frame", len(data))
		}
		kind, n := data[0], int(binary.BigEndian.Uint32(data[1:5]))
		if len(data) < 9+n {
			t.Fatalf("batches.seg has a frame of %d bytes with %d left", n, len(data)-9)
		}
		payload := data[5 : 5+n]
		data = data[9+n:]
		if kind == 'b' {
			var row map[string]json.RawMessage
			var b struct {
				B uint64 `json:"b"`
			}
			if err := json.Unmarshal(payload, &row); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(payload, &b); err != nil {
				t.Fatal(err)
			}
			if b.B >= from {
				delete(row, "stretches")
				if payload, err = json.Marshal(row); err != nil {
					t.Fatal(err)
				}
			}
		}
		frame := binary.BigEndian.AppendUint32([]byte{kind}, uint32(len(payload)))
		frame = append(frame, payload...)
		out = append(out, binary.BigEndian.AppendUint32(frame, crc32.Checksum(frame, table))...)
	}
	if err := os.WriteFile(name, out, 0o600); err != nil {
		t.Fatal(err)
	}
}
